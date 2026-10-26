package replication_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mestre-ia/mestre/internal/gitx"
	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/replication"
	"github.com/mestre-ia/mestre/internal/store"
	guilddb "github.com/mestre-ia/mestre/internal/store/guild"
	"github.com/mestre-ia/mestre/internal/testkit/fakenet"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/internal/testkit/protorig"
	"github.com/mestre-ia/mestre/internal/vault"
	"github.com/mestre-ia/mestre/pkg/clock"
)

const (
	guildULID  = "01J9Z0000000000000000000G1"
	citadelID  = "citadel"
	citadelAdr = "citadel:7443"
)

// template is a Guild database already migrated and with its one row,
// built once: creating and migrating a file costs tens of milliseconds on a
// Windows runner.
var template []byte

func TestMain(m *testing.M) {
	if err := buildTemplate(); err != nil {
		fmt.Fprintln(os.Stderr, "replication template:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func buildTemplate() error {
	dir, err := os.MkdirTemp("", "replication-template")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "mestre.db")
	ctx := context.Background()
	db, err := guildtpl.Open(ctx, path, store.Options{StmtCache: guildtpl.StmtCache, Synchronous: "OFF"})
	if err != nil {
		return err
	}
	rk := bytes.Repeat([]byte{7}, 32)
	_, err = db.Writer().ExecContext(ctx, `INSERT INTO guild (id, guild_id, ca_cert, epoch, epoch_certificate, timezone, recovery_public_key, created_at)
		VALUES (1, ?, x'00', 1, '{}', 'UTC', ?, 0)`, guildULID, rk)
	if err != nil {
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	template, err = os.ReadFile(path)
	return err
}

// memKeys is the key store of a machine in memory; it can forget the key.
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
	defer m.mu.Unlock()
	m.data = slices.Clone(d)
	return nil
}

func (m *memKeys) Delete(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = nil
	return nil
}

type roster struct {
	mu       sync.Mutex
	excluded map[string]bool
}

func (r *roster) IsExcluded(_ context.Context, id string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.excluded[id], nil
}

func (r *roster) set(id string, v bool) {
	r.mu.Lock()
	r.excluded[id] = v
	r.mu.Unlock()
}

// citadelTap sits on the Citadel's side of every connection: it logs the
// frames of records and can lose the next one.
type citadelTap struct {
	protocol.Conn
	r *rig
}

func (c *citadelTap) Ping(ctx context.Context) error { return c.Conn.(protocol.Pinger).Ping(ctx) }

func (c *citadelTap) Write(ctx context.Context, kind protocol.FrameKind, data []byte) error {
	if kind == protocol.FrameBinary && len(data) >= protocol.DataHeaderSize && data[0] == byte(protocol.DataReplica) {
		pos := binary.BigEndian.Uint64(data[17:25])
		c.r.frameMu.Lock()
		c.r.frames = append(c.r.frames, frame{to: c.Conn.Peer().ID, pos: pos})
		c.r.frameMu.Unlock()
		if target, _ := c.r.dropTarget.Load().(string); target != "" && target == c.Conn.Peer().ID && c.r.dropTarget.CompareAndSwap(target, "") {
			return nil
		}
	}
	return c.Conn.Write(ctx, kind, data)
}

// towerTap sits on the Tower's side of its connection and can lose the next
// replica.resync it sends.
type towerTap struct {
	protocol.Conn
	r *rig
}

func (c *towerTap) Ping(ctx context.Context) error { return c.Conn.(protocol.Pinger).Ping(ctx) }

func (c *towerTap) Write(ctx context.Context, kind protocol.FrameKind, data []byte) error {
	if kind == protocol.FrameText && bytes.Contains(data, []byte(`"replica.resync"`)) {
		c.r.resyncs.Add(1)
		if n := c.r.dropResyncs.Load(); n > 0 && c.r.dropResyncs.CompareAndSwap(n, n-1) {
			return nil
		}
		c.r.resyncsDelivered.Add(1)
	}
	return c.Conn.Write(ctx, kind, data)
}

type tapListener struct {
	protocol.Listener
	r *rig
}

func (l *tapListener) Accept(ctx context.Context) (protocol.Conn, error) {
	c, err := l.Listener.Accept(ctx)
	if err != nil {
		return nil, err
	}
	return &citadelTap{Conn: c, r: l.r}, nil
}

type frame struct {
	to  string
	pos uint64
}

type rigOpts struct {
	vault bool
	// durable opens the database of the Citadel with flushes: a test that
	// rotates the key of the vault needs it (everything else does without, a
	// flush costs tens of milliseconds on a Windows runner).
	durable bool
	files   bool
	cites   replication.Cites
	// mirrors, when set, are the mirrors of the Citadel; git is the one both
	// sides use.
	mirrors      replication.MirrorSource
	git          gitx.Git
	citesMirrors func(store.Change) []string
	// lagThreshold, when set, is PrimaryConfig.LagThreshold: a test that lets
	// hours pass and is not about the lag sets it high, or the Citadel would
	// look at the lag every 5 s of them.
	lagThreshold time.Duration
	// busy, when set, is how long the replicas wait for a lock: a test that
	// keeps a reader on the WAL does not wait the default five seconds, three
	// times over, for each attempt to truncate it.
	busy time.Duration
	// paused, when set, is PrimaryConfig.Paused: a Citadel that does not act
	// yet prunes nothing.
	paused func() bool
}

// rig is a Citadel and the Towers that connect to it, on the fake network.
type rig struct {
	t    *testing.T
	clk  clock.Clock
	net  *fakenet.Network
	nw   protorig.Network
	dir  string
	opts rigOpts

	cdb     *store.DB
	primary *replication.Primary
	server  *protocol.Server
	roster  *roster
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	// the vault of the Citadel
	rk       *vault.RecoveryKey
	vault    *vault.Vault
	ckeys    *memKeys
	envelope string

	filesDir    string
	capturesDir string

	frameMu sync.Mutex
	frames  []frame
	// dropTarget is the Tower whose next frame of records is lost on the way.
	dropTarget atomic.Value
	// dropResyncs is how many of the next replica.resync of the Towers are lost
	// on the way; resyncs counts all that were sent (the protocol sends again
	// the ones not acknowledged), resyncsDelivered the ones that were not lost.
	dropResyncs, resyncs, resyncsDelivered atomic.Int32
	// ackEvery sets the interval of the confirmations of a Tower (default 1 s).
	ackEvery map[string]time.Duration

	dropMu  sync.Mutex
	dropped []string

	towers map[string]*tower
}

type tower struct {
	r        *rig
	id       string
	dbPath   string
	db       *store.DB
	rep      *replication.Replica
	client   *protocol.Client
	parts    protorig.Parts
	keys     *memKeys
	env      string
	files    string
	captures string
	mirrors  string
	xfer     string
	cancel   context.CancelFunc
	wg       sync.WaitGroup

	snapshots atomic.Int32
	// events is what the Tower saw, in order: "file <path>", "snapshot".
	evMu   sync.Mutex
	events []string
}

func (tw *tower) event(s string) {
	tw.evMu.Lock()
	tw.events = append(tw.events, s)
	tw.evMu.Unlock()
}

// newRig must be called inside a synctest bubble: the time is the bubble's.
func newRig(t *testing.T, o rigOpts) *rig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &rig{
		t: t, clk: clock.Real{}, dir: t.TempDir(), opts: o, cancel: cancel,
		roster: &roster{excluded: map[string]bool{}}, towers: map[string]*tower{}, ackEvery: map[string]time.Duration{},
	}
	r.dropTarget.Store("")
	r.net = fakenet.New(r.clk)
	r.nw = protorig.Network{Net: r.net}
	path := filepath.Join(r.dir, "citadel.db")
	if err := os.WriteFile(path, template, 0o600); err != nil {
		t.Fatal(err)
	}
	// The rows of the template are stamped with the real time; the test lives in the bubble's.
	// Nothing but the store writes to the log, so this goes around the store.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `UPDATE replication_log SET at = ?`, r.clk.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	sync := "OFF"
	if o.durable {
		sync = ""
	}
	r.cdb, err = guildtpl.Open(ctx, path, store.Options{Synchronous: sync, Clock: r.clk})
	if err != nil {
		t.Fatal(err)
	}
	pc := replication.PrimaryConfig{
		DB: r.cdb, Clock: r.clk, Roster: r.roster, Dir: filepath.Join(r.dir, "snapshots"),
		LagThreshold: o.lagThreshold,
		Paused:       o.paused,
		OnDropped:    func(id string) { r.dropMu.Lock(); r.dropped = append(r.dropped, id); r.dropMu.Unlock() },
		GuildConfig: func(context.Context) (protocol.GuildConfig, error) {
			return protocol.GuildConfig{Timezone: "UTC"}, nil
		},
	}
	if o.vault {
		r.rk, err = vault.NewRecoveryKey()
		if err != nil {
			t.Fatal(err)
		}
		r.ckeys = &memKeys{}
		r.envelope = filepath.Join(r.dir, "cofre-envelope.bin")
		r.vault, err = vault.Create(ctx, vault.Config{
			DB: r.cdb, Keys: r.ckeys, EnvelopePath: r.envelope, RecoveryPublicKey: r.rk.PublicKey(), Masker: logging.NewMasker(), Clock: r.clk,
		})
		if err != nil {
			t.Fatal(err)
		}
		pc.Vault = &vault.Copier{Keys: r.ckeys, EnvelopePath: r.envelope}
	}
	if o.files {
		r.filesDir = filepath.Join(r.dir, "citadel-files")
		r.capturesDir = filepath.Join(r.dir, "citadel-captures")
		pc.Files, err = replication.NewFiles(
			replication.FileRoot{Name: "classes", Dir: r.filesDir, Walk: true},
			replication.FileRoot{Name: "captures", Dir: r.capturesDir, Walk: true, NotForExcluded: true})
		if err != nil {
			t.Fatal(err)
		}
		pc.Cites = o.cites
	}
	if o.mirrors != nil {
		pc.Mirrors = replication.NewMirrors(o.mirrors, filepath.Join(r.dir, "bundles"))
		pc.CitesMirrors = o.citesMirrors
		if err := os.MkdirAll(filepath.Join(r.dir, "bundles"), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	r.primary, err = replication.NewPrimary(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := protorig.Config(r.clk, citadelID, 1, citadelID)
	cfg.Router = protocol.NewRouter()
	r.primary.Register(cfg.Router)
	cfg.Hooks = r.primary.Hooks(protocol.Hooks{})
	r.server, err = protocol.NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r.primary.Attach(r.server)
	lis, err := r.nw.Listen(citadelAdr, protocol.Peer{Role: protocol.RoleMachine, ID: citadelID})
	if err != nil {
		t.Fatal(err)
	}
	lis = &tapListener{Listener: lis, r: r}
	r.wg.Go(func() { _ = r.server.Serve(ctx, lis) })
	r.wg.Go(func() { _ = r.primary.Run(ctx) })
	t.Cleanup(r.stop)
	return r
}

func (r *rig) stop() {
	for _, tw := range r.towers {
		tw.stop()
	}
	r.cancel()
	r.wg.Wait()
	_ = r.cdb.Close()
}

// addTower creates a Tower and starts it.
func (r *rig) addTower(id string, startReplica bool) *tower {
	r.t.Helper()
	dir := filepath.Join(r.dir, id)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		r.t.Fatal(err)
	}
	tw := &tower{
		r: r, id: id, dbPath: filepath.Join(dir, "mestre.db"), keys: &memKeys{}, env: filepath.Join(dir, "cofre-envelope.bin"),
		files: filepath.Join(dir, "files"), captures: filepath.Join(dir, "captures"), xfer: filepath.Join(dir, "xfer"), mirrors: filepath.Join(dir, "mirrors"),
	}
	_, tw.parts = protorig.Config(r.clk, id, 1, citadelID)
	r.towers[id] = tw
	tw.start(startReplica)
	return tw
}

func (tw *tower) start(startReplica bool) {
	r := tw.r
	t := r.t
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	tw.cancel = cancel
	var err error
	tw.db, err = guilddb.Open(ctx, tw.dbPath, store.Options{StmtCache: guildtpl.StmtCache, Replica: true, Synchronous: "OFF", Clock: r.clk, BusyTimeout: r.opts.busy})
	if err != nil {
		t.Fatal(err)
	}
	rc := replication.ReplicaConfig{DB: tw.db, Clock: r.clk, GuildID: guildULID, AckEvery: r.ackEvery[tw.id], TransferDir: tw.xfer,
		OnApplied: func(first, last uint64) { tw.event(fmt.Sprintf("applied %d-%d", first, last)) }}
	if r.opts.vault {
		rc.Vault = &vault.Copier{Keys: tw.keys, EnvelopePath: tw.env}
	}
	if r.opts.files {
		rc.Files, err = replication.NewFiles(
			replication.FileRoot{Name: "classes", Dir: tw.files},
			replication.FileRoot{Name: "captures", Dir: tw.captures, NotForExcluded: true})
		if err != nil {
			t.Fatal(err)
		}
	}
	if r.opts.mirrors != nil {
		rc.Mirrors = &replication.MirrorStore{Root: tw.mirrors, Git: r.opts.git}
	}
	tw.rep, err = replication.NewReplica(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := protorig.Config(r.clk, tw.id, 1, citadelID)
	cfg.Outbox, cfg.Processed = tw.parts.Store, tw.parts.Store
	cfg.Epoch, cfg.Certs = tw.parts.Epochs, tw.parts.Epochs
	cfg.Router = protocol.NewRouter()
	tw.rep.Register(cfg.Router)
	cfg.Hello = func(ctx context.Context) (protocol.HelloInfo, error) {
		info := protocol.HelloInfo{Version: "0.0.0-test", Sheet: protocol.Object{}}
		tw.rep.Hello(ctx, &info)
		return info, nil
	}
	fins := tw.rep.Finalizers()
	snap := fins[protocol.KindDBSnapshot]
	fins[protocol.KindDBSnapshot] = protocol.FinalizerFunc(func(ctx context.Context, b protocol.XferBegin, file string) error {
		err := snap.Finalize(ctx, b, file)
		if err == nil {
			tw.snapshots.Add(1)
			tw.event("snapshot")
		}
		return err
	})
	if file, ok := fins[protocol.KindFile]; ok {
		fins[protocol.KindFile] = &eventFile{Finalizer: file, tw: tw}
	}
	if bundle, ok := fins[protocol.KindGitBundle]; ok {
		fins[protocol.KindGitBundle] = &eventBundle{Finalizer: bundle, tw: tw}
	}
	cfg.Xfer = &protocol.XferConfig{Dir: tw.xfer, Finalizers: fins}
	dial := r.nw.Dialer(tw.id, citadelAdr, protocol.Peer{Role: protocol.RoleMachine, ID: tw.id})
	tw.client, err = protocol.NewClient(cfg, protocol.DialerFunc(func(ctx context.Context) (protocol.Conn, error) {
		c, err := dial.Dial(ctx)
		if err != nil {
			return nil, err
		}
		return &towerTap{Conn: c, r: r}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	tw.rep.Attach(tw.client.Link())
	tw.wg.Go(func() { _ = tw.client.Run(ctx) })
	if startReplica {
		tw.wg.Go(func() { _ = tw.rep.Run(ctx) })
	}
}

// eventFile logs the files that arrive.
type eventFile struct {
	protocol.Finalizer
	tw *tower
}

func (e *eventFile) Have(ctx context.Context, b protocol.XferBegin) bool {
	return e.Finalizer.(protocol.Skipper).Have(ctx, b)
}

func (e *eventFile) Finalize(ctx context.Context, b protocol.XferBegin, file string) error {
	err := e.Finalizer.Finalize(ctx, b, file)
	if err == nil {
		raw := b.Meta["path"]
		e.tw.event("file " + strings.Trim(string(raw), `"`))
	}
	return err
}

// eventBundle logs the bundles that arrive.
type eventBundle struct {
	protocol.Finalizer
	tw *tower
}

func (e *eventBundle) Have(ctx context.Context, b protocol.XferBegin) bool {
	return e.Finalizer.(protocol.Skipper).Have(ctx, b)
}

func (e *eventBundle) Finalize(ctx context.Context, b protocol.XferBegin, file string) error {
	err := e.Finalizer.Finalize(ctx, b, file)
	if err == nil {
		e.tw.event("bundle")
	}
	return err
}

func (tw *tower) stop() {
	if tw.cancel == nil {
		return
	}
	tw.cancel()
	tw.wg.Wait()
	_ = tw.db.Close()
	tw.cancel = nil
}

// settle lets everything that can happen without time passing happen.
func settle() { synctest.Wait() }

// eventually waits for cond. A pause for the file system makes SQLite sleep (on
// Windows its retry of a locked file is a time.Sleep, which in the bubble is a
// sleep of the fake clock), and then the bubble looks idle with the work not done:
// settle alone is not a promise there. So between tries the clock moves a little.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	eventuallyFor(t, what, 2*time.Millisecond, 600, cond)
}

// eventuallyFor is eventually with the step of the clock and the number of tries.
func eventuallyFor(t *testing.T, what string, step time.Duration, tries int, cond func() bool) {
	t.Helper()
	for range tries {
		settle()
		if cond() {
			return
		}
		advance(step)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// advance moves the bubble's clock by d and settles.
func advance(d time.Duration) {
	_ = clock.Sleep(context.Background(), clock.Real{}, d)
	synctest.Wait()
}

// ---- writing and comparing ----

func (r *rig) exec(q string, args ...any) {
	r.t.Helper()
	if _, err := r.cdb.Writer().ExecContext(context.Background(), q, args...); err != nil {
		r.t.Fatalf("%s: %v", q, err)
	}
}

func (r *rig) insertMachine(i int) {
	r.t.Helper()
	r.exec(`INSERT INTO machines (machine_id, name, instance, priority, excluded, state, names, joined_at) VALUES (?, ?, '', 0, 0, 'active', '{}', ?)`,
		fmt.Sprintf("m%04d", i), fmt.Sprintf("machine %d", i), int64(i))
}

// dump is every table of the database but the log, as text.
func dump(t *testing.T, db *store.DB, skip ...string) string {
	t.Helper()
	ctx := context.Background()
	rows, err := db.Reader().QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name <> 'replication_log' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(skip, n) {
			tables = append(tables, n)
		}
	}
	rows.Close()
	var b strings.Builder
	for _, tb := range tables {
		rs, err := db.Reader().QueryContext(ctx, `SELECT * FROM "`+tb+`" ORDER BY 1`)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rs.Columns()
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		for rs.Next() {
			if err := rs.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "%s:", tb)
			for _, v := range vals {
				fmt.Fprintf(&b, "%T=%v|", v, v)
			}
			b.WriteByte('\n')
		}
		rs.Close()
	}
	return b.String()
}

func (r *rig) lsn() uint64 {
	r.t.Helper()
	n, err := r.cdb.LastLSN(context.Background())
	if err != nil {
		r.t.Fatal(err)
	}
	return n
}

// lsn is the last record the Tower applied (0 before it has a replica).
func (tw *tower) lsn() uint64 {
	n, err := tw.db.LastLSN(context.Background())
	if err != nil {
		return 0
	}
	return n
}

func (r *rig) framesTo(id string) []uint64 {
	r.frameMu.Lock()
	defer r.frameMu.Unlock()
	var out []uint64
	for _, f := range r.frames {
		if f.to == id {
			out = append(out, f.pos)
		}
	}
	return out
}

// receipts are the machines that gave a receipt of vault.drop, in order.
func (r *rig) receipts() []string {
	r.dropMu.Lock()
	defer r.dropMu.Unlock()
	return slices.Clone(r.dropped)
}
