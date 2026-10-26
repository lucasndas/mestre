package transport_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mestre-ia/mestre/internal/transport"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// CA-PROTO-11 (the Citadel sees the machine_id of the SAN)
func TestTowerAndGuardianConnectByMTLS(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := newGuild(t, clock.Real{})
		srv := g.serve(t)
		ctx := context.Background()

		cl, sv := connect(t, g.client(t, g.tower), srv.tower, transport.RouteTower)
		if p := sv.Peer(); p.Kind != transport.KindMachine || p.MachineID != "tower1" || p.Serial != transport.Serial(g.tower.Leaf()) {
			t.Fatalf("the Citadel sees %+v, want the tower1 engine", p)
		}
		if p := cl.Peer(); p.Kind != transport.KindMachine || p.MachineID != "citadel" {
			t.Fatalf("the Tower sees %+v, want the citadel engine", p)
		}
		if err := cl.Write(ctx, transport.MessageText, []byte(`{"type":"hello"}`)); err != nil {
			t.Fatal(err)
		}
		frame := append([]byte{0x01}, bytes.Repeat([]byte{7}, 64<<10)...)
		if err := sv.Write(ctx, transport.MessageBinary, frame); err != nil {
			t.Fatal(err)
		}
		if typ, p, err := sv.Read(ctx); err != nil || typ != transport.MessageText || string(p) != `{"type":"hello"}` {
			t.Fatalf("Citadel read %v %q %v", typ, p, err)
		}
		if typ, p, err := cl.Read(ctx); err != nil || typ != transport.MessageBinary || !bytes.Equal(p, frame) {
			t.Fatalf("Tower read %v len %d %v", typ, len(p), err)
		}

		gcl, gsv := connect(t, g.client(t, g.guard), srv.guardian, transport.RouteGuardian)
		if p := gsv.Peer(); p.Kind != transport.KindGuardian || p.MachineID != "tower1" {
			t.Fatalf("Guardian route sees %+v", p)
		}
		// Close handshake: the code reaches the other side.
		go func() { _, _, _ = gcl.Read(ctx) }()
		if err := gsv.Close(transport.StatusIncompatibleVersion, "versions 1-1"); err != nil {
			t.Fatal(err)
		}
		<-gcl.Done()
		go func() { _ = cl.Close(transport.StatusStaleEpoch, "epoch 2") }()
		_, _, err := sv.Read(ctx)
		if transport.CloseStatus(err) != transport.StatusStaleEpoch {
			t.Fatalf("read after close = %v, want 4006", err)
		}
	})
}

// CA-PROTO-11
func TestMTLSRoutesRefuseWrongCredentials(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := newGuild(t, clock.Real{})
		srv := g.serve(t)
		go func() {
			for {
				c, err := srv.tower.Accept(context.Background())
				if err != nil {
					return
				}
				t.Errorf("route handed out a connection from %+v", c.Peer())
			}
		}()
		other := newGuild(t, clock.Real{})
		dial := func(c *transport.Client, route string) error {
			_, err := c.Dial(context.Background(), citadelAddr, route)
			return err
		}
		cases := map[string]struct {
			c     *transport.Client
			route string
			want  string
		}{
			"engine certificate of another CA":         {g.client(t, other.tower), transport.RouteTower, "certificate authority"},
			"Guardian certificate on the Tower route":  {g.client(t, g.guard), transport.RouteTower, "403"},
			"engine certificate on the Guardian route": {g.client(t, g.tower), transport.RouteGuardian, "403"},
		}
		for name, tc := range cases {
			err := dial(tc.c, tc.route)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s: dial = %v, want an error with %q", name, err, tc.want)
			}
		}

		// A client that offers only TLS 1.2 fails the handshake.
		raw, err := g.net.Host("tower1").DialContext(context.Background(), citadelAddr)
		if err != nil {
			t.Fatal(err)
		}
		c12 := tls.Client(raw, &tls.Config{MaxVersion: tls.VersionTLS12, InsecureSkipVerify: true})
		if err := c12.Handshake(); err == nil {
			t.Error("TLS 1.2 handshake succeeded")
		}
		_ = raw.Close()

		// A server of another CA is refused by the Tower.
		ln, err := g.net.Host("evil").Listen("evil:7443")
		if err != nil {
			t.Fatal(err)
		}
		evil, err := transport.NewServer(transport.ServerConfig{CA: other.ca.Certificate(), Cert: other.citadel})
		if err != nil {
			t.Fatal(err)
		}
		evil.Route(transport.RouteTower, transport.KindMachine)
		go func() { _ = evil.Serve(ln) }()
		defer evil.Close(transport.StatusGoingAway, "")
		if _, err := g.client(t, g.tower).Dial(context.Background(), "evil:7443", transport.RouteTower); err == nil ||
			!strings.Contains(err.Error(), "unknown authority") {
			t.Fatalf("dial to a server of another CA = %v", err)
		}
	})
}

