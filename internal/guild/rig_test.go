package guild_test

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	"github.com/lucasndas/mestre/internal/store/machine"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/internal/vault"
	"github.com/lucasndas/mestre/pkg/clock"
)

var day0 = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

// The two databases are migrated once, by TestMain, outside the time of every
// test (creating and migrating a file costs tens of milliseconds on a Windows
// runner); each node starts from a copy. The schema itself is tested in
// internal/store.
var guildTemplate, machineTemplate []byte

func TestMain(m *testing.M) {
	var err error
	if guildTemplate, machineTemplate, err = buildTemplates(); err != nil {
		fmt.Fprintln(os.Stderr, "guild: templates:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func buildTemplates() (g, m []byte, err error) {
	dir, err := os.MkdirTemp("", "guild-template")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)
	ctx := context.Background()
	opts := store.Options{Synchronous: "OFF"}
	gdb, err := guildtpl.Open(ctx, filepath.Join(dir, "mestre.db"), opts)
	if err != nil {
		return nil, nil, err
	}
	if err := gdb.Close(); err != nil {
		return nil, nil, err
	}
	mdb, err := machine.Open(ctx, filepath.Join(dir, "maquina.db"), opts)
	if err != nil {
		return nil, nil, err
	}
	if err := mdb.Close(); err != nil {
		return nil, nil, err
	}
	if g, err = os.ReadFile(filepath.Join(dir, "mestre.db")); err != nil {
		return nil, nil, err
	}
	m, err = os.ReadFile(filepath.Join(dir, "maquina.db"))
	return g, m, err
}

// memSecrets is the vault as the CA sees it (transport.SecretStore): the real
// vault is tested in internal/vault and in internal/transport.
type memSecrets struct {
	mu    sync.Mutex
	vals  map[string][]byte
	types map[string]vault.Type
	uses  []string
}

var _ transport.SecretStore = (*memSecrets)(nil)

func (m *memSecrets) Put(_ context.Context, name string, typ vault.Type, value []byte, _ *vault.SiteMeta) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.vals == nil {
		m.vals, m.types = map[string][]byte{}, map[string]vault.Type{}
	}
	m.vals[name], m.types[name] = slices.Clone(value), typ
	return nil
}

func (m *memSecrets) Use(_ context.Context, name, purpose string, fn func([]byte) error) error {
	m.mu.Lock()
	v, ok := m.vals[name]
	m.uses = append(m.uses, purpose)
	m.mu.Unlock()
	if !ok {
		return vault.ErrNotFound
	}
	return fn(slices.Clone(v))
}

func (m *memSecrets) names() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for n := range m.vals {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// memKeys is a vault.KeyStore in memory: the protection of the key file by the
// system is tested in internal/vault.
type memKeys struct {
	mu   sync.Mutex
	data []byte
}

func (m *memKeys) Load(context.Context) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		return nil, vault.ErrNoKey
	}
	return slices.Clone(m.data), nil
}

func (m *memKeys) Save(_ context.Context, d []byte) error {
	m.mu.Lock()
	m.data = slices.Clone(d)
	m.mu.Unlock()
	return nil
}

func (m *memKeys) has() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data != nil
}

// node is one machine of the tests: its machine database and the record of its
// place in the Guild.
type node struct {
	name   string
	mach   *store.DB
	keys   *memKeys
	member *guild.Member
}

var errBadPIN = errors.New("wrong PIN")

// fakeApprover is the Owner's channel: the bot and the panel of milestone 5.
// It records what is asked and what is withdrawn.
type fakeApprover struct {
	mu        sync.Mutex
	asked     chan guild.ApprovalRequest
	n         int
	withdrawn []string
	askErr    error
	// auto, when set, answers inside Ask, as an Owner who accepts at once.
	auto func(guild.ApprovalRequest)
}

func newApprover() *fakeApprover { return &fakeApprover{asked: make(chan guild.ApprovalRequest, 64)} }

func (a *fakeApprover) Ask(_ context.Context, r guild.ApprovalRequest) (string, error) {
	a.mu.Lock()
	if a.askErr != nil {
		err := a.askErr
		a.mu.Unlock()
		return "", err
	}
	a.n++
	id := fmt.Sprintf("approval-%d", a.n)
	auto := a.auto
	a.mu.Unlock()
	a.asked <- r
	if auto != nil {
		auto(r)
	}
	return id, nil
}

func (a *fakeApprover) Withdraw(_ context.Context, id string) {
	a.mu.Lock()
	a.withdrawn = append(a.withdrawn, id)
	a.mu.Unlock()
}

func (a *fakeApprover) withdrawals() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.withdrawn)
}

