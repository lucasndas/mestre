package quests_test

import (
	"fmt"
	"strings"
	"testing"
)

// A new round takes back what the quest checked; a quest made to correct
// another one, over the same issue, takes nothing back.
func TestANewRoundUnchecksWhatTheQuestCheckedAndACorrectionQuestUnchecksNothing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	n := w.issueOf("- [ ] C1 um\n- [ ] C2 dois\n")
	single := func(name string) string {
		return fmt.Sprintf("name: %s\nissue: %d\nsteps:\n  - {id: dev, sheet: Dev Claude}\n", name, n)
	}
	a := w.run(w.create(single("A")))
	if r := w.done("Dev Claude 1", map[string]any{"proof": []string{"C1=teste"}}, "ok"); !r.OK {
		t.Fatalf("done = %+v", r.Error)
	}
	w.machines.Report(w.ctx, w.char("Dev Claude 1").ID, "idle")
	if !strings.Contains(w.issueBody(n), "- [x] C1 um") {
		t.Fatalf("C1 is not checked:\n%s", w.issueBody(n))
	}
	// the correction quest proves C2 only; C1 stays checked
	w.run(w.create(single("B")))
	if r := w.done("Dev Claude 1", map[string]any{"proof": []string{"C2=outro"}}, "ok"); !r.OK {
		t.Fatalf("done = %+v", r.Error)
	}
	w.machines.Report(w.ctx, w.char("Dev Claude 1").ID, "idle")
	body := w.issueBody(n)
	if !strings.Contains(body, "- [x] C1 um") || !strings.Contains(body, "- [x] C2 dois") {
		t.Fatalf("the correction quest took something back:\n%s", body)
	}
	// a new round of A takes back C1, which A checked, and not C2, which B did
	if _, err := w.orc.Quests.Run(w.ctx, a.ID, ""); err != nil {
		t.Fatal(err)
	}
	body = w.issueBody(n)
	if !strings.Contains(body, "- [ ] C1 um") || !strings.Contains(body, "- [x] C2 dois") || strings.Contains(body, "teste") {
		t.Errorf("after the new round of A:\n%s", body)
	}
}
