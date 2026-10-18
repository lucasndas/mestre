//go:build full

package guild_test

import (
	"context"
	"errors"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/discovery"
	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/privdir"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/store/machine"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/internal/testkit/protorig"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/internal/vault"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The full suite of the Guild runs two machines of the same computer as two
// processes would: real TCP on the loopback with the mTLS of the transport,
// the broadcast of the discovery on real UDP, the databases in files, the key
// of the engine in the file the system protects. A runner that does not hand
// its own broadcast back to a local socket cannot run the discovery part; it
// says so and skips, and the same criterion runs on the lab machines in the
// heavy suite (CA-GUILDA-10).

const wait = 30 * time.Second

func suite() string { return os.Getenv("MESTRE_TEST_SUITE") }

func sysPackets() discovery.SystemPacketNetwork { return discovery.SystemPacketNetwork{Suite: suite()} }

// freeUDPPort picks a UDP port in the private range that nobody listens on, so
// the test does not meet a Mestre that runs on the machine.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	for range 50 {
		port := 20000 + rand.IntN(30000)
		c, err := sysPackets().ListenShared(port)
		if err != nil {
			continue
		}
		c.Close()
		return port
	}
	t.Fatal("no free UDP port in the private range")
	return 0
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

// instance is one machine of the test with its files.
type instance struct {
	dir     string
	mach    *store.DB
	keys    *vault.FileKeyStore
	member  *guild.Member
	guildDB *store.DB
}

func newInstance(t *testing.T, name string) *instance {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := privdir.Ensure(dir); err != nil { // the key file wants a folder of its own
		t.Fatal(err)
	}
	i := &instance{dir: dir, keys: vault.NewFileKeyStore(filepath.Join(dir, "motor.key"))}
	opts := store.Options{Synchronous: "OFF"}
	var err error
	if i.mach, err = machine.Open(t.Context(), filepath.Join(dir, "maquina.db"), opts); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { i.mach.Close() })
	i.member = guild.NewMember(i.mach, i.keys, clock.Real{})
	return i
}

