//go:build full

package characters_test

import (
	"slices"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/providers"
)

// The tests below are not parallel: they set PATH (the fake agent is the
// `claude` of the PATH) and each starts real processes.

// CA-DUBLES-11
func TestTheEngineCreatesACharacterOnARealPTYAndTheHooksOfTheFakeAgentCallTheRealMestreHook(t *testing.T) { //nolint:paralleltest // it sets PATH and starts real processes
	f := newFullRig(t)
	c := f.create("Dev Claude", "Dev") // idle: the SessionStart of the agent reached the engine through `mestre hook`
	log, err := f.reg.Log(f.ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if last := log[len(log)-1]; last.To != characters.Idle || last.Reason != "session_start" {
		t.Fatalf("last transition = %s (%s), want idle by the session_start hook", last.To, last.Reason)
	}
	rec, err := f.eng.mgr.Record(f.ctx, c.ID)
	if err != nil || rec.PTY.PID == 0 || rec.Status != string(characters.Idle) {
		t.Fatalf("terminal record = %+v, %v; want a real mestre-pty and the state of the character", rec, err)
	}

	// A turn: the engine types the message, and the hooks of the turn come back.
	if err := f.eng.rt.Deliver(f.ctx, c.ID, "m1", "faça o login"); err != nil {
		t.Fatal(err)
	}
	f.watch.until(t, "a turn that began and ended", func() bool {
		return f.state(c) == characters.Idle && slices.Contains(f.log(c), characters.Busy)
	})
	screen, err := f.eng.rt.Screen(f.ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !containsText(screen.Text, "faça o login") || !containsText(screen.Text, "feito") {
		t.Errorf("screen of the real terminal:\n%s", screen.Text)
	}
}

// Part of CA-PERS-09: the same with a real terminal and the fake agent (the fast suite has the criterion).
func TestARealScreenNoAdapterKnowsBlocksTheCharacterOnInputAndARawKeyBringsItBack(t *testing.T) { //nolint:paralleltest // it sets PATH and starts real processes
	f := newFullRig(t)
	f.startScreens["Curioso"] = "Uma tela que nenhum adaptador conhece"
	res, err := f.svc.Create(f.ctx, characters.CreateRequest{Name: "Curioso", Sheet: "Dev Claude", Table: "3-login", Workspace: workspace, Creator: engine})
	if err != nil {
		t.Fatal(err)
	}
	c := res.Character
	f.waitState(c, characters.Blocked) // the screen stayed still, and the terminal was read
	if got := f.get(c); got.Reason != characters.ReasonInput {
		t.Fatalf("reason = %q, want input", got.Reason)
	}
	ns := f.waitNotice(characters.NoticeBlockedInput) // sent after the report of the state
	if len(ns) != 1 || !containsText(ns[0].Screen, "Uma tela que nenhum adaptador conhece") {
		t.Fatalf("the table master was not told with the screen: %+v", ns)
	}
	if err := f.eng.rt.Input(f.ctx, c.ID, []byte("\r")); err != nil { // `ask --raw`
		t.Fatal(err)
	}
	f.waitState(c, characters.Idle) // the agent went on and its SessionStart came
}

func containsText(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// CA-PERS-07
func TestAfterTheEngineIsKilledTheBusyAndTheWaitingKeepTheirStatesAndTheOneWhoseMestrePtyDiedIsInterruptedAndResumedWithItsSession(t *testing.T) { //nolint:paralleltest // it sets PATH and starts real processes
	f := newFullRig(t)
	busy := f.create("Dev Claude", "Preso")
	waiting := f.create("Dev Claude", "Espera")
	victim := f.create("Dev Persistente", "Vitima")
	rt := f.eng.rt

	// One is busy with a turn that does not end; the other ends its turn with
	// a wait for another character; both have things in the inbox and waits.
	if err := rt.Deliver(f.ctx, busy.ID, "m1", "trabalho longo"); err != nil {
		t.Fatal(err)
	}
	f.waitState(busy, characters.Busy)
	f.waits.set(waiting.ID, "Revisor")
	f.inbox.set(waiting.ID, 2)
	if err := rt.Deliver(f.ctx, waiting.ID, "m2", "pergunte ao revisor"); err != nil {
		t.Fatal(err)
	}
	f.waitState(waiting, characters.Waiting)
	// The one that will be interrupted has its own inbox and wait too.
	f.waits.set(victim.ID, "Outro")
	f.inbox.set(victim.ID, 3)
	pids := map[string]int{}
	for _, c := range []characters.Character{busy, waiting, victim} {
		rec, err := f.eng.mgr.Record(f.ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		pids[c.ID] = rec.PTY.PID
	}
	session := f.get(victim).ProviderSessionID
	if session == "" {
		t.Fatal("the character has no saved session")
	}

	// The engine dies; meanwhile the mestre-pty of one of them is killed.
	f.eng.stop()
	rec, err := f.eng.mgr.Record(f.ctx, victim.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.sys.KillTree(f.ctx, rec.PTY, nil); err != nil {
		t.Fatal(err)
	}
	e2 := f.startEngine()
	report := e2.recovered
	got := append([]string(nil), report.Reconnected...)
	slices.Sort(got)
	if want := []string{busy.ID, waiting.ID}; !slices.Equal(got, sortedIDs(want)) || !slices.Equal(report.Interrupted, []string{victim.ID}) {
		t.Fatalf("recover = %+v", report)
	}

	// The two survivors are as they were, without passing through interrupted.
	for c, want := range map[string]characters.State{busy.ID: characters.Busy, waiting.ID: characters.Waiting} {
		reg, err := f.svc.Get(f.ctx, c)
		if err != nil || reg.State != want {
			t.Errorf("%s = %v (%v), want %s", c, reg.State, err, want)
		}
		rec, err := f.eng.mgr.Record(f.ctx, c)
		if err != nil || rec.PTY.PID != pids[c] {
			t.Errorf("%s: the mestre-pty changed: %+v (%v), pid was %d", c, rec.PTY, err, pids[c])
		}
	}
	for _, c := range []characters.Character{busy, waiting} {
		if contains(f.log(c), characters.Interrupted) {
			t.Errorf("%s passed through interrupted: %v", c.Name, f.log(c))
		}
	}
	f.waitState(victim, characters.Interrupted)

	// The survivors are supervised again: a message to the waiting one runs a
	// turn whose hooks reach the new engine (its wait is gone: it ends idle).
	f.waits.set(waiting.ID, "")
	if err := e2.rt.Deliver(f.ctx, waiting.ID, "m3", "outra pergunta"); err != nil {
		t.Fatal(err)
	}
	f.watch.until(t, "the turn after the restart", func() bool { return f.state(waiting) == characters.Idle })

	// The Citadel resumes the dead one with the session it saved.
	if err := f.svc.ResumeMachine(f.ctx, "m1"); err != nil {
		t.Fatal(err)
	}
	f.waitState(victim, characters.Idle)
	f.prep.mu.Lock()
	last := f.prep.launched[len(f.prep.launched)-1]
	f.prep.mu.Unlock()
	if !last.Resumed || !slices.Contains(last.Args, session) || !slices.Contains(last.Args, "--resume") {
		t.Errorf("the restart command = %v, want --resume %s", last.Args, session)
	}
	if again := f.get(victim); again.ProviderSessionID != session || again.TerminalID != victim.ID+"-2" {
		t.Errorf("after the resumption: session %q, terminal %q", again.ProviderSessionID, again.TerminalID)
	}
	if n, _ := f.inbox.Pending(f.ctx, waiting.ID); n != 2 {
		t.Errorf("the inbox of the waiting character: %d, want 2 kept", n)
	}
	// The resumed character keeps its own inbox and waits.
	if n, _ := f.inbox.Pending(f.ctx, victim.ID); n != 3 {
		t.Errorf("the inbox of the resumed character: %d, want 3 kept", n)
	}
	if reason, ok, _ := f.waits.Active(f.ctx, victim.ID); !ok || reason != "Outro" {
		t.Errorf("the wait of the resumed character = %q (%v), want kept", reason, ok)
	}
}

func sortedIDs(s []string) []string { slices.Sort(s); return s }

func (f *fullRig) get(c characters.Character) characters.Character {
	f.t.Helper()
	got, err := f.svc.Get(f.ctx, c.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return got
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
