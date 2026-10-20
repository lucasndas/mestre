package quests_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/app"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/scrolls"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/fakegit"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/internal/testkit/orcfakes"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The example of the document: Arquiteto -> Dev -> (Revisor, QA) -> fix, with
// a return of the Revisor to the Dev, an approval before the last step, and QA
// pinned to the other machine.
const exampleDef = `
name: Login com testes
max_returns: 3
steps:
  - {id: plan,   sheet: Arquiteto Claude, instructions: "Planeje o login"}
  - {id: dev,    sheet: Dev Claude, after: [plan], sub_issue: true}
  - {id: review, sheet: Revisor Codex, after: [dev], can_return_to: dev}
  - {id: qa,     sheet: QA Antigravity, after: [dev], machine: windows-pc}
  - {id: fix,    character: Dev Claude 1, after: [review, qa], join: all}
`

// towers is the half of the tables of the Tower: what a done there tells.
type towers struct {
	mu       sync.Mutex
	calls    []string
	warnings []string
	err      error
}

func (t *towers) Done(_ context.Context, machine, _, table string) (tables.DoneReport, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = append(t.calls, machine+"/"+table)
	return tables.DoneReport{Warnings: slices.Clone(t.warnings)}, t.err
}

// github is the GitHub of the quests: it records what was done, and fails when
// asked to.
type github struct {
	mu     sync.Mutex
	next   int
	issues map[int]*issue
	fail   bool
	calls  []string
}

type issue struct {
	Parent         int
	Title, Body    string
	Comments       []comment
	Closed, Reason string
}

type comment struct{ As, Body string }

func (g *github) record(call string) error {
	g.calls = append(g.calls, call)
	if g.fail {
		return fmt.Errorf("github is down")
	}
	return nil
}

func (g *github) make(parent int, title, body string) int {
	if g.issues == nil {
		g.issues = map[int]*issue{}
		g.next = 100
	}
	g.next++
	g.issues[g.next] = &issue{Parent: parent, Title: title, Body: body}
	return g.next
}

func (g *github) CreateIssue(_ context.Context, _ string, epic int, title, body string) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("issue"); err != nil {
		return 0, err
	}
	return g.make(epic, title, body), nil
}

func (g *github) CreateSubIssue(_ context.Context, _ string, parent int, title, body string) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("sub-issue"); err != nil {
		return 0, err
	}
	return g.make(parent, title, body), nil
}

func (g *github) UpdateBody(_ context.Context, _ string, n int, body string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("body"); err != nil {
		return err
	}
	g.issues[n].Body = body
	return nil
}

func (g *github) IssueBody(_ context.Context, _ string, n int) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("read"); err != nil {
		return "", err
	}
	i, ok := g.issues[n]
	if !ok {
		return "", fmt.Errorf("issue %d does not exist", n)
	}
	return i.Body, nil
}

func (g *github) Comment(_ context.Context, _ string, n int, class, body string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("comment"); err != nil {
		return err
	}
	g.issues[n].Comments = append(g.issues[n].Comments, comment{As: class, Body: body})
	return nil
}

func (g *github) Close(_ context.Context, _ string, n int, reason quests.CloseReason) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.record("close"); err != nil {
		return err
	}
	g.issues[n].Closed = string(reason)
	return nil
}

func (g *github) get(n int) issue {
	g.mu.Lock()
	defer g.mu.Unlock()
	if i, ok := g.issues[n]; ok {
		return *i
	}
	return issue{}
}

func (g *github) setFail(fail bool) {
	g.mu.Lock()
	g.fail = fail
	g.mu.Unlock()
}

// approvals is the flow of approval of milestone 5: it keeps the requests.
type approvals struct {
	mu    sync.Mutex
	next  int64
	asked []quests.MissionPoint
	fail  bool
}

func (a *approvals) OpenMissionPoint(_ context.Context, p quests.MissionPoint) (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fail {
		return 0, fmt.Errorf("the flow is down")
	}
	a.next++
	a.asked = append(a.asked, p)
	return 500 + a.next, nil
}

func (a *approvals) last() quests.MissionPoint {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.asked[len(a.asked)-1]
}

