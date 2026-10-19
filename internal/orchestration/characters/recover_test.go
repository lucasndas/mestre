package characters_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/providers"
)

// restart is what happens to the engine of a machine that goes down and
// comes back: a new runtime on the same machine database, which finds the
// terminals that survived (reconnected) and the ones whose mestre-pty died.
func (r *rig) restart(machine string, alive []characters.Character, dead []characters.Character) {
	r.t.Helper()
	m := r.mach[machine]
	var rep characters.HostReport
	for _, c := range alive {
		rep.Reconnected = append(rep.Reconnected, m.host.proc(c.ID))
	}
	for _, c := range dead {
		rep.Interrupted = append(rep.Interrupted, m.host.proc(c.ID).id)
	}
	m.host.mu.Lock()
	m.host.report = rep
	m.host.mu.Unlock()
	m.rt.Close()
	rt, err := characters.NewRuntime(characters.RuntimeOptions{
		Machine: machine, Clock: r.clk, DB: m.db, Host: m.host, Prep: m.prep, Providers: providers.NewRegistry(providers.Options{}),
		Reporter: r.tap, Notifier: r.notes, Waits: r.waits,
	})
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(rt.Close)
	m.rt, r.machines[machine] = rt, rt
}

// Part of CA-PERS-07 and of CA-CIDADELA-13: the full suite does the same with a
// real mestre-pty and the fake agent (the waits and the outbox of the
// criterion of the Guild come with the stages that own them).
func TestAfterTheRestartOfTheEngineTheSurvivorsKeepTheirStateAndTheDeadOnesAreInterruptedAndResumed(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	gm := r.makeGM()
	tm := r.makeTM("3-login")
	busy := r.make("Dev Claude", "Ocupado", "3-login")
	waiting := r.make("Dev Claude", "Espera", "3-login")
	task := r.make("Dev Claude", "Com tarefa", "3-login")
	persistent := r.make("Dev Persistente", "Persistente", "3-login")
	ephemeral := r.make("Dev Claude", "Efemero", "3-login")
	shell := mustCreate(t, r, characters.CreateRequest{Name: "srv", Base: "bash", Table: "3-login"})
	for _, c := range []characters.Character{gm, tm, busy, waiting, task, persistent, ephemeral} {
		r.ready(c)
	}
	r.stable(shell, "user@host:~$ ", 0)
	r.busy(busy)
	r.busy(waiting)
	r.waits.set(waiting.ID, "Revisor")
	r.hook(waiting, providers.EventTurnEnd)
	r.waits.set(waiting.ID, "")
	if err := r.svc.TaskAssigned(r.ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	r.busy(task)
	lastBusy := r.get(busy.ID)
	machine := busy.Machine
	prep := r.mach[machine].prep

	// The engine goes down; the terminals of "busy" and "waiting" survive.
	r.clk.Advance(10 * time.Minute)
	r.restart(machine, []characters.Character{busy, waiting}, []characters.Character{gm, tm, task, persistent, ephemeral, shell})
	prep.mu.Lock()
	prep.prepared = nil
	prep.mu.Unlock()
	rep, err := r.mach[machine].rt.Recover(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(rep.Reconnected)
	if want := []string{busy.ID, waiting.ID}; !slices.Equal(rep.Reconnected, sorted(want)) || len(rep.Interrupted) != 6 {
		t.Fatalf("recover = %+v", rep)
	}
	if got := r.get(busy.ID); got.State != characters.Busy || got.StoppedAt != lastBusy.StoppedAt {
		t.Errorf("the busy survivor = %s, want busy", got.State)
	}
	if got := r.get(waiting.ID); got.State != characters.Waiting || got.Reason != "Revisor" {
		t.Errorf("the waiting survivor = %s (%s), want waiting (Revisor)", got.State, got.Reason)
	}
	for _, c := range []characters.Character{busy, waiting} {
		for _, l := range mustLog(t, r, c) {
			if l.To == characters.Interrupted {
				t.Errorf("%s passed through interrupted", c.Name)
			}
		}
	}
	// The 15 minutes continue from the last signal, before the restart.
	r.clk.Advance(4*time.Minute + 59*time.Second)
	if r.state(busy) != characters.Busy {
		t.Fatalf("5 minutes before the last signal + 10 + 4:59: %s", r.state(busy))
	}
	r.clk.Advance(time.Second)
	if r.state(busy) != characters.Stuck {
		t.Errorf("15 minutes after its last signal: %s, want stuck", r.state(busy))
	}
	for _, c := range []characters.Character{gm, tm, task, persistent, ephemeral, shell} {
		if r.state(c) != characters.Interrupted {
			t.Errorf("%s = %s, want interrupted (its mestre-pty died)", c.Name, r.state(c))
		}
	}

	// The resumption: the order, the sessions and what is not resumed.
	if err := r.svc.ResumeMachine(r.ctx, machine); err != nil {
		t.Fatal(err)
	}
	prep.mu.Lock()
	var order []string
	for _, p := range prep.prepared {
		order = append(order, p.Name)
		if p.ResumeSessionID == "" && p.ID != gm.ID && p.ID != tm.ID {
			t.Errorf("%s was not resumed with its session", p.Name)
		}
	}
	prep.mu.Unlock()
	if want := []string{"Grão-Mestre", "Mestre", "Com tarefa", "Persistente"}; !slices.Equal(order, want) {
		t.Errorf("resumption order = %v, want %v", order, want)
	}
	for _, c := range []characters.Character{gm, tm, task, persistent} {
		if got := r.state(c); got != characters.Starting {
			t.Errorf("%s = %s after the resumption, want starting", c.Name, got)
		}
	}
	if got := r.get(ephemeral.ID); got.State != characters.Stopped {
		t.Errorf("an ephemeral with no task: %s, want stopped", got.State)
	}
	if got := r.path(ephemeral); got[len(got)-1] != characters.Stopped {
		t.Errorf("states = %v", got)
	}
	if got := r.state(shell); got != characters.Interrupted {
		t.Errorf("a simple terminal: %s, want interrupted (the engine does not run commands again)", got)
	}
	if ns := r.notes.of(characters.NoticeNotResumed); len(ns) != 1 || ns[0].Character != shell.ID {
		t.Errorf("the creator of the terminal: %+v", ns)
	}
	// The resumed one got a new terminal and kept its session.
	if in := r.builder.input(task.ID); !in.Resumed || in.ResumeSessionID != r.get(task.ID).ProviderSessionID || in.ResumeSessionID == "" {
		t.Errorf("build input of the resumption = %+v", in)
	}
	if got := r.get(task.ID).TerminalID; got != task.ID+"-2" {
		t.Errorf("terminal = %q, want the second of the character", got)
	}
}

func sorted(s []string) []string { slices.Sort(s); return s }

func TestAMachineThatIsAwayLeavesItsCharactersInterruptedAndTheTableMasterToldUntilItIsBack(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	tm := r.makeTM("3-login")
	c := r.make("Dev Persistente", "Dev", "3-login")
	r.ready(tm)
	r.ready(c)
	r.mach[c.Machine].host.emit(characters.HostEvent{Terminal: c.ID, Kind: characters.HostExit})
	delete(r.machines, c.Machine) // the machine is out
	if err := r.svc.ResumeMachine(r.ctx, c.Machine); err == nil {
		t.Fatal("no error for a machine that cannot be reached")
	}
	if r.state(c) != characters.Interrupted {
		t.Errorf("state = %s, want interrupted (visible)", r.state(c))
	}
	if ns := r.notes.of(characters.NoticeMachineDown); len(ns) != 1 || ns[0].Character != c.ID {
		t.Errorf("notices = %+v", ns)
	}
	r.machines[c.Machine] = r.mach[c.Machine].rt // it is back
	if err := r.svc.ResumeMachine(r.ctx, c.Machine); err != nil {
		t.Fatal(err)
	}
	if r.state(c) != characters.Starting {
		t.Errorf("state = %s, want starting", r.state(c))
	}
}

func TestACharacterWithoutASessionRestartsFromScratchWithAMessageThatSaysWhereItWas(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{first: "THE LAYER", machines: []machineSpec{withCodex(linux("linux"))}})
	res, err := r.create(characters.CreateRequest{Name: "Dev", Sheet: "Revisor Codex", Table: "3-login", Quest: "q1", Step: "s-qa"})
	if err != nil {
		t.Fatal(err)
	}
	c := res.Character
	// Codex tells its session in the hooks; this one never did.
	ready := event(c, providers.EventSessionStart)
	ready.SessionID = ""
	r.mach[c.Machine].rt.OnHook(ready, hook.Answer{})
	if err := r.svc.Done(r.ctx, c.ID, "terminei a tela de login"); err != nil {
		t.Fatal(err)
	}
	r.mach[c.Machine].host.emit(characters.HostEvent{Terminal: c.ID, Kind: characters.HostExit})
	if err := r.svc.ResumeMachine(r.ctx, c.Machine); err != nil {
		t.Fatal(err)
	}
	ready.Provider = providers.Codex
	r.mach[c.Machine].rt.OnHook(ready, hook.Answer{}) // the new process is ready
	typed := r.mach[c.Machine].host.proc(c.ID).typedAll()
	if len(typed) != 1 || !strings.Contains(typed[0], "THE LAYER") || !strings.Contains(typed[0], "terminei a tela de login") || !strings.Contains(typed[0], "could not be resumed") ||
		!strings.Contains(typed[0], "s-qa") || !strings.Contains(typed[0], "Missão: ") {
		t.Errorf("typed = %q, want the layer and the resumption message together", typed)
	}
}

func TestTheCitadelReconcilesWithWhatAMachineSaysIsAlive(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	a := r.make("Dev Claude", "A", "3-login")
	b := r.make("Dev Claude", "B", "3-login")
	c := r.make("Dev Claude", "C", "3-login")
	for _, x := range []characters.Character{a, b, c} {
		r.ready(x)
	}
	// B is busy on the machine, which the Citadel did not hear of; C is gone.
	live := []protocol.TerminalState{
		{TerminalID: a.ID, AgentID: a.ID, State: "idle"},
		{TerminalID: b.ID, AgentID: b.ID, State: "busy", SessionID: "s-b"},
	}
	if err := r.svc.Reconcile(r.ctx, a.Machine, live); err != nil {
		t.Fatal(err)
	}
	if r.state(a) != characters.Idle || r.state(b) != characters.Busy || r.state(c) != characters.Interrupted {
		t.Errorf("states = %s %s %s", r.state(a), r.state(b), r.state(c))
	}
	if r.get(b.ID).ProviderSessionID != "s-b" {
		t.Errorf("session = %q", r.get(b.ID).ProviderSessionID)
	}
}

func TestTheGrandMasterViewTellsTheWorkspacesWhetherTheMainCloneIsStill(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	view := characters.GrandMasterView{S: r.svc}
	state := func() string {
		s, err := view.State(r.ctx, workspace)
		if err != nil {
			t.Fatal(err)
		}
		return string(s)
	}
	if got := state(); got != "" {
		t.Fatalf("no Grand Master: %q", got)
	}
	gm := r.makeGM()
	if got := state(); got != "busy" { // starting counts as working
		t.Errorf("starting: %q", got)
	}
	r.ready(gm)
	if got := state(); got != "idle" {
		t.Errorf("idle: %q", got)
	}
	r.busy(gm)
	if got := state(); got != "busy" {
		t.Errorf("busy: %q", got)
	}
	if err := view.NotifySystem(r.ctx, workspace, "o clone principal mudou"); err != nil {
		t.Fatal(err)
	}
	if ns := r.notes.of(characters.NoticeSystem); len(ns) != 1 || ns[0].To.Kind != characters.ToGrandMaster {
		t.Errorf("notices = %+v", ns)
	}
}