// CA-PROTO-11 (validity by the injected clock)
func TestExpiredClientCertificateIsRefused(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		clk := clock.NewFake(day0)
		g := newGuild(t, clk)
		g.serve(t)
		clk.Advance(366 * 24 * time.Hour)
		// The Citadel's own certificate expired too; renew it so that only
		// the Tower's is expired.
		cert, key := issueCert(t, g.ca, transport.KindMachine, transport.Names{MachineID: "citadel", Hostname: "citadel"})
		g.citadel.Set(cert, key)
		_, err := g.client(t, g.tower).Dial(context.Background(), citadelAddr, transport.RouteTower)
		if err == nil || !strings.Contains(err.Error(), "expired") {
			t.Fatalf("dial with an expired client certificate = %v", err)
		}
		renewed := issue(t, g.ca, transport.KindMachine, "tower1")
		c, err := g.client(t, renewed).Dial(context.Background(), citadelAddr, transport.RouteTower)
		if err != nil {
			t.Fatalf("dial with the renewed certificate: %v", err)
		}
		_ = c.CloseNow()
	})
}

// CA-PROTO-12
func TestRevocationRefusesHandshakeAndClosesOpenConnections(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := newGuild(t, clock.Real{})
		srv := g.serve(t)
		tower := g.client(t, g.tower)
		cl, _ := connect(t, tower, srv.tower, transport.RouteTower)
		gcl, _ := connect(t, g.client(t, g.guard), srv.guardian, transport.RouteGuardian)
		other := g.client(t, issue(t, g.ca, transport.KindMachine, "tower2"))
		ocl, _ := connect(t, other, srv.tower, transport.RouteTower)
		towerErr, guardErr, otherErr := readLoop(cl), readLoop(gcl), readLoop(ocl)

		g.creds.RevokeMachine("tower1")
		if n := srv.CloseRevoked(); n != 2 {
			t.Fatalf("CloseRevoked closed %d connections, want the engine and the Guardian of tower1", n)
		}
		for name, ch := range map[string]<-chan error{"engine": towerErr, "Guardian": guardErr} {
			if err := <-ch; transport.CloseStatus(err) != transport.StatusRevoked {
				t.Errorf("%s read = %v, want close 4002", name, err)
			}
		}
		select {
		case err := <-otherErr:
			t.Fatalf("tower2 dropped: %v", err)
		default:
		}
		if _, err := tower.Dial(context.Background(), citadelAddr, transport.RouteTower); err == nil {
			t.Fatal("a revoked machine reconnected")
		}
		// The refusal is in the TLS handshake, before any HTTP: the server
		// answers the revoked client certificate with an alert.
		raw, err := g.net.Host("tower1").DialContext(context.Background(), citadelAddr)
		if err != nil {
			t.Fatal(err)
		}
		defer raw.Close()
		tc := tls.Client(raw, &tls.Config{
			MinVersion: tls.VersionTLS13, InsecureSkipVerify: true, NextProtos: []string{"http/1.1"},
			GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
				return g.tower.TLSForTest(), nil
			},
		})
		err = tc.Handshake()
		if err == nil {
			// TLS 1.3: the client finishes first; the server's verdict on
			// its certificate is the next record.
			_, err = tc.Read(make([]byte, 1))
		}
		if err == nil || !strings.Contains(err.Error(), "tls: bad certificate") {
			t.Fatalf("handshake of a revoked machine = %v, want the TLS alert bad certificate", err)
		}
	})
}

