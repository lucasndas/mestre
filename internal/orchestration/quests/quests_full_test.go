//go:build full

package quests_test

import (
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
)

// CA-MIS-05: with the engine of a machine down and `qa` under way, when it is
// back, `plan` and `dev` are still completed and `qa` is delivered again with
// the note of the resumption. The Citadel goes down and comes back too, in the
// middle: the state of the quest is in the database, so the new services go on
// from the last step that ended, and the steps of the characters that stayed
// alive are not delivered again.
func TestAfterTheEngineOfAMachineFallsQaIsDeliveredAgainAndTheEndedStepsStay(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.recruit("Dev Claude", "Dev Claude 1")
	q := w.run(w.create(exampleDef))
	w.finish("Arquiteto Claude 1", "Plano pronto", map[string]string{"docs/plano.md": "# plano\n"}, "")
	w.finish("Dev Claude 1", "Login feito", map[string]string{"src/login.go": "package login\n"}, "")
	w.wantStates(q, map[string]quests.StepState{"plan": quests.StepCompleted, "dev": quests.StepCompleted, "review": quests.StepRunning, "qa": quests.StepRunning})
	qa, review := w.char("QA Antigravity 1"), w.char("Revisor Codex 1")
	if qa.Machine != "torre" || review.Machine != "cidadela" {
		t.Fatalf("machines: qa on %s, review on %s", qa.Machine, review.Machine)
	}
	questItems := func(c characters.Character) (n int, last string) {
		items, err := w.orc.Inbox.Store().All(w.ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			if it.Kind == inbox.KindQuest {
				n, last = n+1, it.Text
			}
		}
		return n, last
	}
	qaBefore, _ := questItems(qa)
	reviewBefore, _ := questItems(review)
	runs, _ := w.orc.Quests.Store().Runs(w.ctx, q.ID+"/qa")
	start := runs[0].StartCommit

	// The engine of the Tower falls: its terminals are gone. Then the Citadel
	// falls and comes back, and then the machine is back.
	if err := w.orc.Characters.Reconcile(w.ctx, "torre", nil); err != nil {
		t.Fatal(err)
	}
	if w.get(qa.ID).State != characters.Interrupted || w.get(review.ID).State == characters.Interrupted {
		t.Fatalf("states: qa %s, review %s", w.get(qa.ID).State, w.get(review.ID).State)
	}
	w.restart()
	w.wantStates(q, map[string]quests.StepState{"plan": quests.StepCompleted, "dev": quests.StepCompleted, "review": quests.StepRunning, "qa": quests.StepPreparing})
	if err := w.orc.Characters.ResumeMachine(w.ctx, "torre"); err != nil {
		t.Fatal(err)
	}

	w.wantStates(q, map[string]quests.StepState{"plan": quests.StepCompleted, "dev": quests.StepCompleted, "review": quests.StepRunning, "qa": quests.StepRunning})
	n, last := questItems(qa)
	if n != qaBefore+1 || !strings.Contains(last, "retomada após reinício") || !strings.Contains(last, "Etapa \"qa\"") {
		t.Errorf("qa was not delivered again with the note (%d items, was %d): %s", n, qaBefore, last)
	}
	if n, _ := questItems(review); n != reviewBefore {
		t.Errorf("the step of the character that stayed alive was delivered again (%d items, was %d)", n, reviewBefore)
	}
	runs, _ = w.orc.Quests.Store().Runs(w.ctx, q.ID+"/qa")
	if len(runs) != 1 || runs[0].StartCommit != start {
		t.Errorf("the delivery again changed the run: %+v", runs)
	}

	// It goes on to the end with the new services.
	w.finish("Revisor Codex 1", "Aprovado", nil, "")
	w.finish("QA Antigravity 1", "Testes ok", map[string]string{"tests/e2e.txt": "ok\n"}, "")
	w.finish("Dev Claude 1", "Correções", nil, "")
	if got := w.quest(q.ID); got.State != quests.Completed {
		t.Errorf("the quest is %s; want completed", got.State)
	}
	text := w.scroll(q)
	for _, want := range []string{"1. plan", "2. dev", "3. review", "4. qa", "5. fix"} {
		if !strings.Contains(text, want) {
			t.Errorf("the scroll lacks %q", want)
		}
	}
}
