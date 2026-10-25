//go:build full

package guild_test

import (
	"context"
	"crypto/tls"
	"errors"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/app"
	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/discovery"
	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/privdir"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/store/machine"
	"github.com/lucasndas/mestre/internal/telegram"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/internal/testkit/faketelegram"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/internal/testkit/protorig"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/internal/users"
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

// CA-GUILDA-09, on Linux and on Windows: a second instance on the same machine
// finds the Guild by broadcast and enters it, with the Owner asked as in
// production: a request entrada_na_guilda in the chain of approval, the flow of
// approval over the real bot of the Telegram (the fake one), the Owner clicking
// the button and giving the PIN in the chat. It then connects by mTLS only, and
// connects again after a restart.
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
	ow := newTelegramOwner(t, ctx, a.guildDB, clk, func() *guild.Citadel { return citadel })
	citadel, err = guild.NewCitadel(ctx, guild.CitadelConfig{
		DB: a.guildDB, CA: founded.CA, Clock: clk, Cert: src, Approver: ow.approver, Credentials: creds,
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
	go ow.answerEntry(t, ctx) // the Owner of the Telegram: the button and the PIN

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
	if q := <-ow.asked; q.Code != <-codes {
		t.Errorf("the Owner saw %q on the question and the installer showed another code", q.Code)
	}
	// who let it in is the Owner, by the PIN, through a request of the chain
	var approved int
	if err := a.guildDB.Reader().QueryRow(`SELECT count(*) FROM join_requests WHERE state = 'approved' AND decided_by = ?`, ow.ownerID).Scan(&approved); err != nil || approved != 1 {
		t.Errorf("%d entries approved by the Owner (%v), want 1", approved, err)
	}
	var viaChain int
	if err := a.guildDB.Reader().QueryRow(`SELECT count(*) FROM approval_requests WHERE type = ? AND state = 'approved'`, string(classes.RequestGuildEntry)).Scan(&viaChain); err != nil || viaChain != 1 {
		t.Errorf("%d requests entrada_na_guilda approved in the chain (%v), want 1", viaChain, err)
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

	// "by mTLS only": the route of the Towers refuses a client that trusts the CA but
	// has no client certificate, and a client that does not speak TLS at all
	towerURL := net.JoinHostPort("127.0.0.1", strconv.Itoa(tcpPort)) + transport.RouteTower
	noCert := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: founded.CA.Pool(), MinVersion: tls.VersionTLS13}}}
	defer noCert.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+towerURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := noCert.Do(req); err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("the Tower route without a client certificate = %d, want 401", resp.StatusCode)
		}
	}
	plainReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+towerURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := (&http.Client{}).Do(plainReq); err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode < 400 {
			t.Errorf("the Tower route over plain HTTP = %d, want a refusal", resp.StatusCode)
		}
	}

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

// telegramOwner is the Owner of the Guild answering through the production path:
// the real approver of the engine, the chain of approval, the flow with the PIN and
// the real bot, over the fake Telegram on the real clock.
type telegramOwner struct {
	approver guild.Approver
	asked    chan guild.ApprovalRequest
	fake     *faketelegram.Server
	user     faketelegram.User
	ownerID  string
}

const ownerPIN = "123456"

type noAlerts struct{}

func (noAlerts) Unauthorized(context.Context, int64, string) {}
func (noAlerts) OwnerAlert(context.Context, string)          {}
func (noAlerts) Tell(context.Context, string, string)        {}

type noRouter struct{}

func (noRouter) Deliver(context.Context, telegram.Inbound) error { return nil }

