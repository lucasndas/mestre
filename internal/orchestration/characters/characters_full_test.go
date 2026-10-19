//go:build full

package characters_test

import (
	"slices"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
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
