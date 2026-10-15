package quests_test

import (
	"database/sql"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
)

// questItems counts the items `quest` in the inbox of a character.
func (w *world) questItems(name string) int {
	w.t.Helper()
	items, err := w.orc.Inbox.Store().All(w.ctx, w.char(name).ID)
	if err != nil {
		w.t.Fatal(err)
	}
	n := 0
	for _, it := range items {
		if it.Kind == inbox.KindQuest {
			n++
		}
	}
	return n
}

// The engine can go down between the commit that makes a step run and the item
// that tells the character: the sweep that follows a restart puts the item
// again, once.
func TestARunningStepWithoutItsItemGetsItAfterARestartOnce(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.recruit("Dev Claude", "Dev Claude 1")
	q := w.run(w.create(exampleDef))
	w.wantStates(q, map[string]quests.StepState{"plan": quests.StepRunning, "dev": quests.StepPending, "review": quests.StepPending, "qa": quests.StepPending, "fix": quests.StepPending})
	if n := w.questItems("Arquiteto Claude 1"); n != 1 {
		t.Fatalf("items = %d; want 1", n)
	}
	// the crash: the item never reached the inbox
	err := w.orc.Inbox.Store().DB.WriteDurable(w.ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(w.ctx, `DELETE FROM inbox_messages WHERE kind = 'quest'`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := w.questItems("Arquiteto Claude 1"); n != 0 {
		t.Fatalf("items = %d after the crash", n)
	}
	w.restart()
	for range 2 {
		if err := w.orc.Quests.Sweep(w.ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := w.questItems("Arquiteto Claude 1"); n != 1 {
		t.Fatalf("items = %d after the sweeps; want the step handed over once", n)
	}
}
