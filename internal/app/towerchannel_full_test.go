//go:build full

package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/presets"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/pkg/clock"
)

// prepGate holds the session zero of the characters of the Tower until it is
// released: the point where a start can be cancelled with the Tower in the middle of it.
type prepGate struct {
	characters.Preparer
	entered chan string
	release chan struct{}
}

func (g *prepGate) Prepare(ctx context.Context, req sessionzero.CharacterRequest) (sessionzero.CharacterPlan, error) {
	select {
	case g.entered <- req.ID:
	default:
	}
	select {
	case <-g.release:
	case <-ctx.Done():
		return sessionzero.CharacterPlan{}, ctx.Err()
	}
	return g.Preparer.Prepare(ctx, req)
}

// channelRig is a Citadel that hosts the orchestration and a Tower with its
// terminals and the fake agent.
type channelRig struct {
	r    *rig
	a, b *inproc
	root string
	orc  *Orchestration
	work string
	gate *prepGate
}

func newChannelRig(t *testing.T, gated ...bool) *channelRig {
	t.Helper()
	return newChannelRigWith(t, channelOpts{gated: len(gated) > 0 && gated[0]})
}

// channelOpts are the choices of a channelRig: gated holds the preparation of the
// Tower until released; builder gives the Citadel a session zero of a character.
type channelOpts struct {
	gated, builder bool
	// resumed records the session each build of the session zero resumes (with builder)
	resumed *resumeLog
}

