package scrolls_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/connections"
	"github.com/lucasndas/mestre/internal/orchestration/scrolls"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/pkg/clock"
)

var day0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

// The Guild database is migrated once, by TestMain; every test starts from a
// copy (docs/qualidade/testes.md).
var guildTemplate []byte

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "scrolls-template")
	if err != nil {
		fmt.Fprintln(os.Stderr, "scrolls:", err)
		os.Exit(1)
	}
	path := filepath.Join(dir, "mestre.db")
	db, err := guildtpl.Open(context.Background(), path, store.Options{Synchronous: "OFF"})
	if err == nil {
		err = db.Close()
	}
	if err == nil {
		guildTemplate, err = os.ReadFile(path)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "scrolls: template:", err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

const (
	workspace = "meu-app"
	table     = "42-login"
)

// linker is the connections, which the scrolls need to exist before them (the
// connections ask the scrolls what is in a tome).
type linker struct {
	mu sync.Mutex
	c  *connections.Service
}

func (l *linker) get() *connections.Service {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.c
}

func (l *linker) Link(ctx context.Context, ws, tb string, a, b connections.Ref, by string) error {
	return l.get().Link(ctx, ws, tb, a, b, by)
}

func (l *linker) Forget(ctx context.Context, ref connections.Ref) error {
	return l.get().Forget(ctx, ref)
}

func (l *linker) CanSeeTome(ctx context.Context, agent, tome string) (bool, error) {
	return l.get().CanSeeTome(ctx, agent, tome)
}

func (l *linker) CanSeeScroll(ctx context.Context, agent, scroll string) (bool, error) {
	return l.get().CanSeeScroll(ctx, agent, scroll)
}

type fakeTables struct {
	root string
}

func (f fakeTables) Table(_ context.Context, ws, id string) (characters.TableInfo, error) {
	if id != table && id != "43-api" {
		return characters.TableInfo{}, fmt.Errorf("no table %s in %s", id, ws)
	}
	return characters.TableInfo{ID: id, Workspace: ws, State: characters.TableWorking}, nil
}

func (f fakeTables) Worktree(_ context.Context, _, tb, machine string) (string, error) {
	return filepath.Join(f.root, machine, tb), nil
}

// world is a workspace with a table, its characters and the scrolls.
type world struct {
	t       *testing.T
	ctx     context.Context
	clk     *clock.Fake
	db      *store.DB
	dbPath  string
	reg     characters.Registry
	conns   *connections.Service
	svc     *scrolls.Service
	root    string
	changes *changeLog
	n       int
}

type changeLog struct {
	mu sync.Mutex
	cs []scrolls.Change
}

func (c *changeLog) add(ch scrolls.Change) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cs = append(c.cs, ch)
}

func (c *changeLog) all() []scrolls.Change {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]scrolls.Change(nil), c.cs...)
}

type worldOptions struct {
	copies scrolls.Copies
	// targets gives the places of the copies.
	targets func(w *world) scrolls.Targets
	max     int
	quests  scrolls.QuestNames
}

func newWorld(t *testing.T, o worldOptions) *world {
	t.Helper()
	w := &world{t: t, ctx: context.Background(), clk: clock.NewFake(day0), root: t.TempDir(), changes: &changeLog{}}
	path := filepath.Join(t.TempDir(), "mestre.db")
	w.dbPath = path
	if err := os.WriteFile(path, guildTemplate, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := guildtpl.Open(w.ctx, path, store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	w.db = db
	if _, err := db.Writer().Exec(`INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES (?, 'lucas/meu-app', 'lucas/meu-app', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`, workspace); err != nil {
		t.Fatal(err)
	}
	w.reg = characters.Registry{DB: db}
	lk := &linker{}
	so := scrolls.Options{Store: &scrolls.Store{DB: db}, Clock: w.clk, Registry: w.reg, Principal: func(ws string) string { return filepath.Join(w.root, "principal", ws) },
		Tables: fakeTables{root: filepath.Join(w.root, "wt")}, Connections: lk, Copies: o.copies, OnChange: w.changes.add, MaxSize: o.max, Quests: o.quests}
	if o.targets != nil {
		so.Targets = o.targets(w)
	}
	w.svc, err = scrolls.NewService(so)
	if err != nil {
		t.Fatal(err)
	}
	w.conns, err = connections.NewService(connections.Options{Registry: w.reg, Clock: w.clk, Scrolls: w.svc})
	if err != nil {
		t.Fatal(err)
	}
	lk.mu.Lock()
	lk.c = w.conns
	lk.mu.Unlock()
	return w
}

// char registers a character of the table (the engine makes its automatic
// connections too).
func (w *world) char(role characters.Role, tb, name string) characters.Character {
	w.t.Helper()
	w.n++
	w.clk.Advance(time.Millisecond)
	c := characters.Character{
		ID: fmt.Sprintf("c%02d", w.n), Name: name, Kind: characters.KindCharacter, Role: role, Workspace: workspace, Table: tb,
		Machine: "m1", Sheet: "Dev Claude", Base: "claude-code", Class: "Dev", State: characters.Idle, Lifecycle: characters.Persistent,
		CreatedBy: characters.Creator{Kind: characters.CreatedByEngine}, CreatedAt: w.clk.Now(),
	}
	if err := w.reg.Insert(w.ctx, c); err != nil {
		w.t.Fatal(err)
	}
	if err := w.conns.Born(w.ctx, c); err != nil {
		w.t.Fatal(err)
	}
	return c
}

func (w *world) dev(name string) characters.Character {
	return w.char(characters.RoleCharacter, table, name)
}

func by(c characters.Character) scrolls.Actor {
	return scrolls.Actor{Kind: scrolls.ByCharacter, ID: c.ID, Name: c.Name}
}

func user(level characters.UserLevel) scrolls.Actor {
	return scrolls.Actor{Kind: scrolls.ByUser, ID: "u-" + string(level), Name: "Ana", Level: level}
}

// create makes a scroll as the character.
func (w *world) create(c characters.Character, content string, opts ...func(*scrolls.CreateRequest)) scrolls.Note {
	w.t.Helper()
	r := scrolls.CreateRequest{Workspace: workspace, Table: table, Actor: by(c), Content: content}
	for _, f := range opts {
		f(&r)
	}
	n, err := w.svc.Create(w.ctx, r)
	if err != nil {
		w.t.Fatal(err)
	}
	return n
}

func named(name string) func(*scrolls.CreateRequest) {
	return func(r *scrolls.CreateRequest) { r.Name = name }
}

func stacked(tome string) func(*scrolls.CreateRequest) {
	return func(r *scrolls.CreateRequest) { r.Stack = tome }
}

// read reads the content of a scroll as the actor.
func (w *world) read(a scrolls.Actor, name string) (scrolls.Note, string, error) {
	return w.svc.Read(w.ctx, scrolls.ReadRequest{Workspace: workspace, Table: table, Actor: a, Name: name})
}

func (w *world) write(c characters.Character, name, content string, rev *int64) (scrolls.Note, error) {
	return w.svc.Write(w.ctx, scrolls.WriteRequest{Workspace: workspace, Table: table, Actor: by(c), Name: name, Content: content, ExpectRev: rev})
}

// fileOf is the file of a scroll in the main clone.
func (w *world) fileOf(n scrolls.Note) string { return w.svc.File(n.Workspace, n.Table, n.ID) }

func rev(n int64) *int64 { return &n }
