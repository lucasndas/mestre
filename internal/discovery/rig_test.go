package discovery_test

import (
	"context"
	"net"
	"sync"
	"testing"

	"github.com/lucasndas/mestre/internal/discovery"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/testkit/discorig"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/internal/testkit/protorig"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/pkg/clock"
)

const (
	guildID    = "guild-1"
	enginePort = 7443
)

// rig is a Guild on the fake network: its CA, and machines with an IP, a TLS
// server, a Responder and, when they are the Citadel, the protocol server.
// Everything runs inside a synctest bubble, so the clock is clock.Real.
type rig struct {
	t   *testing.T
	clk clock.Clock
	ca  *transport.CA
	net *fakenet.Network
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ca, err := transport.NewCA(guildID, clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	return &rig{t: t, clk: clock.Real{}, ca: ca, net: fakenet.New(clock.Real{})}
}

// cert issues a certificate of the Guild of ca for machine id at ip.
func cert(t *testing.T, ca *transport.CA, k transport.Kind, id, ip string) *transport.CertSource {
	t.Helper()
	key, err := transport.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	csr, err := transport.NewCSR(key)
	if err != nil {
		t.Fatal(err)
	}
	n := transport.Names{MachineID: id, Hostname: id, IPs: []net.IP{net.ParseIP(ip)}}
	issue := ca.IssueMachine
	if k == transport.KindGuardian {
		issue = ca.IssueGuardian
	}
	leaf, err := issue(csr, n)
	if err != nil {
		t.Fatal(err)
	}
	return transport.NewCertSource(leaf, key)
}

// machine is one machine of the rig.
type machine struct {
	r    *rig
	host string
	id   string

	mu       sync.Mutex
	ip       string
	role     string
	epoch    uint64
	citadel  string // the Citadel it follows
	citadelI string // and where it is
	guild    string

	tls      *transport.Server
	towers   transport.Listener
	protocol *protocol.Server
	parts    protorig.Parts
	resp     *discovery.Responder
}

// newMachine puts a machine on the network at ip. ca is the CA of its
// certificate: the rig's, unless the test wants a foreign one.
func (r *rig) newMachine(ctx context.Context, host, id, ip, role string, epoch uint64, ca *transport.CA) *machine {
	t := r.t
	t.Helper()
	m := &machine{r: r, host: host, id: id, ip: ip, role: role, epoch: epoch, guild: guildID}
	if role == discovery.RoleCitadel {
		m.citadel, m.citadelI = id, ip
	}
	r.net.SetIP(host, ip)
	var err error
	m.tls, err = transport.NewServer(transport.ServerConfig{CA: ca.Certificate(), Cert: cert(t, ca, transport.KindMachine, id, ip), Clock: r.clk})
	if err != nil {
		t.Fatal(err)
	}
	m.towers = m.tls.Route(transport.RouteTower, transport.KindMachine)
	ln, err := r.net.Host(host).Listen(net.JoinHostPort(host, "7443"))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- m.tls.Serve(ln) }()
	t.Cleanup(func() {
		_ = m.tls.Close(transport.StatusGoingAway, "test over")
		<-done
	})
	return m
}

// serveProtocol runs the protocol server of the machine behind its TLS
// server: as the Citadel it welcomes Towers, otherwise it answers 4007.
func (m *machine) serveProtocol(ctx context.Context) {
	t := m.r.t
	cfg, parts := protorig.Config(m.r.clk, m.id, m.epoch, m.citadel)
	cfg.IsCitadel = func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.role == discovery.RoleCitadel
	}
	m.parts = parts
	srv, err := protocol.NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.protocol = srv
	sctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = srv.Serve(sctx, protocol.FromTransportListener(m.towers)); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

// answer starts the Responder of the machine on the shared port of its host.
func (m *machine) answer(ctx context.Context) {
	t := m.r.t
	resp, err := discovery.StartResponder(ctx, discovery.ResponderConfig{
		Net: discorig.Packets{Host: m.r.net.Host(m.host)}, Clock: m.r.clk,
		Info: func() (discovery.Info, bool) { return m.info(), true },
	})
	if err != nil {
		t.Fatal(err)
	}
	m.resp = resp
	t.Cleanup(resp.Close)
}

func (m *machine) info() discovery.Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	return discovery.Info{
		GuildID: m.guild, MachineID: m.id, Name: m.host, Role: m.role, Epoch: m.epoch, Port: enginePort,
		Citadel:  &discovery.CitadelRef{MachineID: m.citadel, Addrs: []string{m.citadelI}, Port: enginePort},
		Machines: 3, CAFp: "sha256:test",
	}
}

// moveTo changes the IP of the machine, as a DHCP lease that ended.
func (m *machine) moveTo(ip string) {
	m.mu.Lock()
	m.ip = ip
	if m.role == discovery.RoleCitadel {
		m.citadelI = ip
	}
	m.mu.Unlock()
	m.r.net.SetIP(m.host, ip)
}

func (m *machine) addr() string { return net.JoinHostPort(m.ip, "7443") }

// client is the transport client of a Tower of the rig.
func (r *rig) client(host, id string) *transport.Client {
	r.t.Helper()
	c, err := transport.NewClient(transport.ClientConfig{
		CA: r.ca.Certificate(), Cert: cert(r.t, r.ca, transport.KindMachine, id, "192.168.1.50"),
		Network: r.net.Host(host), Clock: r.clk,
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return c
}

// locator builds the Locator of the Tower host, over the fake network.
func (r *rig) locator(host string, c *transport.Client, known func() discovery.Known, mut func(*discovery.LocatorConfig)) *discovery.Locator {
	r.t.Helper()
	cfg := discovery.LocatorConfig{
		Clock: r.clk, Known: known, Verifier: discovery.TLSVerifier{Prober: c},
		Search: discovery.SearchConfig{
			Net: discorig.Packets{Host: r.net.Host(host)}, Browser: discorig.Browser{Host: r.net.Host(host)}, GuildID: guildID,
		},
		Rand: func() float64 { return 0.5 },
	}
	if mut != nil {
		mut(&cfg)
	}
	l, err := discovery.NewLocator(cfg)
	if err != nil {
		r.t.Fatal(err)
	}
	return l
}

func fixed(epoch uint64, citadel string) func() discovery.Known {
	return func() discovery.Known { return discovery.Known{Epoch: epoch, CitadelMachineID: citadel} }
}
