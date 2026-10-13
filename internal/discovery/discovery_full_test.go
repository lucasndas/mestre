//go:build full

package discovery_test

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/brutella/dnssd"

	"github.com/lucasndas/mestre/internal/discovery"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/testkit/protorig"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The full suite of the discovery uses real sockets: UDP on the machine it
// runs on. A runner that does not loop its own broadcast back to a local
// socket (some containers and locked-down networks) cannot run the broadcast
// tests; they say so and skip, and the same criteria run on real machines in
// the heavy suite (the lab of MVP-1). Everything else here must pass.

const wait = 20 * time.Second

func suite() string { return os.Getenv("MESTRE_TEST_SUITE") }

func sysPackets() discovery.SystemPacketNetwork { return discovery.SystemPacketNetwork{Suite: suite()} }

// freeUDPPort returns a UDP port nobody listens on, so the tests do not meet
// a Mestre running on the machine or each other on 7445.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	var lc net.ListenConfig
	pc, err := lc.ListenPacket(t.Context(), "udp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	return pc.LocalAddr().(*net.UDPAddr).Port
}

// requireLocalBroadcast skips the test if a broadcast sent from this machine
// does not reach two sockets that share a port on it.
func requireLocalBroadcast(t *testing.T, port int) {
	t.Helper()
	pn := sysPackets()
	var rx [2]discovery.PacketConn
	for i := range rx {
		c, err := pn.ListenShared(port)
		if err != nil {
			t.Fatalf("two sockets cannot share the discovery port: %v", err)
		}
		defer c.Close()
		rx[i] = c
	}
	tx, err := pn.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	got := make(chan int, 2)
	for i, c := range rx {
		go func() {
			buf := make([]byte, 16)
			if _, _, err := c.ReadFrom(buf); err == nil {
				got <- i
			}
		}()
	}
	ctx, cancel := clock.WithTimeout(t.Context(), clock.Real{}, 2*time.Second)
	defer cancel()
	for _, to := range pn.Broadcasts(port) {
		_ = tx.WriteTo([]byte("probe"), to)
	}
	heard := map[int]bool{}
	for len(heard) < 2 {
		select {
		case i := <-got:
			heard[i] = true
		case <-ctx.Done():
			t.Skipf("this runner does not deliver a UDP broadcast to the local sockets (heard by %d of 2): the criterion runs on the lab machines in the heavy suite", len(heard))
		}
	}
}

