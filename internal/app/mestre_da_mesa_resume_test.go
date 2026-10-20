package app_test

import (
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
)

// The table master restarted after the Grand Master gets the state of its table
// in the message: the missions with their state and step, the characters and
// their states, its waits and the items in its inbox; and none of the state of
// the workspace, which is the Grand Master's.
func TestTheTableMasterIsResumedAfterTheGrandMasterWithTheStateOfItsTable(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	q, dev, _ := w.startReview(3)
	w.mach.Report(w.ctx, w.gm.ID, characters.Idle)
	for _, c := range []characters.Character{w.gm, w.master} {
		if _, _, err := w.orc.Registry.Transition(w.ctx, c.ID, characters.Interrupted, characters.Change{Reason: "restart", At: w.clk.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	before := len(w.mach.Started())
	if err := w.orc.Characters.ResumeMachine(w.ctx, "cidadela"); err != nil {
		t.Fatal(err)
	}
	var order []string
	var msg string
	for _, s := range w.mach.Started()[before:] {
		order = append(order, s.Character)
		if s.Character == w.master.ID {
			msg = s.ResumeMessage
		}
	}
	if len(order) < 2 || order[0] != w.gm.ID || order[1] != w.master.ID {
		t.Fatalf("started = %v; want the Grand Master and then the table master", order)
	}
	for _, want := range []string{"State of your table " + w.table.ID, `mission "` + q.Name + `": running`, "step review", strings.TrimSpace(dev.Name), "Your waits:", "Items waiting in your inbox:"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the resumption message lacks %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "State of the workspace") {
		t.Errorf("the table master got the state of the workspace:\n%s", msg)
	}
}
