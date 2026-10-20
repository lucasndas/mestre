package basesync_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/app"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/fakegit"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/internal/testkit/orcfakes"
	"github.com/lucasndas/mestre/pkg/clock"
)

// catalog knows the sheet the tests use.
type catalog struct{}

func (catalog) Sheet(_ context.Context, name string) (quests.SheetInfo, bool, error) {
	if name == "Dev Claude" {
		return quests.SheetInfo{Name: name, Class: "dev"}, true, nil
	}
	return quests.SheetInfo{}, false, nil
}

func (catalog) ClassAllowed(context.Context, string, string) error { return nil }

// world is the Citadel of the tests with a workspace, a table and its master.
type world struct {
	t        *testing.T
	ctx      context.Context
	clk      *clock.Fake
	orc      *app.Orchestration
	git      *fakegit.Git
	remote   string
	machines *orcfakes.Machines
	users    *orcfakes.Users
	fwd      *localchan.LocalForwarder
	slug     string
	table    tables.Table
	gm       characters.Character
	master   characters.Character
}

func newWorld(t *testing.T, verifier ...quests.Verifier) *world {
	t.Helper()
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	root := t.TempDir()
	db, err := guildtpl.Open(ctx, filepath.Join(root, "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
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

	w := &world{t: t, ctx: ctx, clk: clk, git: git, remote: remote.Dir(), users: &orcfakes.Users{}}
	w.machines = orcfakes.NewMachines(clk)
	w.machines.Chars = func() *characters.Service { return w.orc.Characters }
	oo := app.OrchestrationOptions{Guild: db, Machine: "cidadela", Clock: clk, WorkspacesDir: filepath.Join(root, "workspaces"), TempDir: filepath.Join(root, "tmp"),
		Git: git, Machines: w.machines, Presets: orcfakes.Presets{Persistent: map[string]bool{"Dev Claude": true}}, Builder: orcfakes.Builder{}, GitHub: orcfakes.Hub{URL: remote.Dir()},
		Epics: orcfakes.Epics{}, Alerts: orcfakes.Alerts{}, Files: orcfakes.Files{}, Messages: orcfakes.Messages{}, Peers: &orcfakes.Peers{Root: filepath.Join(root, "towers")},
		QuestCatalog: catalog{}, QuestVerifier: firstVerifier(verifier), Users: w.users, Destructive: orcfakes.Destructive{}}
	w.orc, err = app.NewOrchestration(oo)
	if err != nil {
		t.Fatal(err)
	}
	w.machines.Confirm = w.orc.Inbox.Confirmed
	w.fwd = localchan.NewLocalForwarder()
	w.orc.Register(w.fwd, nil)

	ws, err := w.orc.Workspaces.Create(ctx, workspaces.CreateRequest{Repo: "lucas/Meu_App"})
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
	w.slug = ws.Slug
	res, err := w.orc.Characters.Create(ctx, characters.CreateRequest{Sheet: "Grão-Mestre", Workspace: ws.Slug, Role: characters.RoleGrandMaster, Creator: characters.Creator{Kind: characters.CreatedByEngine}})
	if err != nil {
		t.Fatal(err)
	}
	w.gm = res.Character
	opened, err := w.orc.Tables.Open(ctx, tables.OpenRequest{By: w.gm, Epic: 42})
	if err != nil {
		t.Fatal(err)
	}
	w.table, w.master = opened.Table, opened.Master
	w.machines.Report(ctx, w.master.ID, characters.Idle)
	return w
}

// recruit makes a character of the table as the table master would.
func (w *world) recruit(sheet, name string) characters.Character {
	w.t.Helper()
	res, err := w.orc.Characters.Create(w.ctx, characters.CreateRequest{Name: name, Sheet: sheet, Workspace: w.slug, Table: w.table.ID, Role: characters.RoleCharacter,
		Creator: characters.Creator{Kind: characters.CreatedByCharacter, ID: w.master.ID}})
	if err != nil {
		w.t.Fatal(err)
	}
	return res.Character
}

func (w *world) char(name string) characters.Character {
	w.t.Helper()
	c, err := w.orc.Registry.ByName(w.ctx, w.slug, w.table.ID, name)
	if err != nil {
		w.t.Fatalf("character %q: %v", name, err)
	}
	return c
}

// mirror is the mirror of the workspace, where the branch of the table lives.
func (w *world) mirror() *fakegit.Handle {
	w.t.Helper()
	m, err := w.orc.Workspaces.OpenMirror(w.ctx, w.slug)
	if err != nil {
		w.t.Fatal(err)
	}
	return m.(*fakegit.Handle)
}

// commit puts a commit in the branch of the table, as an agent does.
func (w *world) commit(msg string, files map[string]string) {
	w.t.Helper()
	w.mirror().Data().Commit(w.table.Branch, msg, files)
}

// call is a command of an agent through the forwarder of the Citadel.
func (w *world) call(agent, cmd string, flags map[string]any, args ...string) localchan.Response {
	w.t.Helper()
	if flags == nil {
		flags = map[string]any{}
	}
	return w.fwd.Forward(w.ctx, &localchan.Call{
		Request: localchan.Request{Cmd: cmd, AgentID: agent, Args: localchan.TextArgs(args...), Flags: flags},
		Caller:  localchan.Caller{Identified: true, AgentID: agent},
	})
}

// finish is a character that works on its step, commits files and gives its
// done; then its turn ends.
func (w *world) finish(name, summary string, files map[string]string) quests.DoneResult {
	w.t.Helper()
	c := w.char(name)
	if len(files) > 0 {
		w.commit("feat: "+name, files)
	}
	r := w.call(c.ID, "done", nil, summary)
	if !r.OK {
		w.t.Fatalf("done of %s: %+v", name, r.Error)
	}
	var res quests.DoneResult
	if err := json.Unmarshal(r.Data, &res); err != nil {
		w.t.Fatal(err)
	}
	w.machines.Report(w.ctx, c.ID, characters.Idle)
	return res
}

// devWorks is the table with a Dev that changed code in a mission, and the PR
// of the table opened (the table in review).
func (w *world) devWorks() characters.Character {
	w.t.Helper()
	dev := w.recruit("Dev Claude", "Dev Claude 1")
	v, err := w.orc.Quests.Create(w.ctx, quests.CreateRequest{Workspace: w.slug, Table: w.table.ID, Designer: w.master.ID,
		Data: []byte("name: Login\nsteps:\n  - {id: dev, character: Dev Claude 1}\n")})
	if err != nil {
		w.t.Fatal(err)
	}
	if _, err := w.orc.Quests.Run(w.ctx, v.ID, ""); err != nil {
		w.t.Fatal(err)
	}
	w.finish("Dev Claude 1", "login feito", map[string]string{"login.go": "package app\n"})
	if _, err := w.orc.Tables.PROpened(w.ctx, w.slug, w.table.ID, 7, "head"); err != nil {
		w.t.Fatal(err)
	}
	return dev
}

// moveBase puts a commit in the main branch of GitHub and has the mirror
// fetched, as the loop of the workspace does.
func (w *world) moveBase(file string) {
	w.t.Helper()
	w.git.Handle(w.remote).Data().Commit("main", "feat: "+file, map[string]string{file: "x\n"})
	if err := w.orc.Workspaces.Fetch(w.ctx, w.slug); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) tableNow() tables.Table {
	w.t.Helper()
	t, err := w.orc.Tables.Get(w.ctx, w.slug, w.table.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	return t
}

// syncQuests lists the missions of the step sync of the table.
func (w *world) syncQuests() []quests.Quest {
	w.t.Helper()
	all, err := w.orc.Quests.Store().OfTable(w.ctx, w.slug, w.table.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	var out []quests.Quest
	for _, q := range all {
		if len(q.Name) >= 4 && q.Name[:4] == "sync" {
			out = append(out, q)
		}
	}
	return out
}

// itemsOf lists the items of the inbox of a character, as texts.
func (w *world) itemsOf(id string, kind inbox.Kind) []string {
	w.t.Helper()
	items, err := w.orc.Inbox.Store().All(w.ctx, id)
	if err != nil {
		w.t.Fatal(err)
	}
	var out []string
	for _, it := range items {
		if it.Kind == kind {
			out = append(out, it.Text)
		}
	}
	return out
}

func firstVerifier(v []quests.Verifier) quests.Verifier {
	if len(v) == 0 {
		return nil
	}
	return v[0]
}

var _ = fmt.Sprintf
