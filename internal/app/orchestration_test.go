package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/app"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/machinesheet"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/presets"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	"github.com/lucasndas/mestre/internal/testkit/fakegit"
	"github.com/lucasndas/mestre/pkg/clock"
)

// orcMachines is the engine of the Citadel's machine as the Citadel sees it: it
// starts and ends the characters, types what the inbox delivers and, like the
// runtime, reports the character at work.
type orcMachines struct {
	mu      sync.Mutex
	chars   func() *characters.Service
	started []characters.StartCommand
	stopped []characters.StopCommand
	typed   []protocol.InboxDeliver
	seq     map[string]uint64
	confirm func(ctx context.Context, id string) error
}

func (m *orcMachines) report(ctx context.Context, id string, st characters.State) {
	m.mu.Lock()
	m.seq[id]++
	seq := m.seq[id]
	m.mu.Unlock()
	_ = m.chars().Report(ctx, characters.Report{Character: id, Seq: seq, State: st, At: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)})
}

func (m *orcMachines) Start(ctx context.Context, _ string, cmd characters.StartCommand) (characters.StartResult, error) {
	m.mu.Lock()
	m.started = append(m.started, cmd)
	m.mu.Unlock()
	m.report(ctx, cmd.Character, characters.Idle)
	return characters.StartResult{Terminal: cmd.Terminal}, nil
}

func (m *orcMachines) Stop(ctx context.Context, _ string, cmd characters.StopCommand) error {
	m.mu.Lock()
	m.stopped = append(m.stopped, cmd)
	m.mu.Unlock()
	m.report(ctx, cmd.Character, characters.Stopped)
	return nil
}

func (m *orcMachines) Deliver(ctx context.Context, _ string, d protocol.InboxDeliver) (protocol.InboxResult, error) {
	m.mu.Lock()
	m.typed = append(m.typed, d)
	m.mu.Unlock()
	if cs, err := m.chars().Alive(ctx); err == nil {
		for _, c := range cs {
			if c.TerminalID == d.TerminalID {
				m.report(ctx, c.ID, characters.Busy)
			}
		}
	}
	_ = m.confirm(ctx, d.ItemID)
	return protocol.InboxResult{ItemID: d.ItemID, Status: protocol.InboxDelivered}, nil
}

func (m *orcMachines) Input(context.Context, string, string, []byte) error { return nil }
func (m *orcMachines) Screen(context.Context, string, string) (characters.Screen, error) {
	return characters.Screen{}, nil
}

// orcPresets has one sheet for everybody and one machine, the Citadel.
type orcPresets struct{}

func (orcPresets) Snapshot(_ context.Context, sheet string) (presets.Snapshot, error) {
	return presets.Snapshot{Sheet: presets.Sheet{Name: sheet, Base: "claude-code", Agent: presets.AgentClaudeCode, Class: "Dev"},
		Base: presets.Base{Name: "claude-code", Command: "claude"}}, nil
}
func (orcPresets) Base(context.Context, string) (presets.Base, bool, error) {
	return presets.Base{Name: "claude-code", Command: "claude"}, true, nil
}
func (orcPresets) Choose(context.Context, string, string) (machinesheet.Choice, error) {
	return machinesheet.Choice{Verdicts: []machinesheet.Verdict{{MachineID: "cidadela", Name: "Cidadela", OK: true}}}, nil
}
func (orcPresets) Machines(context.Context) ([]machinesheet.Sheet, error) { return nil, nil }
func (orcPresets) Sheets(context.Context) ([]presets.Sheet, error)        { return nil, nil }
func (orcPresets) Bases(context.Context) ([]presets.Base, error)          { return nil, nil }

type orcBuilder struct{}

func (orcBuilder) Build(_ context.Context, in characters.BuildInput) (sessionzero.CharacterRequest, error) {
	c := in.Character
	return sessionzero.CharacterRequest{ID: c.ID, Name: c.Name, Role: c.Role, Workspace: c.Workspace, Machine: c.Machine, Dir: in.Dir,
		Table: sessionzero.TableContext{ID: c.Table}, Snapshot: in.Snapshot}, nil
}

type orcGitHub struct{ url string }

func (g orcGitHub) Repository(_ context.Context, repo workspaces.Repo) (workspaces.RepoInfo, error) {
	return workspaces.RepoInfo{FullName: repo.String(), CloneURL: g.url, DefaultBranch: "main"}, nil
}

type orcEpics struct {
	mu     sync.Mutex
	closed []int
}

func (g *orcEpics) Epic(_ context.Context, _ string, n int) (tables.Epic, error) {
	if n != 42 {
		return tables.Epic{}, fmt.Errorf("%w: #%d", tables.ErrEpicNotFound, n)
	}
	return tables.Epic{Number: 42, Title: "Login de usuários", Labels: []string{"epico"}, Open: true}, nil
}

