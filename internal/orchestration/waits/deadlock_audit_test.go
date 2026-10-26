package waits_test

import (
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/waits"
)

// A step that gets its character later closes a ring that had no arrow when
// the waits were made: the detector looks again when the step is assigned.
func TestADeadlockThroughAStepThatGetsItsCharacterLaterIsWarned(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.gm()
	a := w.dev("42-login", "A")
	b := w.dev("42-login", "B")
	// the step has no character yet: A waits for nobody in particular
	if _, err := w.svc.WaitSteps(w.ctx, waits.StepsRequest{Waiter: a, Steps: []waits.Target{{ID: "step-b", Name: "etapa de B"}}}); err != nil {
		t.Fatal(err)
	}
	w.askAsync(waits.AskRequest{From: b, To: a, Prompt: "ajude"})
	if n := len(w.notes.of(characters.NoticeDeadlock)); n != 0 {
		t.Fatalf("%d warnings before the step had a character", n)
	}
	w.quests.mu.Lock()
	w.quests.owner["step-b"] = b.ID
	w.quests.mu.Unlock()
	w.svc.StepAssigned(w.ctx, "step-b")
	notices := w.notes.of(characters.NoticeDeadlock)
	if len(notices) != 2 {
		t.Fatalf("notices = %+v, want the table master and the users once the step has its character", notices)
	}
	// looking again changes nothing: a ring is warned once
	w.svc.Recheck(w.ctx, a.ID)
	if n := len(w.notes.of(characters.NoticeDeadlock)); n != 2 {
		t.Errorf("%d notices after Recheck, want the same ring warned once", n)
	}
}

// An ask nobody waits for any more (sent without waiting, or whose call of the
// CLI is gone) is not an arrow of a ring.
func TestAnAskThatNobodyWaitsForIsNotPartOfARing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	master := w.add(characters.RoleTableMaster, "42-login", characters.NameTableMaster)
	b := w.dev("42-login", "B", master)
	c := w.dev("42-login", "C", master)
	a := w.dev("42-login", "A", master, b, c)
	w.set(a, characters.Busy)
	if _, err := w.svc.Ask(w.ctx, waits.AskRequest{From: b, To: a, Prompt: "ajude", NoWait: true}); err != nil {
		t.Fatal(err)
	}
	gone := make(chan struct{})
	ch := w.askAsync(waits.AskRequest{From: c, To: a, Prompt: "ajude", Gone: gone})
	close(gone)
	<-ch
	for _, from := range []characters.Character{a} {
		for _, to := range []string{"B", "C"} {
			if _, err := w.svc.WaitFor(w.ctx, waits.WaitRequest{Waiter: from, Names: []string{to}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if n := len(w.notes.of(characters.NoticeDeadlock)); n != 0 {
		t.Errorf("%d deadlock warnings for asks that nobody waits for", n)
	}
}

// When the Citadel stops acting the timers of the waits stop with it: nothing
// expires from a dead engine, and Recover brings them back.
func TestTheTimersOfTheWaitsStopWhenTheCitadelStopsActing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	master := w.add(characters.RoleTableMaster, "42-login", characters.NameTableMaster)
	a := w.dev("42-login", "A", master)
	b := w.dev("42-login", "B", master, a)
	wt, err := w.svc.WaitFor(w.ctx, waits.WaitRequest{Waiter: a, Names: []string{b.Name}, Timeout: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	w.svc.StopTimers()
	w.clk.Advance(time.Hour)
	if got, _ := w.svc.Store().GetWait(w.ctx, wt.ID); got.State != waits.Active {
		t.Fatalf("the wait is %s after the timers stopped; want it untouched", got.State)
	}
	if err := w.svc.Recover(w.ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.svc.Store().GetWait(w.ctx, wt.ID); got.State != waits.Expired {
		t.Errorf("the overdue wait is %s after Recover; want expired", got.State)
	}
}