func (a *fakeApprover) questions() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.n
}

// owner is the bot: it checks the PIN of the Owner, and only then tells the
// Guild the answer (the Guild never sees a PIN).
type owner struct {
	c   *guild.Citadel
	pin string
}

func (o owner) answer(joinID, pin string, d guild.Decision) error {
	if pin != o.pin {
		return errBadPIN
	}
	d.ApprovedBy = "dono"
	return o.c.Decide(context.Background(), joinID, d)
}

// hooks records the events the later stages plug into.
type hooks struct {
	mu        sync.Mutex
	joined    []guild.Machine
	removed   []string // "id:reason"
	exclusion []guild.Machine
	alerts    []int
}

func (h *hooks) hooks() guild.Hooks {
	return guild.Hooks{
		Joined: func(m guild.Machine) { h.mu.Lock(); h.joined = append(h.joined, m); h.mu.Unlock() },
		Removed: func(m guild.Machine, why string) {
			h.mu.Lock()
			h.removed = append(h.removed, m.ID+":"+why)
			h.mu.Unlock()
		},
		ExclusionChanged: func(m guild.Machine) { h.mu.Lock(); h.exclusion = append(h.exclusion, m); h.mu.Unlock() },
		RefusalAlert:     func(n int) { h.mu.Lock(); h.alerts = append(h.alerts, n); h.mu.Unlock() },
	}
}

// lazyPeers lets a test plug the protocol in after the Citadel is built.
type lazyPeers struct {
	mu    sync.Mutex
	inner guild.Peers
}

func (p *lazyPeers) set(in guild.Peers) { p.mu.Lock(); p.inner = in; p.mu.Unlock() }

func (p *lazyPeers) get() guild.Peers {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inner
}

func (p *lazyPeers) Leave(ctx context.Context, id, reason string) error {
	if in := p.get(); in != nil {
		return in.Leave(ctx, id, reason)
	}
	return guild.ErrOffline
}

func (p *lazyPeers) CloseRevoked() int {
	if in := p.get(); in != nil {
		return in.CloseRevoked()
	}
	return 0
}

// rig is a Guild of one machine, the Citadel, on the fake network with its
// TLS server and the routes of the entry.
type rig struct {
	t        *testing.T
	clk      clock.Clock
	net      *fakenet.Network
	secrets  *memSecrets
	approver *fakeApprover
	hooks    *hooks
	peers    *lazyPeers
	owner    owner

	gdb     *store.DB
	founder *node
	founded *guild.Founded
	citadel *guild.Citadel
	cert    *transport.CertSource
	srv     *transport.Server
	towers  transport.Listener
	guards  transport.Listener
}

const citadelAddr = "citadel:7443"

