package connections_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/connections"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/pkg/clock"
)

var day0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

// The Guild database is migrated once, by TestMain; every test starts from a
// copy (docs/qualidade/testes.md).
var guildTemplate []byte

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "connections-template")
	if err != nil {
		fmt.Fprintln(os.Stderr, "connections:", err)
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
		fmt.Fprintln(os.Stderr, "connections: template:", err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

const workspace = "meu-app"

// world is a workspace with a registry of characters, the connections over it
// and the scrolls of a later stage, faked.
type world struct {
	t       *testing.T
	ctx     context.Context
	clk     *clock.Fake
	db      *store.DB
	reg     characters.Registry
	svc     *connections.Service
	scrolls *fakeScrolls
	n       int
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t, ctx: context.Background(), clk: clock.NewFake(day0), scrolls: &fakeScrolls{items: map[string]connections.ScrollRef{}, tomes: map[string][]string{}}}
	path := filepath.Join(t.TempDir(), "mestre.db")
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
	w.svc, err = connections.NewService(connections.Options{Registry: w.reg, Clock: w.clk, Scrolls: w.scrolls})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// add registers a character as the engine does (the row, then Born).
func (w *world) add(role characters.Role, table, name string, by characters.Creator) characters.Character {
	w.t.Helper()
	w.n++
	w.clk.Advance(time.Millisecond)
	c := characters.Character{
		ID: fmt.Sprintf("c%02d", w.n), Name: name, Kind: characters.KindCharacter, Role: role, Workspace: workspace, Table: table,
		Machine: "linux", Sheet: "Dev Claude", Base: "claude-code", Class: "Dev", State: characters.Idle, Lifecycle: characters.Persistent,
		CreatedBy: by, CreatedAt: w.clk.Now(),
	}
	if err := w.reg.Insert(w.ctx, c); err != nil {
		w.t.Fatal(err)
	}
	if err := w.svc.Born(w.ctx, c); err != nil {
		w.t.Fatal(err)
	}
	return c
}

func (w *world) gm() characters.Character {
	return w.add(characters.RoleGrandMaster, "", characters.NameGrandMaster, characters.Creator{Kind: characters.CreatedByEngine})
}

func (w *world) master(table string, gm characters.Character) characters.Character {
	return w.add(characters.RoleTableMaster, table, characters.NameTableMaster, characters.Creator{Kind: characters.CreatedByCharacter, ID: gm.ID})
}

func (w *world) dev(table, name string, by characters.Creator) characters.Character {
	return w.add(characters.RoleCharacter, table, name, by)
}

func by(c characters.Character) characters.Creator {
	return characters.Creator{Kind: characters.CreatedByCharacter, ID: c.ID}
}

// reasons are the reasons of the connection of two endpoints, sorted and
// joined; empty when they are not connected.
func (w *world) reasons(a, b connections.Ref) string {
	w.t.Helper()
	c, ok, err := w.svc.Store().Between(w.ctx, a, b)
	if err != nil {
		w.t.Fatal(err)
	}
	if !ok {
		return ""
	}
	names := c.ReasonNames()
	slices.Sort(names)
	return strings.Join(names, ",")
}

func ch(c characters.Character) connections.Ref { return connections.Char(c.ID) }

// names are the names of the characters, in order.
func names(cs []characters.Character) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
	}
	return out
}

// fakeScrolls are the scrolls and tomes of stage 7.
type fakeScrolls struct {
	mu    sync.Mutex
	items map[string]connections.ScrollRef
	tomes map[string][]string
}

func (f *fakeScrolls) scroll(id, name, table string) connections.Ref {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[id] = connections.ScrollRef{ID: id, Name: name, Table: table}
	return connections.Scroll(id)
}

func (f *fakeScrolls) tome(id, name, table string, members ...string) connections.Ref {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[id] = connections.ScrollRef{ID: id, Name: name, Table: table, Tome: true}
	f.tomes[id] = members
	return connections.Tome(id)
}

func (f *fakeScrolls) join(tome, scroll string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tomes[tome] = append(f.tomes[tome], scroll)
}

func (f *fakeScrolls) Resolve(_ context.Context, _, table, nameOrID string) (connections.ScrollRef, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.items {
		if s.Table == table && (strings.EqualFold(s.Name, nameOrID) || s.ID == nameOrID) {
			return s, true, nil
		}
	}
	return connections.ScrollRef{}, false, nil
}

func (f *fakeScrolls) Get(_ context.Context, ref connections.Ref) (connections.ScrollRef, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.items[ref.ID]
	return s, ok, nil
}

func (f *fakeScrolls) Members(_ context.Context, tome string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.tomes[tome]), nil
}