func (g *orcEpics) CloseEpic(_ context.Context, _ string, n int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed = append(g.closed, n)
	return nil
}
func (g *orcEpics) ClosePR(context.Context, string, int) error { return nil }

type orcAlerts struct{}

func (orcAlerts) Operational(context.Context, workspaces.Alert) error { return nil }

type orcFiles struct{}

func (orcFiles) Prepare(context.Context, sessionzero.TableRequest) (sessionzero.TableReport, error) {
	return sessionzero.TableReport{}, nil
}
func (orcFiles) Release(sessionzero.TableRequest) error { return nil }
func (orcFiles) Sync(ctx context.Context, _ sessionzero.TableRequest, f func(context.Context) error) error {
	return f(ctx)
}

type orcMessages struct{}

func (orcMessages) Message(_ context.Context, _, id string) (tables.Message, error) {
	return tables.Message{}, fmt.Errorf("no message %s", id)
}

func call(t *testing.T, fwd *localchan.LocalForwarder, agent, cmd string, flags map[string]any, args ...string) localchan.Response {
	t.Helper()
	if flags == nil {
		flags = map[string]any{}
	}
	return fwd.Forward(t.Context(), &localchan.Call{
		Request: localchan.Request{Cmd: cmd, AgentID: agent, Args: localchan.TextArgs(args...), Flags: flags},
		Caller:  localchan.Caller{Identified: true, AgentID: agent},
	})
}

// The services of milestone 4 are assembled and their commands answer through
// the forwarder of the Citadel: the Grand Master opens the table of an epic
// with `mestre table open`, the table master is recruited by the real Service of
// the characters, connected to the Grand Master, and gets the request in its
// inbox; the table is listed; and once the pull request is merged `mestre
// table close` ends it, the table master last. (Part of CA-CLI-GM-02: the epic
// comes from a fake GitHub, with the fake git.)
// orcWorld is the orchestration of a Citadel over the fakes of the pieces of
// the other milestones.
type orcWorld struct {
	orc      *app.Orchestration
	machines *orcMachines
	epics    *orcEpics
}

func newOrcWorld(t *testing.T) *orcWorld {
	t.Helper()
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

	w := &orcWorld{epics: &orcEpics{}}
	w.machines = &orcMachines{seq: map[string]uint64{}, chars: func() *characters.Service { return w.orc.Characters }}
	w.orc, err = app.NewOrchestration(app.OrchestrationOptions{Guild: db, Machine: "cidadela", Clock: clk, WorkspacesDir: filepath.Join(root, "workspaces"), TempDir: filepath.Join(root, "tmp"),
		Git: git, Machines: w.machines, Presets: orcPresets{}, Builder: orcBuilder{}, GitHub: orcGitHub{url: remote.Dir()}, Epics: w.epics, Alerts: orcAlerts{}, Files: orcFiles{},
		Messages: orcMessages{}})
	if err != nil {
		t.Fatal(err)
	}
	w.machines.confirm = w.orc.Inbox.Confirmed
	return w
}

// The loops of the orchestration run while the Citadel acts and end with it.
func TestTheLoopsOfTheOrchestrationEndWithTheContext(t *testing.T) {
	t.Parallel()
	w := newOrcWorld(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- w.orc.External()(ctx, nil) }()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("External = %v", err)
	}
}