// CA-PROTO-13
func TestCACertAndHealthNeedNoClientCertificate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := newGuild(t, clock.Real{})
		g.serve(t)
		// A browser that does not trust the CA yet: it checks the
		// fingerprint shown by the installer instead.
		hc := &http.Client{Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, a string) (net.Conn, error) {
				return g.net.Host("browser").DialContext(ctx, a)
			},
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}}
		defer hc.CloseIdleConnections()
		get := func(path string) (*http.Response, []byte) {
			resp, err := hc.Get("https://" + citadelAddr + path)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			return resp, b
		}
		resp, der := get(transport.RouteCACert)
		if resp.StatusCode != http.StatusOK || !bytes.Equal(der, g.ca.DER()) {
			t.Fatalf("/ca.crt = %d, %d bytes; want the CA in DER", resp.StatusCode, len(der))
		}
		if sha256.Sum256(der) != transport.Fingerprint(g.ca.DER()) {
			t.Fatal("the SHA-256 of the file must be the CA fingerprint")
		}
		if resp, body := get(transport.RouteHealth); resp.StatusCode != http.StatusOK || string(body) != "ok\n" {
			t.Fatalf("/healthz = %d %q", resp.StatusCode, body)
		}
		if resp, _ := get(transport.RouteTower); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("/ws/tower without a certificate = %d, want 401", resp.StatusCode)
		}
	})
}

// CA-PROTO-14
func TestKeepaliveDropsAHalfOpenConnection(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := newGuild(t, clock.Real{})
		srv := g.serve(t)
		cl, sv := connect(t, g.client(t, g.tower), srv.tower, transport.RouteTower)
		t0 := clock.Real{}.Now() // the bubble's fake time; pings at t0 + 15 s·k
		clErr, svErr := readLoop(cl), readLoop(sv)

		// Answered pings keep an idle connection up.
		if err := clock.Sleep(context.Background(), clock.Real{}, 10*time.Minute+5*time.Second); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-clErr:
			t.Fatalf("idle connection dropped: %v", err)
		default:
		}

		lastPong := t0.Add(10 * time.Minute) // the ping of minute 10 was answered
		g.net.HalfOpen("tower1", "citadel")
		for name, ch := range map[string]<-chan error{"Tower": clErr, "Citadel": svErr} {
			err := <-ch
			if !errors.Is(err, transport.ErrNoPong) {
				t.Fatalf("%s read = %v, want ErrNoPong", name, err)
			}
		}
		if d := clock.Since(clock.Real{}, lastPong); d < 45*time.Second || d > 60*time.Second {
			t.Fatalf("dead %v after the last pong, want between 45 s and 60 s (ping every 15 s)", d)
		}
	})
}

// CA-PROTO-17
func TestOversizedMessageIsDiscardedAndTheConnectionStays(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := newGuild(t, clock.Real{})
		srv := g.serve(t)
		ctx := context.Background()
		cl, sv := connect(t, g.clientWith(t, transport.ClientConfig{Cert: g.tower, MaxMessageSize: 4 << 20}), srv.tower, transport.RouteTower)
		big := bytes.Repeat([]byte("x"), transport.MaxMessageSize+1)
		if err := sv.Write(ctx, transport.MessageText, big); !errors.Is(err, transport.ErrTooLarge) {
			t.Fatalf("Write over the limit = %v, want ErrTooLarge", err)
		}
		if err := cl.Write(ctx, transport.MessageText, big); err != nil {
			t.Fatal(err)
		}
		if err := cl.Write(ctx, transport.MessageText, []byte("next")); err != nil {
			t.Fatal(err)
		}
		if _, _, err := sv.Read(ctx); !errors.Is(err, transport.ErrTooLarge) {
			t.Fatalf("Read of 1 MiB + 1 = %v, want ErrTooLarge", err)
		}
		if _, p, err := sv.Read(ctx); err != nil || string(p) != "next" {
			t.Fatalf("the next message = %q, %v", p, err)
		}
		exact := bytes.Repeat([]byte("y"), transport.MaxMessageSize)
		if err := cl.Write(ctx, transport.MessageBinary, exact); err != nil {
			t.Fatal(err)
		}
		if _, p, err := sv.Read(ctx); err != nil || len(p) != transport.MaxMessageSize {
			t.Fatalf("exactly 1 MiB: %d bytes, %v", len(p), err)
		}
	})
}

// A message of exactly 16 MiB is still discarded with the connection up; one
// byte more closes it with 1009 (docs/conectividade/protocolo.md#envelope).
func TestAMessageOver16MiBClosesWith1009(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := newGuild(t, clock.Real{})
		srv := g.serve(t)
		ctx := context.Background()
		cl, sv := connect(t, g.clientWith(t, transport.ClientConfig{Cert: g.tower, MaxMessageSize: 2 * transport.MaxDiscard}), srv.tower, transport.RouteTower)
		go func() {
			_ = cl.Write(ctx, transport.MessageBinary, make([]byte, transport.MaxDiscard))
			_ = cl.Write(ctx, transport.MessageText, []byte("next"))
			_ = cl.Write(ctx, transport.MessageBinary, make([]byte, transport.MaxDiscard+1))
		}()
		clErr := readLoop(cl)
		if _, _, err := sv.Read(ctx); !errors.Is(err, transport.ErrTooLarge) {
			t.Fatalf("Read of 16 MiB = %v, want ErrTooLarge", err)
		}
		if _, p, err := sv.Read(ctx); err != nil || string(p) != "next" {
			t.Fatalf("after 16 MiB the connection must stay: %q, %v", p, err)
		}
		if _, _, err := sv.Read(ctx); !errors.Is(err, transport.ErrTooLarge) {
			t.Fatalf("Read of 16 MiB + 1 = %v, want ErrTooLarge", err)
		}
		if err := <-clErr; transport.CloseStatus(err) != transport.StatusMessageTooBig {
			t.Fatalf("the sender sees %v, want close 1009", err)
		}
	})
}

