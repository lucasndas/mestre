package rituals_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/app"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/rituals"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/fakegit"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/internal/testkit/orcfakes"
	"github.com/lucasndas/mestre/pkg/clock"
)

// machines are the fake machines with a record of what is typed in a terminal.
type machines struct {
	*orcfakes.Machines
	mu    sync.Mutex
	typed []string
	err   error
}

func (m *machines) Input(_ context.Context, machine, character string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.typed = append(m.typed, machine+"/"+character+": "+string(data))
	return nil
}

func (m *machines) inputs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.typed...)
}

// preRunner is the machine running the pre-run; permissions decide it first.
type preRunner struct {
	mu     sync.Mutex
	result rituals.PreRunResult
	err    error
	calls  []rituals.PreRunRequest
}

func (p *preRunner) Run(_ context.Context, r rituals.PreRunRequest) (rituals.PreRunResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, r)
	return p.result, p.err
}

func (p *preRunner) set(r rituals.PreRunResult) {
	p.mu.Lock()
	p.result = r
	p.mu.Unlock()
}

func (p *preRunner) ran() []rituals.PreRunRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]rituals.PreRunRequest(nil), p.calls...)
}

type permissions struct {
	mu     sync.Mutex
	deny   string
	asked  []string
	target []string
}

func (p *permissions) Allow(_ context.Context, c characters.Character, command string) (rituals.Decision, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.asked = append(p.asked, command)
	p.target = append(p.target, c.Name)
	if p.deny != "" {
		return rituals.Decision{Reason: p.deny}, nil
	}
	return rituals.Decision{Allow: true}, nil
}

func (p *permissions) denyWith(reason string) {
	p.mu.Lock()
	p.deny = reason
	p.mu.Unlock()
}

// world is the Citadel of the tests with a workspace, a table and its master.
type world struct {
	t        *testing.T
	ctx      context.Context
	clk      *clock.Fake
	orc      *app.Orchestration
	machines *machines
	pre      *preRunner
	perm     *permissions
	slug     string
	table    tables.Table
	gm       characters.Character
	master   characters.Character
}

// start is the instant the tests begin at.
var start = time.Date(2026, 10, 15, 9, 20, 0, 0, time.UTC)

type worldOpts struct {
	noPre   bool
	zone    *time.Location
	perTabl int
	// guildZone is the time zone kept in the Guild database (none: no row).
	guildZone string
}