func TestTheOrchestrationOfTheCitadelRunsATableThroughTheForwarder(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	w := newOrcWorld(t)
	orc, machines := w.orc, w.machines
	fwd := localchan.NewLocalForwarder()
	orc.Register(fwd, nil)
	for _, cmd := range []string{"table open", "table list", "table status", "table close", "list", "connect", "ask", "wait", "inbox", "reply", "note create", "done", "quest create", "quest run", "quest pause", "quest resume", "quest cancel", "quest status"} {
		if !slices.Contains(fwd.Commands(), cmd) {
			t.Errorf("the command %q is not registered: %v", cmd, fwd.Commands())
		}
	}

	// The workspace, active, and its Grand Master.
	ws, err := orc.Workspaces.Create(ctx, workspaces.CreateRequest{Repo: "lucas/Meu_App"})
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range workspaces.Stages {
		if st != workspaces.StageMirror {
			if err := orc.Workspaces.Register(st, func(context.Context, workspaces.Workspace) error { return nil }); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := orc.Workspaces.Prepare(ctx, ws.Slug); err != nil {
		t.Fatal(err)
	}
	gmRes, err := orc.Characters.Create(ctx, characters.CreateRequest{Sheet: "Grão-Mestre", Workspace: ws.Slug, Role: characters.RoleGrandMaster, Creator: characters.Creator{Kind: characters.CreatedByEngine}})
	if err != nil {
		t.Fatal(err)
	}
	gm := gmRes.Character
	if got, _ := orc.Characters.Get(ctx, gm.ID); got.State != characters.Idle {
		t.Fatalf("the Grand Master is %s; want idle", got.State)
	}

	// `mestre table open`, as the Grand Master.
	if r := call(t, fwd, gm.ID, "table open", map[string]any{"epic": "7"}); r.OK || r.ExitCode() != 4 {
		t.Fatalf("an epic that does not exist = %+v", r)
	}
	r := call(t, fwd, gm.ID, "table open", map[string]any{"epic": "42"})
	if !r.OK {
		t.Fatalf("table open = %+v", r.Error)
	}
	var opened struct {
		Name, Branch string
		Master       struct{ Name, ID, Machine string }
	}
	if err := json.Unmarshal(r.Data, &opened); err != nil {
		t.Fatal(err)
	}
	if opened.Name != "42-login-de-usuarios" || opened.Branch != "epico/42-login-de-usuarios" || opened.Master.Name != "Mestre" || opened.Master.Machine != "cidadela" {
		t.Fatalf("opened = %+v", opened)
	}
	master, ok, err := orc.Registry.TableMaster(ctx, ws.Slug, opened.Name)
	if err != nil || !ok || master.CreatedBy.Kind != characters.CreatedByCharacter || master.CreatedBy.ID != gm.ID || !master.Pinned {
		t.Fatalf("table master = %+v, %v, %v", master, ok, err)
	}
	if _, err := orc.Connections.Target(ctx, master.ID, "", gm.Name); err != nil {
		t.Errorf("the table master is not connected to the Grand Master: %v", err)
	}
	if got, _ := orc.Characters.Get(ctx, master.ID); got.State != characters.Busy {
		t.Fatalf("the table master is %s; want busy (it got the request)", got.State)
	}
	machines.mu.Lock()
	typed := len(machines.typed)
	machines.mu.Unlock()
	if typed != 1 {
		t.Errorf("typed = %d; want the request of the table", typed)
	}
	if got, _ := orc.Tables.Get(ctx, ws.Slug, opened.Name); got.State != tables.StateWorking {
		t.Errorf("the table is %s; want working", got.State)
	}

	if r := call(t, fwd, gm.ID, "table list", map[string]any{"state": "open"}); !r.OK || !strings.Contains(string(r.Data), "42-login-de-usuarios") {
		t.Errorf("table list = %+v %s", r.Error, r.Data)
	}
	// The Mestre da mesa cannot use the commands of the Grand Master.
	if r := call(t, fwd, master.ID, "table list", nil); r.OK || r.ExitCode() != 3 {
		t.Errorf("table list by the table master = %+v", r)
	}

	// The pull request is merged: the Grand Master ends the table.
	wt, err := orc.Tables.Store().Worktree(ctx, ws.Slug, opened.Name, "cidadela")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := orc.Tables.PROpened(ctx, ws.Slug, opened.Name, 9, wt.LastCommit); err != nil {
		t.Fatal(err)
	}
	if _, err := orc.Tables.MergeConfirmed(ctx, ws.Slug, opened.Name, ""); err != nil {
		t.Fatal(err)
	}
	// The table master is busy with a turn: the end waits for its safe point.
	r = call(t, fwd, gm.ID, "table close", nil, opened.Name)
	if r.OK || r.ExitCode() != localchan.ExitPending {
		t.Fatalf("close while the table master is busy = %+v", r)
	}
	if got, _ := orc.Tables.Get(ctx, ws.Slug, opened.Name); got.State != tables.StateClosing {
		t.Fatalf("the table is %s; want closing", got.State)
	}
	if err := orc.Characters.Done(ctx, master.ID, "PR mesclado"); err != nil { // it gives its done and its turn ends
		t.Fatal(err)
	}
	machines.report(ctx, master.ID, characters.Idle)
	if err := orc.Tables.Sweep(ctx); err != nil { // the engine's sweep takes it on
		t.Fatal(err)
	}
	if got, _ := orc.Tables.Get(ctx, ws.Slug, opened.Name); got.State != tables.StateClosed {
		t.Errorf("the table is %s; want closed", got.State)
	}
	if got, _ := orc.Characters.Get(ctx, master.ID); got.State != characters.Stopped {
		t.Errorf("the table master is %s; want stopped", got.State)
	}
	machines.mu.Lock()
	defer machines.mu.Unlock()
	if len(machines.stopped) != 1 || machines.stopped[0].Character != master.ID || !machines.stopped[0].Final {
		t.Errorf("stopped = %+v", machines.stopped)
	}
}
