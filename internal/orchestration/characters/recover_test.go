package characters_test

import (
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/providers"
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
		Machine: machine, Clock: r.clk, DB: m.db, Host: m.host, Prep: m.prep, Providers: providers.NewRegistry(providers.Options{NoFlush: true}),
		Reporter: r.tap, Notifier: r.notes, Waits: r.waits,
	})
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(rt.Close)
	m.rt, r.machines[machine] = rt, rt
}

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
