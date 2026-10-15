package quests_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
)

// The commands of the quests answer through the forwarder of the Citadel, the
// "casca" of stage 9: the Master of the table makes, runs, pauses, resumes and
// cancels, the Grand Master only reads, and the others are refused.
func TestTheQuestCommandsAnswerThroughTheForwarderAndOnlyTheMasterConducts(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev Claude", "Dev Claude 1")

	r := w.call(w.master.ID, "quest create", map[string]any{"definition": reviewDef}, "Revisão")
	if !r.OK {
		t.Fatalf("quest create = %+v", r.Error)
	}
	var made struct {
		Name, ID, Status string
		Issue            int
		Steps            []struct{ ID, Status string }
	}
	if err := json.Unmarshal(r.Data, &made); err != nil {
		t.Fatal(err)
	}
	if made.Name != "Revisão" || made.Status != "draft" || len(made.Steps) != 2 || made.Steps[0].Status != "pending" || made.Issue == 0 {
		t.Errorf("created: %+v", made)
	}
	// the same name again, a definition with a mistake, a definition missing
	if r := w.call(w.master.ID, "quest create", map[string]any{"definition": reviewDef}, "Revisão"); r.OK || r.Error.Code != "name_taken" || r.ExitCode() != localchan.ExitConflict {
		t.Errorf("a repeated name = %+v", r)
	}
	if r := w.call(w.master.ID, "quest create", map[string]any{"definition": "name: Ruim\nsteps:\n  - {id: a, sheet: Dev Claude, after: [b]}\n"}, "Ruim"); r.OK ||
		r.Error.Code != "invalid_quest" || !strings.Contains(r.Error.Message, `"b"`) || r.ExitCode() != localchan.ExitUsage {
		t.Errorf("an invalid definition = %+v", r)
	}
	if r := w.call(w.master.ID, "quest create", nil, "Vazia"); r.OK || r.ExitCode() != localchan.ExitUsage {
		t.Errorf("no definition = %+v", r)
	}
	// Who: the characters and the Grand Master do not conduct a quest.
	for _, who := range []string{dev.ID, w.gm.ID} {
		if r := w.call(who, "quest run", nil, "Revisão"); r.OK || r.Error.Code != "level_denied" {
			t.Errorf("quest run by %s = %+v", who, r)
		}
	}

	r = w.call(w.master.ID, "quest run", map[string]any{"input": "faça rápido"}, "Revisão")
	var run struct {
		Status string
		Round  int
	}
	if !r.OK || json.Unmarshal(r.Data, &run) != nil || run.Status != "running" || run.Round != 1 {
		t.Fatalf("quest run = %+v %s", r.Error, r.Data)
	}
	if r := w.call(w.master.ID, "quest run", nil, "Revisão"); r.OK || r.Error.Code != "invalid_state" || r.ExitCode() != localchan.ExitConflict {
		t.Errorf("run of a running quest = %+v", r)
	}
	if r := w.call(w.master.ID, "quest run", nil, "Não existe"); r.OK || r.ExitCode() != localchan.ExitNotFound {
		t.Errorf("run of a quest that does not exist = %+v", r)
	}
	if item := w.itemOf("Dev Claude 1"); !strings.Contains(item, "faça rápido") {
		t.Errorf("the input of the run did not reach the first step:\n%s", item)
	}

	// status: the Master by name; the Grand Master with --table.
	r = w.call(w.master.ID, "quest status", nil, "Revisão")
	var st struct {
		Status string
		Steps  []struct {
			ID, Status, Character string
		}
	}
	if !r.OK || json.Unmarshal(r.Data, &st) != nil || st.Status != "running" || len(st.Steps) != 2 || st.Steps[0].Status != "running" || st.Steps[0].Character != dev.ID {
		t.Fatalf("quest status = %+v %s", r.Error, r.Data)
	}
	if r := w.call(w.gm.ID, "quest status", nil, "Revisão"); r.OK || r.Error.Code != "table_required" {
		t.Errorf("the Grand Master without --table = %+v", r)
	}
	if r := w.call(w.gm.ID, "quest status", map[string]any{"table": w.table.ID}); !r.OK || !strings.Contains(string(r.Data), "Revisão") {
		t.Errorf("the Grand Master lists = %+v %s", r.Error, r.Data)
	}

	for _, cmd := range []string{"quest pause", "quest resume"} {
		if r := w.call(w.master.ID, cmd, nil, "Revisão"); !r.OK {
			t.Errorf("%s = %+v", cmd, r.Error)
		}
	}
	if r := w.call(w.master.ID, "quest cancel", map[string]any{"reason": "chega"}, "Revisão"); !r.OK || !strings.Contains(string(r.Data), "cancelled") {
		t.Errorf("quest cancel = %+v %s", r.Error, r.Data)
	}
	if r := w.call(w.master.ID, "quest cancel", nil, "Revisão"); r.OK || r.Error.Code != "invalid_state" {
		t.Errorf("cancel of a cancelled quest = %+v", r)
	}
}