func startRealResponder(t *testing.T, port int, info discovery.Info) {
	t.Helper()
	r, err := discovery.StartResponder(t.Context(), discovery.ResponderConfig{Net: sysPackets(), Port: port, Clock: clock.Real{},
		Info: func() (discovery.Info, bool) { return info, true }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
}

func realInfo(id, role string, tcpPort int) discovery.Info {
	return discovery.Info{
		GuildID: guildID, MachineID: id, Name: id, Role: role, Epoch: 1, Port: tcpPort,
		Citadel:  &discovery.CitadelRef{MachineID: "citadel", Addrs: []string{"127.0.0.1"}, Port: tcpPort},
		Machines: 3, CAFp: "sha256:test",
	}
}

// CA-DESC-06, on Linux and on Windows: a real Citadel and a real Tower on the
// same machine, without a configured address and with an empty cache.
func TestATowerFindsTheCitadelOfTheSameMachineByBroadcastAndConnects(t *testing.T) {
	t.Parallel()
	port := freeUDPPort(t)
	requireLocalBroadcast(t, port)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	clk := clock.Real{}
	sys := transport.SystemNetwork{Suite: suite()}

	ca, err := transport.NewCA(guildID, clk)
	if err != nil {
		t.Fatal(err)
	}
	// the Citadel: TLS server on every address, protocol server, responder
	srvTLS, err := transport.NewServer(transport.ServerConfig{CA: ca.Certificate(), Cert: cert(t, ca, transport.KindMachine, "citadel", "127.0.0.1"), Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	towers := srvTLS.Route(transport.RouteTower, transport.KindMachine)
	ln, err := sys.Listen("0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	tcpPort := ln.Addr().(*net.TCPAddr).Port
	served := make(chan struct{})
	go func() { _ = srvTLS.Serve(ln); close(served) }()
	defer func() { _ = srvTLS.Close(transport.StatusGoingAway, "test over"); <-served }()
	cfgC, _ := protorig.Config(clk, "citadel", 1, "citadel")
	cit, err := protocol.NewServer(cfgC)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = cit.Serve(ctx, protocol.FromTransportListener(towers)) }()
	startRealResponder(t, port, realInfo("citadel", discovery.RoleCitadel, tcpPort))

	// the Tower starts: nothing configured, nothing cached
	cfgT, partsT := protorig.Config(clk, "tower1", 1, "citadel")
	tc, err := transport.NewClient(transport.ClientConfig{CA: ca.Certificate(), Cert: cert(t, ca, transport.KindMachine, "tower1", "127.0.0.1"), Network: sys, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	known := func() discovery.Known {
		return discovery.Known{Epoch: partsT.Epochs.Epoch(), CitadelMachineID: partsT.Epochs.Certificate().CitadelMachineID}
	}
	loc, err := discovery.NewLocator(discovery.LocatorConfig{
		Clock: clk, Known: known, Verifier: discovery.TLSVerifier{Prober: tc},
		Search: discovery.SearchConfig{Net: sysPackets(), GuildID: guildID, Port: port, Window: 1500 * time.Millisecond, Interval: 400 * time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	rctx, stop := clock.WithTimeout(ctx, clk, wait)
	defer stop()
	target, err := loc.Run(rctx)
	if err != nil || target.MachineID != "citadel" || target.Source != discovery.SourceBroadcast {
		t.Fatalf("Run = %+v, %v", target, err)
	}
	cli, err := protocol.NewClient(cfgT, protocol.TransportDialer(tc, loc.Target))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = cli.Run(ctx) }()
	deadline, cancelWait := clock.WithTimeout(ctx, clk, wait)
	defer cancelWait()
	for !cit.Link("tower1").Connected() {
		if err := clock.Sleep(deadline, clk, 20*time.Millisecond); err != nil {
			t.Fatalf("the Tower found %s and did not connect", target.Addr)
		}
	}
}

// CA-DESC-09, on Linux and on Windows: the instances of a machine share the
// discovery port and each one answers as a machine of its own.
func TestThreeInstancesOfOneMachineAnswerOnTheSamePort(t *testing.T) {
	t.Parallel()
	port := freeUDPPort(t)
	requireLocalBroadcast(t, port)
	startRealResponder(t, port, realInfo("citadel", discovery.RoleCitadel, 7443))
	startRealResponder(t, port, realInfo("tower-a", discovery.RoleTower, 7453))
	startRealResponder(t, port, realInfo("tower-b", discovery.RoleTower, 7463))

	// the new instance of the same machine asks
	seen, err := discovery.Search(t.Context(), discovery.SearchConfig{
		Net: sysPackets(), Clock: clock.Real{}, GuildID: guildID, Port: port, Window: 1500 * time.Millisecond, Interval: 400 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, s := range seen {
		ids[s.MachineID] = true
	}
	if len(ids) != 3 || !ids["citadel"] || !ids["tower-a"] || !ids["tower-b"] {
		t.Fatalf("answers = %+v, want three machines", seen)
	}
	cands := discovery.CandidatesFrom(seen, discovery.Known{})
	if len(cands) == 0 || cands[0].MachineID != "citadel" {
		t.Fatalf("candidates = %+v, want the Citadel first", cands)
	}
}

// The query of the binary against a real DNS-SD announcement. It needs
// multicast on the machine; without it the test says so and skips. The
// announcement of the Guardian itself (Avahi, DnsServiceRegister) is covered
// in the heavy suite, by the criterion CA-DESC-07.
func TestTheSystemBrowserHearsARealAnnouncement(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	resp, err := dnssd.NewResponder()
	if err != nil {
		t.Skipf("no mDNS responder can start here: %v", err)
	}
	a := discovery.Announcement{GuildID: guildID, MachineID: "citadel", Role: discovery.RoleCitadel, Epoch: 1, EnginePort: 7443, GuardianPort: 7444}
	txt := map[string]string{}
	for _, kv := range a.TXT() {
		for i := range kv {
			if kv[i] == '=' {
				txt[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	svc, err := dnssd.NewService(dnssd.Config{Name: "mestre-test", Type: discovery.ServiceEngine, Domain: "local", Port: 7443, Text: txt})
	if err != nil {
		t.Skipf("no service can be announced here: %v", err)
	}
	if _, err := resp.Add(svc); err != nil {
		t.Skipf("the service cannot be added: %v", err)
	}
	go func() { _ = resp.Respond(ctx) }()

	bctx, stop := clock.WithTimeout(ctx, clock.Real{}, 8*time.Second)
	defer stop()
	var found *discovery.Seen
	for i := 0; i < 3 && found == nil; i++ {
		seen, err := discovery.Search(bctx, discovery.SearchConfig{
			Browser: discovery.SystemBrowser{Suite: suite()}, Clock: clock.Real{}, GuildID: guildID, Window: 2500 * time.Millisecond,
		})
		if err != nil {
			break
		}
		for _, s := range seen {
			if s.MachineID == "citadel" {
				found = &s
			}
		}
	}
	if found == nil {
		t.Skip("multicast DNS is not delivered between sockets of this machine; the heavy suite covers the real announcement (CA-DESC-07)")
	}
	if found.Role != discovery.RoleCitadel || found.Port != 7443 || found.Epoch != 1 || found.Source != discovery.SourceMDNS {
		t.Fatalf("seen = %+v", found)
	}
}