func newChannelRigWith(t *testing.T, o channelOpts) *channelRig {
	t.Helper()
	buildRuntimeBinaries(t)
	r := newRig(t)
	root, err := os.MkdirTemp("", "mrt")
	if err != nil {
		t.Fatal(err)
	}
	if rr, err := filepath.EvalSymlinks(root); err == nil {
		root = rr
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	r.root = root
	x := &channelRig{r: r, root: root}
	var gate *prepGate
	x.a = r.node("a")
	x.b = r.node("b", x.a)
	x.a.paths.CLISocket = filepath.Join(root, "a", "cli.sock")
	x.b.paths.CLISocket = filepath.Join(root, "b", "cli.sock")
	home := filepath.Join(root, "home")
	for rel, content := range map[string]string{
		"cfg/.credentials.json": "{}",
		"quick.yaml":            "prompts:\n  - match: \".*\"\n    steps:\n      - say: \"feito\"\n",
	} {
		p := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	x.work = filepath.Join(x.b.paths.Workspaces, "meu-app", "mesas", "3-login")
	if err := os.MkdirAll(filepath.Join(x.work, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(x.work, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	x.a.setup = func(e *Engine) {
		e.deps.HostOrchestration = true
		if o.builder {
			e.deps.Integration = builderIntegration{resumed: o.resumed}
		}
	}
	x.b.cfg.Terminais = &Terminals{Dir: runtimeBuilds.ptyFolde, Version: runtimePTYVersion}
	x.b.setup = func(e *Engine) {
		e.deps.Spawner = &terminal.DirectSpawner{Suite: "full", System: terminal.NewSystem()}
		var prep characters.Preparer = runtimePrep{cfg: filepath.Join(home, "cfg"), home: home, sock: x.b.paths.CLISocket}
		if o.gated {
			gate = &prepGate{Preparer: prep, entered: make(chan string, 1), release: make(chan struct{})}
			x.gate = gate
			prep = gate
		}
		e.deps.CharacterPrep = prep
	}
	r.found(x.a, clock.Real{})
	r.start(x.a, clock.Real{})
	r.waitFor("a to act and host the orchestration", func() bool { return x.a.acting() && x.a.e.orc.Load() != nil })
	r.join(x.b, x.a, clock.Real{})
	r.start(x.b, clock.Real{})
	r.waitFor("b to connect", func() bool { return x.b.connected(1) })
	x.orc = x.a.e.orc.Load()
	if _, err := orcDB(t, x.a).Writer().ExecContext(t.Context(), `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES ('meu-app', 'lucas/meu-app', 'lucas/meu-app', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	cat := &presets.Catalog{DB: orcDB(t, x.a), Clock: clock.Real{}}
	doc, err := presets.ParseDocument([]byte("kind: mestre/presets\nversion: 1\nbases:\n  - {name: claude-code, command: claude, platforms: [{os: linux}, {os: windows, command: claude.exe}], ready: hook}\n" +
		"sheets:\n  - {name: Dev Claude, base: claude-code, agent: claude-code, class: Dev, max_copies: 200}\n"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := cat.PlanImport(t.Context(), doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cat.ApplyImport(t.Context(), plan, nil); err != nil {
		t.Fatal(err)
	}
	return x
}

// character inserts a character of the table on the Tower in the registry.
func (x *channelRig) character(t *testing.T, id, name string, st characters.State) characters.Character {
	t.Helper()
	c := characters.Character{ID: id, Name: name, Kind: characters.KindCharacter, Role: characters.RoleCharacter, Workspace: "meu-app", Table: "3-login", Machine: x.b.id,
		Sheet: "Dev Claude", Base: "claude-code", Class: "Dev", Dir: x.work, State: st, Reason: "start", TerminalID: "t-" + id,
		CreatedBy: characters.Creator{Kind: characters.CreatedByEngine}, Lifecycle: characters.Ephemeral, CreatedAt: clock.Real{}.Now()}
	if err := x.orc.Registry.Insert(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	return c
}

func (x *channelRig) state(t *testing.T, id string) characters.State {
	t.Helper()
	c, err := x.orc.Registry.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return c.State
}

// A character of a Tower that has an active wait on the Citadel is not idle
// when its turn ends: the waits are in the database of the Citadel, which a
// Tower cannot ask, so the Citadel turns the idle of the report into waiting.
// Part of CA-CIDADELA-16
func TestATurnThatEndsOnATowerWithAnActiveWaitEndsWaiting(t *testing.T) {
	t.Parallel()
	x := newChannelRig(t)
	ctx := t.Context()
	c := x.character(t, "c9", "Dev-9", characters.Busy)
	if _, err := x.orc.Waits.WaitUntil(ctx, c, clock.Real{}.Now().Add(time.Hour)); err != nil {
		t.Fatalf("the wait: %v", err)
	}
	report := func(state string) {
		t.Helper()
		if res, err := x.b.e.towerLink().Call(ctx, protocol.Outgoing{Type: protocol.TypeTerminalStatus, Payload: protocol.TerminalStatus{TerminalID: "t-c9", State: state, AgentID: c.ID}}); err != nil || res.Status != protocol.AckOK {
			t.Fatalf("report %s: %+v %v", state, res, err)
		}
	}
	report("busy")
	report("idle")
	if got := x.state(t, c.ID); got != characters.Waiting {
		t.Errorf("the character with a wait is %s after its turn ended on the Tower; want waiting", got)
	}
	// one with no wait is idle
	d := x.character(t, "c8", "Dev-8", characters.Busy)
	if res, err := x.b.e.towerLink().Call(ctx, protocol.Outgoing{Type: protocol.TypeTerminalStatus, Payload: protocol.TerminalStatus{TerminalID: "t-c8", State: "idle", AgentID: d.ID}}); err != nil || res.Status != protocol.AckOK {
		t.Fatalf("report idle: %+v %v", res, err)
	}
	if got := x.state(t, d.ID); got != characters.Idle {
		t.Errorf("the character with no wait is %s; want idle", got)
	}
}

// A character the registry has alive on a Tower that the Tower does not know
// (it lost its row, or its terminal is gone) is interrupted when the Tower
// tells its terminals at the next connection; one it does have keeps its state.
// Part of CA-CIDADELA-11
func TestACharacterTheTowerDoesNotHaveIsInterruptedWhenItConnects(t *testing.T) {
	t.Parallel()
	x := newChannelRig(t)
	ctx := t.Context()
	ghost := x.character(t, "c7", "Fantasma", characters.Busy)
	// a real one, started on the Tower
	real := x.character(t, "c1", "Dev-1", characters.Starting)
	if err := x.startOnTower(ctx, real); err != nil {
		t.Fatal(err)
	}
	x.r.waitFor("the real character idle", func() bool { return x.state(t, real.ID) == characters.Idle })

	x.a.e.srvp.Load().CloseAll(protocol.CloseShuttingDown, "the test cuts the connection")
	x.r.waitFor("the ghost to be interrupted by the resync", func() bool { return x.state(t, ghost.ID) == characters.Interrupted })
	if got := x.state(t, real.ID); got != characters.Idle {
		t.Errorf("the character the Tower has is %s after the resync; want idle", got)
	}
}

// startOnTower asks for the start of a character of the table on the Tower, with
// the context given.
func (x *channelRig) startOnTower(ctx context.Context, c characters.Character) error {
	req := sessionzero.CharacterRequest{
		ID: c.ID, Name: c.Name, Role: c.Role, Workspace: "meu-app", Table: sessionzero.TableContext{ID: "3-login"}, Machine: x.b.id, Dir: x.work,
		Snapshot: presets.Snapshot{Sheet: presets.Sheet{Name: c.Sheet, Agent: "claude-code", Class: "Dev"}, Base: presets.Base{Name: "claude-code", Ready: presets.Ready{Mode: presets.ReadyHook}}},
	}
	_, err := x.a.e.mach.Load().Start(ctx, x.b.id, characters.StartCommand{Character: c.ID, Terminal: "t-" + c.ID, Kind: characters.KindCharacter, Request: req})
	return err
}

// aliveOnTower says whether the Tower has a live terminal of the character.
func (x *channelRig) aliveOnTower(id string) bool {
	for _, s := range x.b.e.rt.rt.Live() {
		if s.AgentID == id && characters.State(s.State).Alive() {
			return true
		}
	}
	return false
}

// A start the Citadel gives up on (it was cancelled while the Tower prepared the
// character) does not leave the terminal alive on the Tower: the Citadel tells the
// Tower to end it.
// Part of CA-CIDADELA-16
func TestAStartTheCitadelGivesUpOnLeavesNoTerminalOnTheTower(t *testing.T) {
	t.Parallel()
	x := newChannelRig(t, true)
	c := x.character(t, "c1", "Dev-1", characters.Starting)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- x.startOnTower(ctx, c) }()
	<-x.gate.entered // the Tower is in the middle of the start
	cancel()         // the Citadel gives up
	close(x.gate.release)
	if err := <-result; err == nil {
		t.Fatal("the start the caller gave up on answered ok")
	}
	// the stop of the Citadel goes behind the start on the same line: it finds the character the start made
	x.r.waitFor("the Tower to have no live terminal of the character", func() bool { return !x.aliveOnTower(c.ID) })
	if _, err := x.b.e.rt.rt.Env(t.Context(), c.ID); err == nil {
		t.Error("the character is still on the Tower")
	}
}

// A character that the registry has alive and Starting but that no start of the
// Citadel is making is not exempt from the resync: it was lost on the way.
// Part of CA-CIDADELA-11
func TestAStartingCharacterNoStartIsMakingIsInterruptedByTheResync(t *testing.T) {
	t.Parallel()
	x := newChannelRig(t)
	ghost := x.character(t, "c5", "Perdido", characters.Starting)
	// a start lost on the way: its row is older than any start takes
	if _, err := orcDB(t, x.a).Writer().ExecContext(t.Context(), `UPDATE characters SET created_at = ? WHERE id = ?`, clock.Real{}.Now().Add(-10*time.Minute).UnixMilli(), ghost.ID); err != nil {
		t.Fatal(err)
	}
	x.a.e.srvp.Load().CloseAll(protocol.CloseShuttingDown, "the test cuts the connection")
	x.r.waitFor("the lost start to be interrupted", func() bool { return x.state(t, ghost.ID) == characters.Interrupted })
}

// What the Tower has a terminal of and the Citadel does not have on that machine (a
// character that ended, a start whose stop was lost) is ended by the Tower at the
// resync.
// Part of CA-CIDADELA-16
func TestATerminalTheCitadelDoesNotHaveIsEndedByTheTowerAtTheResync(t *testing.T) {
	t.Parallel()
	x := newChannelRig(t)
	c := x.character(t, "c1", "Dev-1", characters.Starting)
	if err := x.startOnTower(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	x.r.waitFor("the character idle", func() bool { return x.state(t, c.ID) == characters.Idle })
	if _, err := orcDB(t, x.a).Writer().ExecContext(t.Context(), `UPDATE characters SET state = 'stopped' WHERE id = ?`, c.ID); err != nil {
		t.Fatal(err)
	}
	x.a.e.srvp.Load().CloseAll(protocol.CloseShuttingDown, "the test cuts the connection")
	x.r.waitFor("the Tower to end the terminal the Citadel does not have", func() bool { return !x.aliveOnTower(c.ID) })
}

// The idle of the resync is waiting for a character with an active wait.
// Part of CA-CIDADELA-16
func TestAnIdleCharacterWithAWaitIsWaitingAfterTheResync(t *testing.T) {
	t.Parallel()
	x := newChannelRig(t)
	c := x.character(t, "c1", "Dev-1", characters.Starting)
	if err := x.startOnTower(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	x.r.waitFor("the character idle", func() bool { return x.state(t, c.ID) == characters.Idle })
	cur, err := x.orc.Registry.Get(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.orc.Waits.WaitUntil(t.Context(), cur, clock.Real{}.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := orcDB(t, x.a).Writer().ExecContext(t.Context(), `UPDATE characters SET state = 'idle' WHERE id = ?`, c.ID); err != nil {
		t.Fatal(err)
	}
	x.a.e.srvp.Load().CloseAll(protocol.CloseShuttingDown, "the test cuts the connection")
	x.r.waitFor("the resync to make it waiting", func() bool { return x.state(t, c.ID) == characters.Waiting })
}