// The done of a character with neither a step nor a task is registered and
// ignored, and the character is told so; one with a task is answered.
func TestADoneWithNoStepNorTaskIsRegisteredAndIgnoredWithAWarning(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev Claude", "Dev Claude 1")
	r := w.call(dev.ID, "done", nil, "acabei")
	if r.OK || r.Error.Code != "no_active_task" || r.ExitCode() != localchan.ExitConflict {
		t.Fatalf("done with nothing to answer = %+v", r)
	}
	if dones, _ := w.orc.Registry.Dones(w.ctx, dev.ID); len(dones) != 1 || dones[0].Text != "acabei" {
		t.Errorf("the done was not registered: %+v", dones)
	}
	// With a task under way the done answers it.
	if err := w.orc.Characters.TaskAssigned(w.ctx, dev.ID); err != nil {
		t.Fatal(err)
	}
	if r := w.call(dev.ID, "done", nil, "tarefa feita"); !r.OK {
		t.Fatalf("done of a task = %+v", r.Error)
	}
	if got := w.get(dev.ID); got.TaskOpen {
		t.Error("the task is still open after its done")
	}
	if r := w.call(dev.ID, "done", map[string]any{"return": "x"}, "de novo"); r.OK || r.Error.Code != "no_return_step" {
		t.Errorf("return with no step = %+v", r)
	}
	if r := w.call(dev.ID, "done", nil); r.OK || r.ExitCode() != localchan.ExitUsage {
		t.Errorf("done with no summary = %+v", r)
	}
}

// A done that the Citadel applied later (the queue of a Tower) ends the step
// once: the same step is not ended twice by a second call.
func TestASecondDoneOfTheSameStepEndsNothingTwice(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	q := w.run(w.create(oneStep))
	dev := w.char("Dev Claude 1")
	w.finish("Dev Claude 1", "primeiro", nil, "")
	r := w.call(dev.ID, "done", nil, "repetido")
	if r.OK || r.Error.Code != "no_active_task" {
		t.Errorf("the second done = %+v", r)
	}
	runs, _ := w.orc.Quests.Store().Runs(w.ctx, q.ID+"/a")
	if len(runs) != 1 || runs[0].Summary != "primeiro" {
		t.Errorf("runs: %+v", runs)
	}
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepCompleted, "b": quests.StepRunning})
}

// A Tower that cannot be reached holds the step in preparing, and the next
// pass delivers it once the branch gets there.
func TestAStepOnATowerThatCannotBeReachedWaitsForTheSync(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.peers.SetDown(true)
	q := w.run(w.create(`
name: Remota
steps:
  - {id: a, sheet: QA Antigravity, machine: windows-pc}
`))
	// The character cannot be made there yet: the step waits in preparing and
	// the quest is not paused for it.
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepPreparing})
	if got := w.quest(q.ID); got.State != quests.Running {
		t.Fatalf("the quest is %s; a Tower that is down only makes the step wait", got.State)
	}
	if _, err := w.orc.Registry.ByName(w.ctx, w.slug, w.table.ID, "QA Antigravity 1"); err == nil {
		t.Fatal("a character was made for a machine that cannot be reached")
	}
	w.peers.SetDown(false)
	if err := w.orc.Quests.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepRunning})
	if qa := w.char("QA Antigravity 1"); qa.Machine != "torre" {
		t.Errorf("the character runs on %s", qa.Machine)
	}
}
