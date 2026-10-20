package epicdeps_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/connections"
	"github.com/lucasndas/mestre/internal/orchestration/epicdeps"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/waits"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/pkg/clock"
)

const workspace = "meu-app"

var day0 = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// The Guild database is migrated once, by TestMain; every test starts from a
// copy (docs/qualidade/testes.md).
var guildTemplate []byte

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "epicdeps-template")
	if err != nil {
		fmt.Fprintln(os.Stderr, "epicdeps:", err)
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
		fmt.Fprintln(os.Stderr, "epicdeps: template:", err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type deliverer struct{}

func (deliverer) Deliver(_ context.Context, _ string, d protocol.InboxDeliver) (protocol.InboxResult, error) {
	return protocol.InboxResult{ItemID: d.ItemID, Status: protocol.InboxDelivered}, nil
}

type notifier struct{}

func (notifier) Notify(context.Context, characters.Notice) error { return nil }

// fakeTables is the tables of the epics, as the dependencies see them.
type fakeTables struct {
	mu sync.Mutex
	by map[int]tables.Table
}

func (f *fakeTables) ByEpic(_ context.Context, _ string, epic int) (tables.Table, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.by[epic]
	return t, ok, nil
}

func (f *fakeTables) put(epic int, state tables.State, merged bool) tables.Table {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := tables.Table{Workspace: workspace, ID: fmt.Sprintf("%d-t", epic), Epic: epic, State: state, Merged: merged}
	f.by[epic] = t
	return t
}

func (f *fakeTables) drop(epic int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.by, epic)
}

type fakeEpics struct {
	mu  sync.Mutex
	by  map[int]tables.Epic
	err error
}

func (f *fakeEpics) Epic(_ context.Context, _ string, n int) (tables.Epic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return tables.Epic{}, f.err
	}
	e, ok := f.by[n]
	if !ok {
		return tables.Epic{}, tables.ErrEpicNotFound
	}
	return e, nil
}

type world struct {
	t        *testing.T
	ctx      context.Context
	db       *store.DB
	clk      *clock.Fake
	reg      characters.Registry
	in       *inbox.Service
	waits    *waits.Service
	tables   *fakeTables
	epics    *fakeEpics
	svc      *epicdeps.Service
	gm       characters.Character
	released []string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mestre.db")
	if err := os.WriteFile(path, guildTemplate, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := guildtpl.Open(context.Background(), path, store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Writer().Exec(`INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES (?, 'lucas/meu-app', 'lucas/meu-app', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`, workspace); err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, ctx: context.Background(), db: db, clk: clock.NewFake(day0), reg: characters.Registry{DB: db},
		tables: &fakeTables{by: map[int]tables.Table{}}, epics: &fakeEpics{by: map[int]tables.Epic{}}}
	w.gm = w.add(characters.RoleGrandMaster, "", characters.NameGrandMaster)
	w.in, err = inbox.NewService(inbox.Options{DB: &inbox.Store{DB: db}, Clock: w.clk, Registry: w.reg, Deliverer: deliverer{}})
	if err != nil {
		t.Fatal(err)
	}
	conns, err := connections.NewService(connections.Options{Registry: w.reg, Clock: w.clk})
	if err != nil {
		t.Fatal(err)
	}
	w.waits, err = waits.NewService(waits.Options{Store: &waits.Store{DB: db}, Clock: w.clk, Registry: w.reg, Inbox: w.in, Notifier: notifier{}, Connections: conns})
	if err != nil {
		t.Fatal(err)
	}
	w.svc = w.service()
	return w
}

// service builds the Service over the same database: a restart of the engine
// builds it again.
func (w *world) service() *epicdeps.Service {
	w.t.Helper()
	svc, err := epicdeps.New(epicdeps.Options{DB: w.db, Clock: w.clk, Tables: w.tables, Epics: w.epics, Characters: w.reg, Waits: w.waits, Inbox: w.in,
		Released: func(_ context.Context, _ string, t tables.Table) { w.released = append(w.released, t.ID) }})
	if err != nil {
		w.t.Fatal(err)
	}
	return svc
}

func (w *world) add(role characters.Role, table, name string) characters.Character {
	w.t.Helper()
	w.clk.Advance(time.Millisecond)
	id := fmt.Sprintf("c%d", w.clk.Now().UnixMilli()%100000)
	c := characters.Character{ID: id, Name: name, Kind: characters.KindCharacter, Role: role, Workspace: workspace, Table: table,
		Machine: "m1", TerminalID: "t" + id, Sheet: "Dev Claude", Base: "claude-code", Class: "Dev", State: characters.Busy, Lifecycle: characters.Persistent,
		CreatedBy: characters.Creator{Kind: characters.CreatedByEngine}, CreatedAt: w.clk.Now()}
	if err := w.reg.Insert(w.ctx, c); err != nil {
		w.t.Fatal(err)
	}
	return c
}

// inboxTexts lists the texts of every item the Grand Master got.
func (w *world) inboxTexts() []string {
	w.t.Helper()
	items, err := w.in.Store().All(w.ctx, w.gm.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	var out []string
	for _, it := range items {
		out = append(out, it.Text)
	}
	return out
}

func (w *world) mentions(substr ...string) bool {
	for _, text := range w.inboxTexts() {
		ok := true
		for _, s := range substr {
			ok = ok && strings.Contains(text, s)
		}
		if ok {
			return true
		}
	}
	return false
}

func (w *world) activeWaits() []waits.Wait {
	w.t.Helper()
	all, err := (&waits.Store{DB: w.db}).ActiveWaits(w.ctx)
	if err != nil {
		w.t.Fatal(err)
	}
	return all
}

func (w *world) held(epic int) bool {
	w.t.Helper()
	held, err := w.svc.Held(w.ctx, workspace, epic)
	if err != nil {
		w.t.Fatal(err)
	}
	return held
}

func isCycle(err error) (*epicdeps.CycleError, bool) {
	var c *epicdeps.CycleError
	return c, errors.As(err, &c)
}