// newRig founds a Guild on the machine "citadel" and serves its routes. The
// network runs on the real clock (net/http sets deadlines with it); clk drives
// the Guild: the fake clock of a test, or the clock of a synctest bubble.
func newRig(t *testing.T, clk clock.Clock, mods ...func(*guild.CitadelConfig)) *rig {
	t.Helper()
	ctx := context.Background()
	r := &rig{
		t: t, clk: clk, net: fakenet.New(clock.Real{}), secrets: &memSecrets{}, approver: newApprover(),
		hooks: &hooks{}, peers: &lazyPeers{},
	}
	r.net.SetIP("citadel", "192.168.1.10")
	r.founder = r.newNode("citadel")
	r.gdb = openDB(t, guildTemplate, "mestre.db", clk, guilddb.Open)
	f, err := guild.Create(ctx, guild.CreateConfig{
		GuildDB: r.gdb, Member: r.founder.member, Secrets: r.secrets, Clock: clk,
		Name: "servidor", Hostname: "servidor", IPs: []net.IP{net.ParseIP("192.168.1.10")}, Timezone: "America/Sao_Paulo",
		RecoveryPublicKey: []byte("recovery-public-key"),
	})
	if err != nil {
		t.Fatal(err)
	}
	r.founded = f
	r.cert = transport.NewCertSource(f.Motor, f.MotorKey)
	cfg := guild.CitadelConfig{
		DB: r.gdb, CA: f.CA, Clock: clk, Cert: r.cert, Approver: r.approver, Peers: r.peers, Hooks: r.hooks.hooks(),
	}
	for _, mod := range mods {
		mod(&cfg)
	}
	if r.citadel, err = guild.NewCitadel(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	r.owner = owner{c: r.citadel, pin: "1234"}
	r.srv, r.towers, r.guards = r.serve("citadel", citadelAddr, f.CA.Certificate(), r.cert)
	r.citadel.Mount(r.srv)
	return r
}

// serve starts a TLS server of the Guild at addr on host.
func (r *rig) serve(host, addr string, ca *x509.Certificate, cert *transport.CertSource) (*transport.Server, transport.Listener, transport.Listener) {
	r.t.Helper()
	s, err := transport.NewServer(transport.ServerConfig{CA: ca, Cert: cert, Credentials: r.credentials(), Clock: r.clk})
	if err != nil {
		r.t.Fatal(err)
	}
	towers := s.Route(transport.RouteTower, transport.KindMachine)
	guards := s.Route(transport.RouteGuardian, transport.KindGuardian)
	ln, err := r.net.Host(host).Listen(addr)
	if err != nil {
		r.t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve(ln) }()
	r.t.Cleanup(func() {
		_ = s.Close(transport.StatusGoingAway, "test over")
		if err := <-done; err != nil {
			r.t.Errorf("serve: %v", err)
		}
	})
	return s, towers, guards
}

func (r *rig) credentials() transport.CredentialChecker {
	if r.citadel == nil {
		return nil
	}
	return r.citadel.Credentials()
}

func openDB(t *testing.T, template []byte, file string, clk clock.Clock, open func(context.Context, string, store.Options) (*store.DB, error)) *store.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), file)
	if err := os.WriteFile(path, template, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := open(context.Background(), path, store.Options{Clock: clk, Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// newNode is a machine that is outside any Guild.
func (r *rig) newNode(name string) *node {
	r.t.Helper()
	return bareNode(r.t, r.clk, name)
}

func bareNode(t *testing.T, clk clock.Clock, name string) *node {
	t.Helper()
	n := &node{name: name, keys: &memKeys{}}
	n.mach = openDB(t, machineTemplate, "maquina.db", clk, machine.Open)
	n.member = guild.NewMember(n.mach, n.keys, clk)
	return n
}

// joining is a machine in the middle of its entry.
type joining struct {
	code chan string
	done chan joinResult
}

type joinResult struct {
	j   *guild.Joined
	err error
}

func (r *rig) joinConfig(n *node, addr string) (guild.JoinConfig, chan string) {
	code := make(chan string, 1)
	return guild.JoinConfig{
		Addr: addr, Network: r.net.Host(n.name), Clock: r.clk,
		Name: n.name, Hostname: n.name, OS: "linux", IPs: []net.IP{net.ParseIP("192.168.1.20")},
		OnCode: func(c string) { code <- c },
	}, code
}

// startJoin starts the entry of n at the Citadel of the rig.
func (r *rig) startJoin(n *node) *joining { return r.startJoinAt(n, citadelAddr) }

func (r *rig) startJoinAt(n *node, addr string) *joining {
	cfg, code := r.joinConfig(n, addr)
	j := &joining{code: code, done: make(chan joinResult, 1)}
	ctx := r.t.Context()
	go func() {
		got, err := guild.Enroll(ctx, n.member, cfg)
		j.done <- joinResult{got, err}
	}()
	return j
}

func (j *joining) result() (*guild.Joined, error) {
	r := <-j.done
	return r.j, r.err
}

// enroll puts n in the Guild with an Owner who accepts at once.
func (r *rig) enroll(n *node) *guild.Joined {
	r.t.Helper()
	r.approver.mu.Lock()
	r.approver.auto = func(req guild.ApprovalRequest) {
		if err := r.owner.answer(req.JoinID, "1234", guild.Decision{Accept: true}); err != nil {
			r.t.Errorf("auto accept: %v", err)
		}
	}
	r.approver.mu.Unlock()
	defer func() {
		r.approver.mu.Lock()
		r.approver.auto = nil
		r.approver.mu.Unlock()
	}()
	j, err := r.startJoin(n).result()
	if err != nil {
		r.t.Fatalf("enroll %s: %v", n.name, err)
	}
	return j
}

// count is the number of rows of a table of the Guild database.
func (r *rig) count(table string) int {
	r.t.Helper()
	var n int
	if err := r.gdb.Reader().QueryRowContext(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

// countWhere counts the rows of a table that match where.
func (r *rig) countWhere(table, where string, args ...any) int {
	r.t.Helper()
	var n int
	if err := r.gdb.Reader().QueryRowContext(context.Background(), `SELECT count(*) FROM `+table+` WHERE `+where, args...).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

// expectNothingIssued checks that the registry and the credentials are those
// of the founder alone.
func (r *rig) expectNothingIssued() {
	r.t.Helper()
	if m, c := r.count("machines"), r.count("machine_credentials"); m != 1 || c != 2 {
		r.t.Fatalf("registry has %d machines and %d credentials; want the founder's 1 and 2", m, c)
	}
}