func (a *approvals) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.asked)
}

// catalog knows the sheets of the example and a class that is above the
// ceiling.
type catalog struct{}

func (catalog) Sheet(_ context.Context, name string) (quests.SheetInfo, bool, error) {
	switch name {
	case "Arquiteto Claude", "Dev Claude", "Revisor Codex", "QA Antigravity":
		return quests.SheetInfo{Name: name, Class: "dev"}, true, nil
	case "Root Claude":
		return quests.SheetInfo{Name: name, Class: "root"}, true, nil
	}
	return quests.SheetInfo{}, false, nil
}

func (catalog) ClassAllowed(_ context.Context, _, class string) error {
	if class == "root" {
		return fmt.Errorf("it allows what the ceiling does not")
	}
	return nil
}

// world is the Citadel of the tests with a workspace, a table and its master.
type world struct {
	t        *testing.T
	ctx      context.Context
	clk      *clock.Fake
	root     string
	orc      *app.Orchestration
	opts     app.OrchestrationOptions
	git      *fakegit.Git
	machines *orcfakes.Machines
	peers    *orcfakes.Peers
	towers   *towers
	hub      *github
	appr     *approvals
	users    *orcfakes.Users
	fwd      *localchan.LocalForwarder
	slug     string
	table    tables.Table
	gm       characters.Character
	master   characters.Character
}

type worldOpts struct {
	noGitHub, noApprovals, noTowers bool
	// perTable is the limit of characters of a table (0: the default).
	perTable int
	// ceiling is the ceiling of the workspace (nil: not checked).
	ceiling characters.Ceiling
	// done is the done_requires of the classes (nil: nothing is asked).
	done quests.DoneRules
	// verifier runs the verification of the workspace (nil: no command).
	verifier quests.Verifier
	// epics are other epics that exist and are open.
	epics []int
	// completed is told when a mission ends with all its steps done.
	completed func(ctx context.Context, workspace, table, quest string)
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

	w := &world{t: t, ctx: ctx, clk: clk, root: root, git: git, peers: &orcfakes.Peers{Root: filepath.Join(root, "towers")},
		towers: &towers{}, hub: &github{}, appr: &approvals{}, users: &orcfakes.Users{}}
	w.machines = orcfakes.NewMachines(clk)
	w.machines.Chars = func() *characters.Service { return w.orc.Characters }
	oo := app.OrchestrationOptions{Guild: db, Machine: "cidadela", Clock: clk, WorkspacesDir: filepath.Join(root, "workspaces"), TempDir: filepath.Join(root, "tmp"),
		Git: git, Machines: w.machines, Presets: orcfakes.Presets{Persistent: map[string]bool{"Dev Claude": true}}, Builder: orcfakes.Builder{}, GitHub: orcfakes.Hub{URL: remote.Dir()}, Epics: orcfakes.Epics{Also: opt.epics}, Alerts: orcfakes.Alerts{}, Files: orcfakes.Files{},
		Messages: orcfakes.Messages{}, Peers: w.peers, QuestCatalog: catalog{}, Users: w.users, Destructive: orcfakes.Destructive{}, Limits: characters.Limits{PerTable: opt.perTable}, Ceiling: opt.ceiling}
	oo.QuestDone = opt.done
	oo.MissionCompleted = opt.completed
	oo.QuestVerifier = opt.verifier
	if !opt.noTowers {
		oo.QuestTowers = w.towers
	}
	if !opt.noGitHub {
		oo.QuestGitHub = w.hub
	}
	if !opt.noApprovals {
		oo.QuestApprovals = w.appr
	}
	w.opts = oo
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
	// The table master finishes its first turn: it is free to be told things.
	w.machines.Report(ctx, w.master.ID, characters.Idle)
	return w
}