func newWorld(t *testing.T, o ...worldOpts) *world {
	t.Helper()
	var opt worldOpts
	if len(o) > 0 {
		opt = o[0]
	}
	ctx := t.Context()
	clk := clock.NewFake(start)
	root := t.TempDir()
	db, err := guildtpl.Open(ctx, filepath.Join(root, "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if opt.guildZone != "" {
		if _, err := db.Writer().ExecContext(ctx, `INSERT INTO guild (id, guild_id, ca_cert, epoch, epoch_certificate, timezone, recovery_public_key, created_at)
			VALUES (1, '01M4ZPXYG0PNPDZ5JN7697ZSCH', x'00', 1, '{}', ?, x'07', 0)`, opt.guildZone); err != nil {
			t.Fatal(err)
		}
	}
	git := fakegit.NewGit(clk)
	remote, err := git.Init(ctx, filepath.Join(root, "remote", "meu-app"), gitx.InitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	git.Handle(remote.Dir()).Data().Commit("main", "chore: initial commit", map[string]string{"README.md": "# Meu App\n"})

	w := &world{t: t, ctx: ctx, clk: clk, pre: &preRunner{}, perm: &permissions{}}
	w.machines = &machines{Machines: orcfakes.NewMachines(clk)}
	w.machines.Chars = func() *characters.Service { return w.orc.Characters }
	oo := app.OrchestrationOptions{Guild: db, Machine: "cidadela", Clock: clk, WorkspacesDir: filepath.Join(root, "workspaces"), TempDir: filepath.Join(root, "tmp"),
		Git: git, Machines: w.machines, Presets: orcfakes.Presets{Persistent: map[string]bool{"Dev Claude": true}}, Builder: orcfakes.Builder{}, GitHub: orcfakes.Hub{URL: remote.Dir()},
		Epics: orcfakes.Epics{}, Alerts: orcfakes.Alerts{}, Files: orcfakes.Files{}, Messages: orcfakes.Messages{}, Peers: &orcfakes.Peers{Root: filepath.Join(root, "towers")},
		Users: &orcfakes.Users{}, Destructive: orcfakes.Destructive{}, Limits: characters.Limits{PerTable: opt.perTabl}}
	if !opt.noPre {
		oo.RitualPreRun, oo.RitualPermissions = w.pre, w.perm
	}
	if opt.zone != nil {
		oo.RitualZones = zone{opt.zone}
	}
	w.orc, err = app.NewOrchestration(oo)
	if err != nil {
		t.Fatal(err)
	}
	w.machines.Confirm = w.orc.Inbox.Confirmed

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
	w.machines.Report(ctx, w.gm.ID, characters.Idle)
	return w
}

type zone struct{ loc *time.Location }

func (z zone) Zone(context.Context, string) *time.Location { return z.loc }

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

// terminal inserts a simple terminal of the table (the sheets of the double have
// no machine to choose for a base).
func (w *world) terminal(name string) characters.Character {
	w.t.Helper()
	c := characters.Character{ID: "term-" + name, Name: name, Kind: characters.KindTerminal, Role: characters.RoleCharacter, Workspace: w.slug, Table: w.table.ID,
		Machine: "cidadela", Base: "bash", State: characters.Idle, Lifecycle: characters.Persistent, CreatedBy: characters.Creator{Kind: characters.CreatedByEngine},
		TerminalID: "tty-" + name, Dir: "/work/" + name, CreatedAt: w.clk.Now()}
	if err := w.orc.Registry.Insert(w.ctx, c); err != nil {
		w.t.Fatal(err)
	}
	return c
}

func (w *world) char(name string) characters.Character {
	w.t.Helper()
	c, err := w.orc.Registry.ByName(w.ctx, w.slug, w.table.ID, name)
	if err != nil {
		w.t.Fatalf("character %q: %v", name, err)
	}
	return c
}

// state reports the machine telling the Citadel the state of a character.
func (w *world) state(c characters.Character, st characters.State) {
	w.t.Helper()
	w.machines.Report(w.ctx, c.ID, st)
}

// master's actor.
func (w *world) byMaster() rituals.Actor {
	return rituals.Actor{Kind: rituals.ByCharacter, Character: w.master}
}

func (w *world) byGM() rituals.Actor {
	return rituals.Actor{Kind: rituals.ByCharacter, Character: w.gm}
}

// make creates a ritual of the table that aims at a character.
func (w *world) make(name, target string, sched rituals.Schedule, mod ...func(*rituals.CreateRequest)) rituals.Ritual {
	w.t.Helper()
	req := rituals.CreateRequest{Workspace: w.slug, Table: w.table.ID, Name: name, Target: target, Prompt: "Rode os testes", Schedule: sched, By: w.byMaster()}
	for _, m := range mod {
		m(&req)
	}
	r, err := w.orc.Rituals.Create(w.ctx, req)
	if err != nil {
		w.t.Fatalf("create ritual %s: %v", name, err)
	}
	return r
}

func (w *world) tick() {
	w.t.Helper()
	if err := w.orc.Rituals.Tick(w.ctx); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) firings(r rituals.Ritual) []rituals.Firing {
	w.t.Helper()
	fs, err := w.orc.Rituals.Store().Firings(w.ctx, r.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	return fs
}

// items are the items of a given kind in the inbox of a character, oldest first.
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

func (w *world) at(h, m int) time.Time {
	return time.Date(2026, 10, 15, h, m, 0, 0, time.UTC)
}

// goTo moves the fake clock to an instant.
func (w *world) goTo(t time.Time) {
	w.t.Helper()
	if d := t.Sub(w.clk.Now()); d > 0 {
		w.clk.Advance(d)
	}
}

func every(d time.Duration) rituals.Schedule { return rituals.Schedule{Kind: rituals.Every, Every: d} }

func daily(h, m int) rituals.Schedule {
	return rituals.Schedule{Kind: rituals.Daily, Hour: h, Minute: m}
}

func states(fs []rituals.Firing) string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = fmt.Sprintf("%s@%s", f.State, f.ScheduledFor.Format("15:04"))
	}
	return strings.Join(out, " ")
}