func TestRenewedCertificateServesNewHandshakesWithoutDroppingOpenOnes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := newGuild(t, clock.Real{})
		srv := g.serve(t)
		ctx := context.Background()
		cl, sv := connect(t, g.client(t, g.tower), srv.tower, transport.RouteTower)
		old := g.tower.Leaf()
		cert, key := issueCert(t, g.ca, transport.KindMachine, transport.Names{MachineID: "tower1", Hostname: "tower1"})
		g.tower.Set(cert, key)
		if err := cl.Write(ctx, transport.MessageText, []byte("still up")); err != nil {
			t.Fatal(err)
		}
		if _, p, err := sv.Read(ctx); err != nil || string(p) != "still up" {
			t.Fatalf("open connection after renewal: %q, %v", p, err)
		}
		_, sv2 := connect(t, g.client(t, g.tower), srv.tower, transport.RouteTower)
		if sv2.Peer().Serial == transport.Serial(old) || sv2.Peer().Serial != transport.Serial(cert) {
			t.Fatal("a new handshake must present the renewed certificate")
		}
	})
}

func TestServerCloseSendsTheCodeToEveryConnection(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := newGuild(t, clock.Real{})
		srv := g.serve(t)
		cl, _ := connect(t, g.client(t, g.tower), srv.tower, transport.RouteTower)
		errs := readLoop(cl)
		if err := srv.Close(transport.StatusShuttingDown, "citadel shutting down"); err != nil {
			t.Fatal(err)
		}
		if err := <-errs; transport.CloseStatus(err) != transport.StatusShuttingDown {
			t.Fatalf("read = %v, want 4005", err)
		}
		if _, err := srv.tower.Accept(context.Background()); !errors.Is(err, transport.ErrListenerClosed) {
			t.Fatalf("Accept after Close = %v", err)
		}
	})
}

func TestSystemNetworkRefusesTheFastSuite(t *testing.T) {
	t.Parallel()
	n := transport.SystemNetwork{Suite: "fast"}
	if _, err := n.Listen("127.0.0.1:0"); !errors.Is(err, transport.ErrNetworkInFastSuite) {
		t.Fatalf("Listen = %v", err)
	}
	if _, err := n.DialContext(context.Background(), "127.0.0.1:1"); !errors.Is(err, transport.ErrNetworkInFastSuite) {
		t.Fatalf("Dial = %v", err)
	}
}

func TestPipeKeepsTheConnContract(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		a, b := transport.Pipe(transport.Peer{Identity: transport.Identity{Kind: transport.KindMachine, MachineID: "citadel"}},
			transport.Peer{Identity: transport.Identity{Kind: transport.KindMachine, MachineID: "tower1"}})
		if a.Peer().MachineID != "citadel" || b.Peer().MachineID != "tower1" {
			t.Fatal("peers")
		}
		_ = a.Write(ctx, transport.MessageText, []byte("1"))
		_ = a.Write(ctx, transport.MessageBinary, []byte("2"))
		if typ, p, _ := b.Read(ctx); typ != transport.MessageText || string(p) != "1" {
			t.Fatal("first message")
		}
		if typ, p, _ := b.Read(ctx); typ != transport.MessageBinary || string(p) != "2" {
			t.Fatal("second message")
		}
		if err := a.Write(ctx, transport.MessageText, make([]byte, transport.MaxMessageSize+1)); !errors.Is(err, transport.ErrTooLarge) {
			t.Fatalf("oversized write = %v", err)
		}
		_ = b.Close(transport.StatusReplaced, "new connection")
		if _, _, err := a.Read(ctx); transport.CloseStatus(err) != transport.StatusReplaced {
			t.Fatalf("read after the peer closed = %v", err)
		}
		<-a.Done()
		<-b.Done()
	})
}
