package quests_test

import (
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/orchestration/quests"
	"github.com/mestre-ia/mestre/internal/orchestration/waits"
)

// `mestre wait --quest` waits for all the steps of a quest: the quests tell the
// waits when each step ends, the character responsible for a step is where an
// arrow of the graph of waits points to, and the wait is cancelled with the
// quest.
func TestAWaitForAQuestIsSatisfiedByItsStepsAndCancelledWithIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	q := w.run(w.create(reviewDef))

	r := w.call(w.master.ID, "wait", map[string]any{"quest": "Revisão"})
	if !r.OK {
		t.Fatalf("wait --quest = %+v", r.Error)
	}
	if r := w.call(w.master.ID, "wait", map[string]any{"quest": "Não existe"}); r.OK || r.ExitCode() != 4 {
		t.Errorf("wait for a quest that does not exist = %+v", r)
	}
	active, err := w.orc.Waits.Store().ActiveOf(w.ctx, w.master.ID)
	if err != nil || len(active) != 1 || active[0].Kind != waits.KindSteps || active[0].Quest != q.ID || len(active[0].Targets) != 2 {
		t.Fatalf("the wait: %+v, %v", active, err)
	}
	// The Master waits for the character that has the step now.
	dev := w.char("Dev Claude 1")
	if who, err := w.orc.Quests.Responsible(w.ctx, q.ID+"/dev"); err != nil || who != dev.ID {
		t.Errorf("responsible for dev = %q, %v", who, err)
	}
	if who, _ := w.orc.Quests.Responsible(w.ctx, q.ID+"/review"); who != "" {
		t.Errorf("responsible for a step that has not begun = %q", who)
	}

	w.finish("Dev Claude 1", "feito", nil, "")
	w.finish("Revisor Codex 1", "ok", nil, "")
	if active, _ := w.orc.Waits.Store().ActiveOf(w.ctx, w.master.ID); len(active) != 0 {
		t.Errorf("the wait stayed: %+v", active)
	}
	items, _ := w.orc.Inbox.Store().All(w.ctx, w.master.ID)
	var woke, completed bool
	for _, it := range items {
		woke = woke || it.Kind == inbox.KindWake && strings.Contains(it.Text, "feito") && strings.Contains(it.Text, "ok")
		completed = completed || it.Kind == inbox.KindQuest && strings.Contains(it.Text, "foi concluída") && it.Refs.Quest == q.ID
	}
	if !woke {
		t.Errorf("the Master was not woken with the results: %+v", items)
	}
	if !completed {
		t.Errorf("the Master did not get the item `quest` that the quest was completed: %+v", items)
	}

	// Cancelled with the quest: another round, a wait, and the quest is cancelled.
	q2 := w.run(w.create("name: Outra\nsteps:\n  - {id: a, sheet: Dev Claude}\n"))
	if r := w.call(w.master.ID, "wait", map[string]any{"quest": "Outra"}); !r.OK {
		t.Fatalf("wait --quest = %+v", r.Error)
	}
	if _, err := w.orc.Quests.Cancel(w.ctx, q2.ID, "chega"); err != nil {
		t.Fatal(err)
	}
	if active, _ := w.orc.Waits.Store().ActiveOf(w.ctx, w.master.ID); len(active) != 0 {
		t.Errorf("the wait of a cancelled quest stayed: %+v", active)
	}
}

// A quest run again after it completed is a new round: the steps go back to
// pending, the scroll gets a section per round, and the characters are made
// again.
func TestARoundOfACompletedQuestStartsAgainWithASectionPerRound(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	v := w.create(oneStep)
	q := w.run(v)
	w.finish("Dev Claude 1", "a1", nil, "")
	w.finish("Revisor Codex 1", "b1", nil, "")
	if got := w.quest(q.ID); got.State != quests.Completed || got.Round != 1 {
		t.Fatalf("round 1: %s, %d", got.State, got.Round)
	}
	if _, err := w.orc.Quests.Run(w.ctx, q.ID, ""); err != nil {
		t.Fatalf("run again: %v", err)
	}
	if got := w.quest(q.ID); got.State != quests.Running || got.Round != 2 {
		t.Fatalf("round 2: %s, %d", got.State, got.Round)
	}
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepRunning, "b": quests.StepPending})
	w.finish("Dev Claude 1", "a2", nil, "")
	w.finish("Revisor Codex 1", "b2", nil, "")
	text := w.scroll(q)
	for _, want := range []string{"## Rodada 1 · 1. a", "**Resumo:** a1", "## Rodada 1 · 2. b", "## Rodada 2 · iniciada", "## Rodada 2 · 1. a", "**Resumo:** a2", "## Rodada 2 · 2. b", "**Resumo:** b2"} {
		if !strings.Contains(text, want) {
			t.Errorf("the scroll lacks %q:\n%s", want, text)
		}
	}
	if _, err := w.orc.Quests.Run(w.ctx, q.ID, ""); err != nil {
		t.Fatalf("a third round: %v", err)
	}
	if _, err := w.orc.Quests.Run(w.ctx, q.ID, ""); err == nil {
		t.Error("a running quest ran again")
	}
}