// restart is the Citadel going down and coming back: new services over the
// same database, which bring back what the engine had.
func (w *world) restart() {
	w.t.Helper()
	orc, err := app.NewOrchestration(w.opts)
	if err != nil {
		w.t.Fatal(err)
	}
	w.orc = orc
	w.machines.Confirm = orc.Inbox.Confirmed
	w.fwd = localchan.NewLocalForwarder()
	orc.Register(w.fwd, nil)
	if err := orc.Inbox.Recover(w.ctx); err != nil {
		w.t.Fatal(err)
	}
	if err := orc.Waits.Recover(w.ctx); err != nil {
		w.t.Fatal(err)
	}
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

func (w *world) get(id string) characters.Character {
	w.t.Helper()
	c, err := w.orc.Characters.Get(w.ctx, id)
	if err != nil {
		w.t.Fatal(err)
	}
	return c
}

// create makes the quest of the example, or of the definition given.
func (w *world) create(def string) quests.View {
	w.t.Helper()
	v, err := w.orc.Quests.Create(w.ctx, quests.CreateRequest{Workspace: w.slug, Table: w.table.ID, Data: []byte(def), Designer: w.master.ID})
	if err != nil {
		w.t.Fatalf("create: %v", err)
	}
	return v
}

func (w *world) run(q quests.View) quests.Quest {
	w.t.Helper()
	got, err := w.orc.Quests.Run(w.ctx, q.ID, "")
	if err != nil {
		w.t.Fatalf("run: %v", err)
	}
	return got
}

func (w *world) quest(id string) quests.Quest {
	w.t.Helper()
	q, err := w.orc.Quests.Store().Quest(w.ctx, id)
	if err != nil {
		w.t.Fatal(err)
	}
	return q
}

func (w *world) step(q quests.Quest, id string) quests.Step {
	w.t.Helper()
	st, err := w.orc.Quests.Store().Step(w.ctx, q.ID+"/"+id)
	if err != nil {
		w.t.Fatal(err)
	}
	return st
}

// states lists the state of every step by its id.
func (w *world) states(q quests.Quest) map[string]quests.StepState {
	w.t.Helper()
	steps, err := w.orc.Quests.Store().Steps(w.ctx, q.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	out := map[string]quests.StepState{}
	for _, st := range steps {
		out[st.ID] = st.State
	}
	return out
}

func (w *world) wantStates(q quests.Quest, want map[string]quests.StepState) {
	w.t.Helper()
	got := w.states(q)
	for id, st := range want {
		if got[id] != st {
			w.t.Errorf("step %s is %s; want %s (all: %v)", id, got[id], st, got)
		}
	}
}

// commit puts a commit in the branch of the table, in the mirror, as the work
// of an agent does once it is committed in its worktree.
func (w *world) commit(msg string, files map[string]string) {
	w.t.Helper()
	m, err := w.orc.Workspaces.OpenMirror(w.ctx, w.slug)
	if err != nil {
		w.t.Fatal(err)
	}
	m.(*fakegit.Handle).Data().Commit(w.table.Branch, msg, files)
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
// done; then its turn ends. It returns what the done answered.
func (w *world) finish(name, summary string, files map[string]string, ret string) quests.DoneResult {
	w.t.Helper()
	c := w.char(name)
	if len(files) > 0 {
		w.commit("feat: "+name, files)
	}
	flags := map[string]any{}
	if ret != "" {
		flags["return"] = ret
	}
	r := w.call(c.ID, "done", flags, summary)
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

// itemOf is the text of the last item `quest` put in the inbox of a character,
// delivered or waiting for it to be free.
func (w *world) itemOf(name string) string {
	w.t.Helper()
	items, err := w.orc.Inbox.Store().All(w.ctx, w.char(name).ID)
	if err != nil {
		w.t.Fatal(err)
	}
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Kind == inbox.KindQuest {
			return items[i].Text
		}
	}
	w.t.Fatalf("%s got no step", name)
	return ""
}

// scroll reads the scroll of the quest.
func (w *world) scroll(q quests.Quest) string {
	w.t.Helper()
	_, text, err := w.orc.Scrolls.Read(w.ctx, scrolls.ReadRequest{Workspace: w.slug, Table: w.table.ID, Actor: scrolls.Engine, Name: scrolls.QuestScrollName(q.Name)})
	if err != nil {
		w.t.Fatalf("scroll: %v", err)
	}
	return text
}
