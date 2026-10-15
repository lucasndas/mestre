package grimoires_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/app"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/connections"
	"github.com/lucasndas/mestre/internal/orchestration/grimoires"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/scrolls"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	"github.com/lucasndas/mestre/internal/testkit/fakegit"
	"github.com/lucasndas/mestre/internal/testkit/orcfakes"
	"github.com/lucasndas/mestre/pkg/clock"
)

// hub is the GitHub of the quests: it numbers the issues and remembers the epic
// each one was made under.
type hub struct {
	mu    sync.Mutex
	next  int
	epics map[int]int
}

func (h *hub) CreateIssue(_ context.Context, _ string, epic int, _, _ string) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next++
	if h.epics == nil {
		h.epics = map[int]int{}
	}
	h.epics[100+h.next] = epic
	return 100 + h.next, nil
}

func (h *hub) CreateSubIssue(_ context.Context, _ string, _ int, _, _ string) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next++
	return 100 + h.next, nil
}

func (*hub) UpdateBody(context.Context, string, int, string) error        { return nil }
func (*hub) Comment(context.Context, string, int, string, string) error   { return nil }
func (*hub) Close(context.Context, string, int, quests.CloseReason) error { return nil }
func (h *hub) epicOf(issue int) int                                       { h.mu.Lock(); defer h.mu.Unlock(); return h.epics[issue] }

// ceilingOf is the ceiling of a workspace, which a test changes.
type ceilingOf struct {
	mu sync.Mutex
	c  *classes.Ceiling
}

func (c *ceilingOf) Current(context.Context) (*classes.Ceiling, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.c, nil
}

