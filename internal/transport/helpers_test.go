package transport_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"errors"
	"net"
	"testing"

	"github.com/mestre-ia/mestre/internal/testkit/fakenet"
	"github.com/mestre-ia/mestre/internal/transport"
	"github.com/mestre-ia/mestre/pkg/clock"
)

const citadelAddr = "citadel:7443"

// guild is a Guild CA with the certificates of a Citadel and of one Tower
// (engine and Guardian), on a fake network.
type guild struct {
	clk     clock.Clock
	ca      *transport.CA
	net     *fakenet.Network
	creds   *transport.MemCredentials
	citadel *transport.CertSource
	tower   *transport.CertSource
	guard   *transport.CertSource
}

func newGuild(t *testing.T, clk clock.Clock) *guild {
	t.Helper()
	ca, err := transport.NewCA("guild-1", clk)
	if err != nil {
		t.Fatal(err)
	}
	// The network runs on the bubble's clock, as net/http sets deadlines
	// with the time package; clk drives certificate validity and keepalive.
	g := &guild{clk: clk, ca: ca, net: fakenet.New(clock.Real{}), creds: transport.NewMemCredentials()}
	g.citadel = issue(t, ca, transport.KindMachine, "citadel")
	g.tower = issue(t, ca, transport.KindMachine, "tower1")
	g.guard = issue(t, ca, transport.KindGuardian, "tower1")
	return g
}

// issue returns a certificate of kind for machine id, with its own key.
func issue(t *testing.T, ca *transport.CA, k transport.Kind, id string) *transport.CertSource {
	t.Helper()
	cert, key := issueCert(t, ca, k, transport.Names{MachineID: id, Hostname: id, IPs: []net.IP{net.ParseIP("192.168.1.10")}})
	return transport.NewCertSource(cert, key)
}

func issueCert(t *testing.T, ca *transport.CA, k transport.Kind, n transport.Names) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := transport.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	csr, err := transport.NewCSR(key)
	if err != nil {
		t.Fatal(err)
	}
	var cert *x509.Certificate
	if k == transport.KindGuardian {
		cert, err = ca.IssueGuardian(csr, n)
	} else {
		cert, err = ca.IssueMachine(csr, n)
	}
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

// server starts the Citadel's server on the fake network with both routes.
type server struct {
	*transport.Server
	tower, guardian transport.Listener
}

func (g *guild) serve(t *testing.T) *server {
	t.Helper()
	return g.serveWith(t, transport.ServerConfig{})
}

func (g *guild) serveWith(t *testing.T, cfg transport.ServerConfig) *server {
	t.Helper()
	if cfg.CA == nil {
		cfg.CA = g.ca.Certificate()
	}
	if cfg.Cert == nil {
		cfg.Cert = g.citadel
	}
	if cfg.Credentials == nil {
		cfg.Credentials = g.creds
	}
	if cfg.Clock == nil {
		cfg.Clock = g.clk
	}
	s, err := transport.NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := g.net.Host("citadel").Listen(citadelAddr)
	if err != nil {
		t.Fatal(err)
	}
	srv := &server{Server: s, tower: s.Route(transport.RouteTower, transport.KindMachine),
		guardian: s.Route(transport.RouteGuardian, transport.KindGuardian)}
	done := make(chan error, 1)
	go func() { done <- s.Serve(ln) }()
	t.Cleanup(func() {
		_ = s.Close(transport.StatusGoingAway, "test over")
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	return srv
}

func (g *guild) client(t *testing.T, cert *transport.CertSource) *transport.Client {
	t.Helper()
	return g.clientWith(t, transport.ClientConfig{Cert: cert})
}

func (g *guild) clientWith(t *testing.T, cfg transport.ClientConfig) *transport.Client {
	t.Helper()
	if cfg.CA == nil {
		cfg.CA = g.ca.Certificate()
	}
	if cfg.Network == nil {
		cfg.Network = g.net.Host("tower1")
	}
	if cfg.Clock == nil {
		cfg.Clock = g.clk
	}
	c, err := transport.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// connect dials route and accepts on l, returning both ends.
func connect(t *testing.T, c transport.Dialer, l transport.Listener, route string) (client, srv transport.Conn) {
	t.Helper()
	return connectAt(t, c, l, citadelAddr, route)
}

func connectAt(t *testing.T, c transport.Dialer, l transport.Listener, addr, route string) (client, srv transport.Conn) {
	t.Helper()
	ctx := context.Background()
	type res struct {
		c   transport.Conn
		err error
	}
	got := make(chan res, 1)
	go func() {
		s, err := l.Accept(ctx)
		got <- res{s, err}
	}()
	cl, err := c.Dial(ctx, addr, route)
	if err != nil {
		t.Fatalf("dial %s: %v", route, err)
	}
	r := <-got
	if r.err != nil {
		t.Fatalf("accept %s: %v", route, r.err)
	}
	t.Cleanup(func() { _ = cl.CloseNow(); _ = r.c.CloseNow() })
	return cl, r.c
}

// readLoop keeps a reader on c (the keepalive needs one) and reports the
// first error.
func readLoop(c transport.Conn) <-chan error {
	out := make(chan error, 1)
	go func() {
		for {
			if _, _, err := c.Read(context.Background()); err != nil {
				if !errors.Is(err, transport.ErrTooLarge) {
					out <- err
					return
				}
			}
		}
	}()
	return out
}