func newTelegramOwner(t *testing.T, ctx context.Context, db *store.DB, clk clock.Clock, citadel func() *guild.Citadel) *telegramOwner {
	t.Helper()
	svc, err := users.New(users.Options{DB: db, Clock: clk, Alerts: noAlerts{}, PIN: users.PINCost{MemoryKiB: 8, Time: 1, Threads: 1}})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := svc.CreateOwner(ctx, "Lucas")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetPIN(ctx, owner.ID, ownerPIN); err != nil {
		t.Fatal(err)
	}
	code, err := svc.NewCode(ctx, owner, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	o := &telegramOwner{ownerID: owner.ID, asked: make(chan guild.ApprovalRequest, 8), user: faketelegram.User{ID: 77, FirstName: "Lucas", Username: "lucas"}}
	if _, err := svc.Pair(ctx, users.PairRequest{Text: code.Code, TelegramID: o.user.ID, Private: true}); err != nil {
		t.Fatal(err)
	}
	const token = "123456:fake-bot-token"
	if o.fake, err = faketelegram.New(clk, t, token); err != nil {
		t.Fatal(err)
	}
	ac := &telegram.ApprovalChannel{Users: svc}
	bot, err := telegram.New(telegram.Options{
		DB: db, Clock: clk, Users: svc, Alerts: noAlerts{}, Router: noRouter{}, Actions: ac.Actions(), Captures: ac.Captures(),
		Client: &telegram.Client{Token: token, HTTP: &http.Client{Transport: fakenet.HandlerTransport(o.fake)}}, PollTimeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	var svcChain *chain.Service
	flow, err := approvals.New(approvals.Options{DB: db, Clock: clk, Chain: lateFlowChain{&svcChain}, Users: svc, PINs: svc, Messenger: ac,
		Locked: func(context.Context) bool { return false }})
	if err != nil {
		t.Fatal(err)
	}
	ac.Bind(bot, flow)
	entry := app.EntryApprover{Chain: func() *chain.Service { return svcChain },
		Decide: func(ctx context.Context, id string, d guild.Decision) error { return citadel().Decide(ctx, id, d) },
		Tell:   func(context.Context, string) {}}
	if svcChain, err = chain.NewService(chain.Options{Store: &chain.Store{DB: db}, Clock: clk, Characters: noCharacters{}, Inbox: noInbox{}, Classes: noClasses{},
		Policy: chain.StaticPolicy{}, Users: flow, Sinks: map[classes.RequestType]chain.Sink{classes.RequestGuildEntry: entry}}); err != nil {
		t.Fatal(err)
	}
	o.approver = watched{Approver: entry, asked: o.asked}
	go func() { _ = bot.Run(ctx) }()
	return o
}

// answerEntry is the Owner on the phone: it waits for the question with the button
// in the chat, taps "Aprovar agora", waits for the bot to ask the PIN and types it.
func (o *telegramOwner) answerEntry(t *testing.T, ctx context.Context) {
	clk := clock.Real{}
	until := func(what string, ok func() bool) bool {
		dl, stop := clock.WithTimeout(ctx, clk, wait)
		defer stop()
		for !ok() {
			if err := clock.Sleep(dl, clk, 20*time.Millisecond); err != nil {
				if ctx.Err() == nil {
					t.Errorf("the Owner waited for %s in vain", what)
				}
				return false
			}
		}
		return true
	}
	var msg int64
	var key string
	if !until("the question about the entry", func() bool {
		for _, s := range o.fake.Sent() {
			kb, ok := s.Params["reply_markup"].(map[string]any)
			if s.Method != "sendMessage" || s.ChatID != o.user.ID || !ok || kb["inline_keyboard"] == nil {
				continue
			}
			for _, row := range kb["inline_keyboard"].([]any) {
				for _, b := range row.([]any) {
					if bt := b.(map[string]any); bt["text"] == "Aprovar agora" {
						msg, key = s.MessageID, bt["callback_data"].(string)
					}
				}
			}
		}
		return key != ""
	}) {
		return
	}
	asked := func() int {
		n := 0
		for _, s := range o.fake.Sent() {
			if s.Method == "sendMessage" && s.ChatID == o.user.ID && strings.Contains(s.Text, "PIN") {
				n++
			}
		}
		return n
	}
	before := asked()
	if _, err := o.fake.UserClicks(o.user, msg, key); err != nil {
		t.Errorf("the button: %v", err)
		return
	}
	if !until("the bot to ask the PIN", func() bool { return asked() > before }) {
		return
	}
	if _, err := o.fake.UserSendsText(o.user, faketelegram.Private(o.user), ownerPIN); err != nil {
		t.Errorf("the PIN: %v", err)
	}
}

// member2 is the record again, for the helpers that take a node.
func (i *instance) member2() *node { return &node{member: i.member} }