func (c *ceilingOf) set(y string, t testing.TB) {
	t.Helper()
	cl, err := classes.ParseCeiling([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.c = cl
	c.mu.Unlock()
}

// side is a workspace of the Guild with a table and its masters.
type side struct {
	slug    string
	epic    int
	table   tables.Table
	gm      characters.Character
	master  characters.Character
	lib     *classes.Library
	ceiling *ceilingOf
}

// world is a Citadel with two workspaces, A and B, each with a working table.
type world struct {
	t        *testing.T
	ctx      context.Context
	clk      *clock.Fake
	orc      *app.Orchestration
	machines *orcfakes.Machines
	hub      *hub
	a, b     side
}

var admin = grimoires.Actor{ID: "ana", Name: "Ana", Level: characters.LevelAdministrator}

type worldOpts struct {
	perTable, perWorkspace int
}

func newWorld(t *testing.T, o ...worldOpts) *world {
	t.Helper()
	var opt worldOpts
	if len(o) > 0 {
		opt = o[0]
	}
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC))
	root := t.TempDir()
	db, err := guilddb.Open(ctx, filepath.Join(root, "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	git := fakegit.NewGit(clk)
	remote, err := git.Init(ctx, filepath.Join(root, "remote", "meu-app"), gitx.InitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	git.Handle(remote.Dir()).Data().Commit("main", "chore: initial commit", map[string]string{"README.md": "# Meu App\n"})

	w := &world{t: t, ctx: ctx, clk: clk, hub: &hub{}}
	w.machines = orcfakes.NewMachines(clk)
	w.machines.Chars = func() *characters.Service { return w.orc.Characters }
	libs := map[string]*classes.Library{}
	var mu sync.Mutex
	oo := app.OrchestrationOptions{Guild: db, Machine: "cidadela", Clock: clk, WorkspacesDir: filepath.Join(root, "workspaces"), TempDir: filepath.Join(root, "tmp"),
		Git: git, Machines: w.machines, Presets: orcfakes.Presets{Persistent: map[string]bool{"Dev Claude": true}}, Builder: orcfakes.Builder{}, GitHub: orcfakes.Hub{URL: remote.Dir()},
		Epics: orcfakes.Epics{Also: []int{77, 99}}, Alerts: orcfakes.Alerts{}, Files: orcfakes.Files{}, Messages: orcfakes.Messages{}, Peers: &orcfakes.Peers{Root: filepath.Join(root, "towers")},
		Users: &orcfakes.Users{}, Destructive: orcfakes.Destructive{}, QuestGitHub: w.hub,
		Limits: characters.Limits{PerTable: opt.perTable, PerWorkspace: opt.perWorkspace},
		Classes: grimoires.LibraryClasses{For: func(_ context.Context, ws string) (*classes.Library, error) {
			mu.Lock()
			defer mu.Unlock()
			return libs[ws], nil
		}}}
	w.orc, err = app.NewOrchestration(oo)
	if err != nil {
		t.Fatal(err)
	}
	w.machines.Confirm = w.orc.Inbox.Confirmed

	mk := func(repo string, epic int) side {
		ws, err := w.orc.Workspaces.Create(ctx, workspaces.CreateRequest{Repo: repo})
		if err != nil {
			t.Fatal(err)
		}
		for _, st := range workspaces.Stages {
			if st != workspaces.StageMirror {
				if err := w.orc.Workspaces.Register(st, func(context.Context, workspaces.Workspace) error { return nil }); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := w.orc.Workspaces.Prepare(ctx, ws.Slug); err != nil {
			t.Fatal(err)
		}
		res, err := w.orc.Characters.Create(ctx, characters.CreateRequest{Sheet: "Grão-Mestre", Workspace: ws.Slug, Role: characters.RoleGrandMaster, Creator: characters.Creator{Kind: characters.CreatedByEngine}})
		if err != nil {
			t.Fatal(err)
		}
		opened, err := w.orc.Tables.Open(ctx, tables.OpenRequest{By: res.Character, Epic: epic})
		if err != nil {
			t.Fatal(err)
		}
		w.machines.Report(ctx, opened.Master.ID, characters.Idle)
		ceil := &ceilingOf{c: classes.DefaultCeiling()}
		lib := &classes.Library{Workspace: ws.Slug, Roots: classes.DefaultRoots(filepath.Join(root, "classes-globais"), w.orc.Workspaces.Layout().Principal(ws.Slug)),
			Records: classes.ClassDB{DB: db, Clock: clk}, Ceiling: ceil, Clock: clk, OS: "linux", Skill: func(string) bool { return false }}
		mu.Lock()
		libs[ws.Slug] = lib
		mu.Unlock()
		return side{slug: ws.Slug, epic: epic, table: opened.Table, gm: res.Character, master: opened.Master, lib: lib, ceiling: ceil}
	}
	w.a = mk("lucas/Meu_App", 42)
	w.b = mk("lucas/Outro_App", 77)
	return w
}

// recruit makes a character in a table as its master would.
func (w *world) recruit(s side, sheet, name string, class string) characters.Character {
	w.t.Helper()
	res, err := w.orc.Characters.Create(w.ctx, characters.CreateRequest{Name: name, Sheet: sheet, Class: class, Workspace: s.slug, Table: s.table.ID, Role: characters.RoleCharacter,
		Creator: characters.Creator{Kind: characters.CreatedByCharacter, ID: s.master.ID}})
	if err != nil {
		w.t.Fatal(err)
	}
	return res.Character
}

func (w *world) char(s side, name string) characters.Character {
	w.t.Helper()
	c, err := w.orc.Registry.ByName(w.ctx, s.slug, s.table.ID, name)
	if err != nil {
		w.t.Fatalf("character %q: %v", name, err)
	}
	return c
}

func (w *world) has(s side, name string) bool {
	_, err := w.orc.Registry.ByName(w.ctx, s.slug, s.table.ID, name)
	return err == nil
}

// scroll makes a scroll of a table.
func (w *world) scroll(s side, name, content, tome string) scrolls.Note {
	w.t.Helper()
	n, err := w.orc.Scrolls.Create(w.ctx, scrolls.CreateRequest{Workspace: s.slug, Table: s.table.ID, Name: name, Content: content, Stack: tome,
		Actor: scrolls.Actor{Kind: scrolls.ByUser, ID: "ana", Name: "Ana", Level: characters.LevelAdministrator}})
	if err != nil {
		w.t.Fatal(err)
	}
	return n
}

// link connects two elements by hand.
func (w *world) link(s side, a, b connections.Ref) {
	w.t.Helper()
	if err := w.orc.Connections.Link(w.ctx, s.slug, s.table.ID, a, b, s.master.ID); err != nil {
		w.t.Fatal(err)
	}
}

const questDef = `name: Fluxo
steps:
  - {id: dev, character: Dev, instructions: "Faça o login"}
  - {id: qa, character: QA, after: [dev], instructions: "Teste o login"}
`

func (w *world) quest(s side, def string) quests.View {
	w.t.Helper()
	v, err := w.orc.Quests.Create(w.ctx, quests.CreateRequest{Workspace: s.slug, Table: s.table.ID, Data: []byte(def), Designer: s.master.ID})
	if err != nil {
		w.t.Fatal(err)
	}
	return v
}

// pkg is the package of a class that asks for the permissions given.
func pkg(t testing.TB, name, perms string) classes.Files {
	t.Helper()
	files, err := classes.NewFiles([]classes.File{
		{Path: "CLASS.md", Data: []byte("---\nname: " + name + "\ndescription: Testa a mesa\napp: novo\n---\nTeste a mesa e relate as falhas.\n")},
		{Path: "permissions.yaml", Data: []byte(perms)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

const modestPerms = "version: 1\nfiles: {write: [\"tests/**\"]}\ncommands: {allow: [\"npm test\"], default: ask}\n"

// workspaceClass saves a class of the workspace, approved.
func (w *world) workspaceClass(s side, name, perms string) {
	w.t.Helper()
	res, err := s.lib.Save(w.ctx, classes.SaveRequest{Name: name, Scope: classes.ClassWorkspace, App: "novo", Files: pkg(w.t, name, perms), By: classes.Author{Level: classes.LevelAdmin, ID: "ana"}})
	if err != nil || !res.Applied {
		w.t.Fatalf("class %s: %+v, %v", name, res, err)
	}
}

func (w *world) items(c characters.Character, kind inbox.Kind) []inbox.Item {
	w.t.Helper()
	all, err := w.orc.Inbox.Store().All(w.ctx, c.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	var out []inbox.Item
	for _, it := range all {
		if it.Kind == kind {
			out = append(out, it)
		}
	}
	return out
}

// save saves a selection of a table under a name.
func (w *world) save(s side, name string, sel grimoires.Selection, mod ...func(*grimoires.SaveRequest)) grimoires.SaveResult {
	w.t.Helper()
	req := grimoires.SaveRequest{Workspace: s.slug, Table: s.table.ID, Name: name, Select: sel, By: admin}
	for _, m := range mod {
		m(&req)
	}
	res, err := w.orc.Grimoires.Save(w.ctx, req)
	if err != nil {
		w.t.Fatalf("save: %v", err)
	}
	return res
}

func picks(ids ...string) []grimoires.Pick {
	out := make([]grimoires.Pick, len(ids))
	for i, id := range ids {
		out[i] = grimoires.Pick{ID: id}
	}
	return out
}
