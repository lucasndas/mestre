package quests_test

import (
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/orchestration/quests"
)

const reviewDef = `
name: Revisão
max_returns: 3
steps:
  - {id: dev,    sheet: Dev Claude, instructions: "Implemente"}
  - {id: review, sheet: Revisor Codex, after: [dev], can_return_to: dev}
`

// A return where the step has none, and a summary that is too long, are
// refused before anything is registered.
func TestADoneThatCannotReturnIsRefusedAndTheStepKeepsRunning(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	q := w.run(w.create(reviewDef))
	dev := w.char("Dev Claude 1")
	r := w.call(dev.ID, "done", map[string]any{"return": "não"}, "feito")
	if r.OK || r.Error.Code != "no_return_step" || r.ExitCode() != localchan.ExitConflict {
		t.Fatalf("a return from a step that has none = %+v", r)
	}
	r = w.call(dev.ID, "done", nil, strings.Repeat("x", quests.MaxSummary+1))
	if r.OK || r.ExitCode() != localchan.ExitLimit {
		t.Fatalf("a summary over the limit = %+v", r)
	}
	w.wantStates(q, map[string]quests.StepState{"dev": quests.StepRunning})
	if dones, _ := w.orc.Registry.Dones(w.ctx, dev.ID); len(dones) != 0 {
		t.Errorf("a refused done was registered: %+v", dones)
	}
}

// `join: any`: the step starts with the first of its previous steps; the other
// ends normally, and its result is discarded.
func TestAJoinAnyStartsWithTheFirstAndDiscardsTheOthersResult(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	q := w.run(w.create(`
name: Corrida
steps:
  - {id: a, sheet: Dev Claude}
  - {id: b, sheet: Revisor Codex}
  - {id: c, sheet: QA Antigravity, after: [a, b], join: any}
`))
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepRunning, "b": quests.StepRunning, "c": quests.StepPending})
	w.finish("Revisor Codex 1", "b chegou primeiro", nil, "")
	w.wantStates(q, map[string]quests.StepState{"b": quests.StepCompleted, "c": quests.StepRunning})
	// a is not interrupted; when it ends its result is discarded.
	res := w.finish("Dev Claude 1", "a chegou tarde", nil, "")
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepDiscarded, "c": quests.StepRunning})
	if res.QuestState != "running" {
		t.Errorf("quest state after the discarded one = %s", res.QuestState)
	}
	runs, _ := w.orc.Quests.Store().Runs(w.ctx, q.ID+"/a")
	if len(runs) != 1 || runs[0].Outcome != quests.OutcomeDiscarded {
		t.Errorf("runs of a: %+v", runs)
	}
	if item := w.itemOf("QA Antigravity 1"); !strings.Contains(item, "b chegou primeiro") || strings.Contains(item, "a chegou tarde") {
		t.Errorf("c got:\n%s", item)
	}
	w.finish("QA Antigravity 1", "c ok", nil, "")
	if got := w.quest(q.ID); got.State != quests.Completed {
		t.Errorf("the quest is %s; a discarded step counts as finished", got.State)
	}
}

// A return makes what depends on the target start again; a step that still
// runs ends, and its result is discarded.
func TestAReturnSendsTheDependentsBackAndDiscardsWhatStillRuns(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	q := w.run(w.create(`
name: Paralelo
steps:
  - {id: dev,    sheet: Dev Claude}
  - {id: review, sheet: Revisor Codex, after: [dev], can_return_to: dev}
  - {id: qa,     sheet: QA Antigravity, after: [dev]}
`))
	w.finish("Dev Claude 1", "v1", nil, "")
	w.wantStates(q, map[string]quests.StepState{"review": quests.StepRunning, "qa": quests.StepRunning})
	w.finish("Revisor Codex 1", "volta", nil, "erro na validação")
	// qa still runs on the old version: it is not interrupted
	w.wantStates(q, map[string]quests.StepState{"dev": quests.StepRunning, "review": quests.StepPending, "qa": quests.StepRunning})
	if !w.step(q, "qa").DiscardResult {
		t.Error("qa was not marked to have its result discarded")
	}
	w.finish("QA Antigravity 1", "qa da v1", nil, "")
	w.wantStates(q, map[string]quests.StepState{"qa": quests.StepPending})
	runs, _ := w.orc.Quests.Store().Runs(w.ctx, q.ID+"/qa")
	if len(runs) != 1 || runs[0].Outcome != quests.OutcomeDiscarded {
		t.Errorf("the result of qa on the old version: %+v", runs)
	}
	// dev ends again: both start again, and qa runs a second time with a new run
	w.finish("Dev Claude 1", "v2", nil, "")
	w.wantStates(q, map[string]quests.StepState{"review": quests.StepRunning, "qa": quests.StepRunning})
	runs, _ = w.orc.Quests.Store().Runs(w.ctx, q.ID+"/qa")
	if len(runs) != 2 || runs[1].Attempt != 2 {
		t.Errorf("runs of qa: %+v", runs)
	}
}