// restart closes the machine database and opens it again, as a process that
// was stopped and started.
func (i *instance) restart(t *testing.T) {
	t.Helper()
	if err := i.mach.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	if i.mach, err = machine.Open(t.Context(), filepath.Join(i.dir, "maquina.db"), store.Options{Synchronous: "OFF"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { i.mach.Close() })
	i.member = guild.NewMember(i.mach, i.keys, clock.Real{})
}

// Part of CA-GUILDA-09 (it closes in M5, with the approval by the Telegram), on Linux and on Windows: a second instance on the same
// machine finds the Guild by broadcast, enters it (the Owner's answer is the
// double of the bot), connects by mTLS only, and connects again after a
// restart. The approval through the real bot is milestone 5.
func TestASecondInstanceFindsTheGuildByBroadcastEntersConnectsByMTLSAndReconnectsAfterARestart(t *testing.T) {
	t.Parallel()
	port := freeUDPPort(t)
	requireLocalBroadcast(t, port)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	clk := clock.Real{}
	sys := transport.SystemNetwork{Suite: suite()}

	// instance A creates the Guild and is the Citadel
	a := newInstance(t, "a")
	a.guildDB = func() *store.DB {
		db, err := guildtpl.Open(ctx, filepath.Join(a.dir, "mestre.db"), store.Options{Synchronous: "OFF"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return db
	}()
	secrets := &memSecrets{}
	founded, err := guild.Create(ctx, guild.CreateConfig{
		GuildDB: a.guildDB, Member: a.member, Secrets: secrets, Clock: clk,
		Name: "a", Hostname: "localhost", IPs: []net.IP{net.ParseIP("127.0.0.1")}, Timezone: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	approver := newApprover()
	creds, err := guild.LoadCredentials(ctx, a.guildDB)
	if err != nil {
		t.Fatal(err)
	}
	src := transport.NewCertSource(founded.Motor, founded.MotorKey)
	tlsSrv, err := transport.NewServer(transport.ServerConfig{CA: founded.CA.Certificate(), Cert: src, Credentials: creds, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	towers := tlsSrv.Route(transport.RouteTower, transport.KindMachine)
	lnA, err := sys.Listen("0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	tcpPort := lnA.Addr().(*net.TCPAddr).Port
	served := make(chan struct{})
	go func() { _ = tlsSrv.Serve(lnA); close(served) }()
	defer func() { _ = tlsSrv.Close(transport.StatusGoingAway, "test over"); <-served }()

	epochsA := loadEpochs(t, a.member2(), guild.EpochsConfig{})
	router := protocol.NewRouter()
	cfgA, _ := protorig.Config(clk, founded.MachineID, 1, founded.MachineID)
	sqlA := protocol.NewSQLStore(a.guildDB)
	cfgA.Epoch, cfgA.Certs, cfgA.Router, cfgA.Outbox, cfgA.Processed = epochsA, epochsA, router, sqlA, sqlA
	protoSrv, err := protocol.NewServer(cfgA)
	if err != nil {
		t.Fatal(err)
	}
	var citadel *guild.Citadel
	citadel, err = guild.NewCitadel(ctx, guild.CitadelConfig{
		DB: a.guildDB, CA: founded.CA, Clock: clk, Cert: src, Approver: approver, Credentials: creds,
		Peers: guild.ProtocolPeers{Server: protoSrv, Transport: tlsSrv, Clock: clk},
	})
	if err != nil {
		t.Fatal(err)
	}
	citadel.Mount(tlsSrv)
	guild.Register(router, guild.Wiring{Citadel: func() *guild.Citadel { return citadel }})
	go func() { _ = protoSrv.Serve(ctx, protocol.FromTransportListener(towers)) }()
	responder, err := discovery.StartResponder(ctx, discovery.ResponderConfig{
		Net: sysPackets(), Port: port, Clock: clk,
		Info: func() (discovery.Info, bool) {
			return discovery.Info{
				GuildID: founded.GuildID, MachineID: founded.MachineID, Name: "a", Role: discovery.RoleCitadel, Epoch: 1, Port: tcpPort,
				Citadel:  &discovery.CitadelRef{MachineID: founded.MachineID, Addrs: []string{"127.0.0.1"}, Port: tcpPort},
				Machines: 1, CAFp: "sha256:test",
			}, true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer responder.Close()
	approver.mu.Lock()
	approver.auto = func(req guild.ApprovalRequest) {
		if err := (owner{c: citadel, pin: "1234"}).answer(req.JoinID, "1234", guild.Decision{Accept: true}); err != nil {
			t.Errorf("accept: %v", err)
		}
	}
	approver.mu.Unlock()

	// instance B is installed: nothing configured, nothing cached
	b := newInstance(t, "b")
	codes := make(chan string, 1)
	res, err := guild.Install(ctx, guild.InstallConfig{
		Plan: guild.PlanInput{Search: discovery.SearchConfig{Net: sysPackets(), Clock: clk, Port: port, Window: 1500 * time.Millisecond, Interval: 400 * time.Millisecond}},
		Join: guild.JoinConfig{
			Network: sys, Clock: clk, Name: "b", Hostname: "localhost", OS: "linux", IPs: []net.IP{net.ParseIP("127.0.0.1")},
			OnCode: func(c string) { codes <- c },
		},
		Member: b.member,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Plan.Action != guild.PlanJoin || res.Joined == nil || res.Plan.Join.GuildID != founded.GuildID {
		t.Fatalf("install = %+v", res)
	}
	if q := <-approver.asked; q.Code != <-codes {
		t.Errorf("the Owner saw %q on the question and the installer showed another code", q.Code)
	}

	// B connects as a Tower, by mTLS and nothing else, and again after a restart
	connect := func(t *testing.T) (*protocol.Client, context.CancelFunc) {
		t.Helper()
		id, bsrc, err := b.member.TLS(ctx)
		if err != nil {
			t.Fatal(err)
		}
		epochs := loadEpochs(t, b.member2(), guild.EpochsConfig{})
		tc, err := transport.NewClient(transport.ClientConfig{CA: id.CA, Cert: bsrc, Network: sys, Clock: clk, Credentials: guild.NewCredentials()})
		if err != nil {
			t.Fatal(err)
		}
		cfg, _ := protorig.Config(clk, id.MachineID, epochs.Epoch(), founded.MachineID)
		cfg.Epoch, cfg.Certs = epochs, epochs
		cli, err := protocol.NewClient(cfg, protocol.TransportDialer(tc, func() (string, string) {
			return net.JoinHostPort("127.0.0.1", strconv.Itoa(tcpPort)), transport.RouteTower
		}))
		if err != nil {
			t.Fatal(err)
		}
		cctx, stop := context.WithCancel(ctx)
		var wg sync.WaitGroup
		wg.Go(func() { _ = cli.Run(cctx) })
		return cli, func() { stop(); wg.Wait() }
	}
	waitConnected := func(t *testing.T, id string) {
		t.Helper()
		dl, stop := clock.WithTimeout(ctx, clk, wait)
		defer stop()
		for !protoSrv.Link(id).Connected() {
			if err := clock.Sleep(dl, clk, 20*time.Millisecond); err != nil {
				t.Fatalf("%s did not connect", id)
			}
		}
	}
	_, stop := connect(t)
	waitConnected(t, res.Joined.MachineID)
	stop()

	b.restart(t)
	_, stop2 := connect(t)
	defer stop2()
	waitConnected(t, res.Joined.MachineID)
	if id, err := b.member.Identity(ctx); err != nil || id.MachineID != res.Joined.MachineID {
		t.Fatalf("identity after the restart: %+v, %v", id, err)
	}

	// the Owner removes it: the connection falls with 4002 and it cannot return
	if _, err := citadel.Remove(ctx, res.Joined.MachineID, "dono"); err != nil {
		t.Fatal(err)
	}
	dl, stopWait := clock.WithTimeout(ctx, clk, wait)
	defer stopWait()
	for protoSrv.Link(res.Joined.MachineID).Connected() {
		if err := clock.Sleep(dl, clk, 20*time.Millisecond); err != nil {
			t.Fatal("the removed machine is still connected")
		}
	}
	_, bsrc, err := b.member.TLS(ctx)
	if err != nil && !errors.Is(err, guild.ErrNotMember) {
		t.Fatal(err)
	}
	if err == nil {
		tc, err := transport.NewClient(transport.ClientConfig{CA: founded.CA.Certificate(), Cert: bsrc, Network: sys, Clock: clk})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tc.Dial(ctx, net.JoinHostPort("127.0.0.1", strconv.Itoa(tcpPort)), transport.RouteTower); err == nil {
			t.Error("a removed machine connected over real sockets")
		}
	}
}

// member2 is the record again, for the helpers that take a node.
func (i *instance) member2() *node { return &node{member: i.member} }
