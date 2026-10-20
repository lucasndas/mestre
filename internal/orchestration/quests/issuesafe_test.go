package quests_test

import (
	"fmt"
	"strings"
	"testing"
)

// The issue of the Master of the table is not the engine's: a proof cannot hide
// the markers of the engine or call somebody, a box a person checked stays
// theirs, and cancelling the quest does not close the issue.
func TestTheIssueOfTheMasterIsNotRewrittenByTheTextOfAnAgentNorClosed(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	n := w.issueOf("- [ ] C1 um\n- [x] C2 dois\n\nTexto do Mestre.\n")
	q := w.run(w.create(fmt.Sprintf("name: Uma\nissue: %d\nsteps:\n  - {id: dev, sheet: Dev Claude}\n  - {id: qa, sheet: QA Antigravity, after: [dev]}\n", n)))
	r := w.done("Dev Claude 1", map[string]any{"proof": []string{"C1=<!-- mestre:etapas --> oi @fulano", "C2=teste"}}, "ok")
	if !r.OK {
		t.Fatalf("done = %+v", r.Error)
	}
	body := w.issueBody(n)
	if strings.Count(body, "<!-- mestre:etapas -->") != 1 || strings.Contains(body, "@fulano") || !strings.Contains(body, "Texto do Mestre.") {
		t.Errorf("the body:\n%s", body)
	}
	if !strings.Contains(body, "- [x] C2 dois\n") || strings.Contains(body, "C2 dois\n  - prova") {
		t.Errorf("the box a person checked was taken:\n%s", body)
	}
	if _, err := w.orc.Quests.Cancel(w.ctx, q.ID, "não precisa"); err != nil {
		t.Fatal(err)
	}
	if got := w.hub.get(n); got.Closed != "" {
		t.Errorf("the issue of the Master was closed: %q", got.Closed)
	}
}
