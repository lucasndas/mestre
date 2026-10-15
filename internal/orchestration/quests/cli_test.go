package quests_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
)

// The answers of `done` and `quest` as docs/cli/comunicacao.md and
// docs/cli/coordenacao.md give them: the exit code and the fields of --json
// (the CLI prints exactly this).

func data(t *testing.T, r localchan.Response) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.Data, &m); err != nil {
		t.Fatalf("data of %+v: %v", r, err)
	}
	return m
}

// CA-CLI-COM-10: the Reviewer hands the work back to the Dev with a reason.
func TestDoneWithReturnExitsZeroAndPointsToTheStepOfTheDev(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	q := w.run(w.create(reviewDef))
	w.finish("Dev Claude 1", "primeira versão", map[string]string{"a.go": "1"}, "")
	rev := w.char("Revisor Codex 1")
	r := w.call(rev.ID, "done", map[string]any{"return": "sem testes de erro"}, "faltam testes")
	if !r.OK || r.ExitCode() != 0 {
		t.Fatalf("done --return = %d %+v", r.ExitCode(), r.Error)
	}
	d := data(t, r)
	if d["returned_to"] != "dev" || d["quest_state"] != "running" || d["quest"] != "Revisão" || d["step"] != "review" || d["task"] != q.ID+"/review" ||
		d["queued"] != false {
		t.Errorf("done --json = %v", d)
	}
	if _, ok := d["warnings"].([]any); !ok {
		t.Errorf("warnings = %v: always a list", d["warnings"])
	}
	if item := w.itemOf("Dev Claude 1"); !strings.Contains(item, "sem testes de erro") {
		t.Errorf("the Dev did not get the reason:\n%s", item)
	}
}

// CA-CLI-COM-12: the limit of returns used up: the done is registered, the
// exit is 0, returned_to is null, the quest is paused and the table master
// gets return_limit.
func TestDoneWithReturnPastTheLimitExitsZeroAndPausesTheQuest(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	q := w.run(w.create(strings.Replace(reviewDef, "max_returns: 3", "max_returns: 1", 1)))
	w.finish("Dev Claude 1", "v1", map[string]string{"a.go": "1"}, "")
	w.finish("Revisor Codex 1", "não", nil, "motivo")
	w.finish("Dev Claude 1", "de novo", map[string]string{"a.go": "2"}, "")
	r := w.call(w.char("Revisor Codex 1").ID, "done", map[string]any{"return": "y"}, "x")
	if !r.OK || r.ExitCode() != 0 {
		t.Fatalf("done past the limit = %d %+v", r.ExitCode(), r.Error)
	}
	d := data(t, r)
	if v, has := d["returned_to"]; !has || v != nil || d["quest_state"] != "paused" {
		t.Errorf("done --json = %v", d)
	}
	if got := w.quest(q.ID); got.State != quests.Paused {
		t.Errorf("the quest is %s", got.State)
	}
	found := false
	for _, m := range w.machines.Delivered(w.master) {
		found = found || strings.Contains(m, "[return_limit]")
	}
	if !found {
		t.Error("the table master was not told return_limit")
	}
}

// A summary over 4,000 characters is refused (exit 8) unless it came from
// --file, which takes 64 KiB.
func TestALongSummaryNeedsAFile(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.run(w.create(reviewDef))
	dev := w.char("Dev Claude 1")
	long := strings.Repeat("ç", quests.MaxSummary+1)
	if r := w.call(dev.ID, "done", nil, long); r.OK || r.ExitCode() != localchan.ExitLimit {
		t.Fatalf("a long summary with no file = %+v", r)
	}
	if r := w.call(dev.ID, "done", map[string]any{"from-file": true}, strings.Repeat("x", quests.MaxSummaryFile+1)); r.OK || r.ExitCode() != localchan.ExitLimit {
		t.Fatalf("a summary over 64 KiB = %+v", r)
	}
	if r := w.call(dev.ID, "done", map[string]any{"from-file": true}, long); !r.OK {
		t.Fatalf("a long summary from a file = %+v", r.Error)
	}
}

// CA-CLI-COORD-06: a return with no limit of returns (a negative max_returns)
// is an invalid quest that points at the step.
func TestQuestCreateWithAReturnAndNoMaxReturnsIsInvalidAndPointsAtTheStep(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	def := `
name: Sem limite
max_returns: -1
steps:
  - {id: dev, sheet: Dev Claude}
  - {id: review, sheet: Revisor Codex, after: [dev], can_return_to: dev}
`
	r := w.call(w.master.ID, "quest create", map[string]any{"definition": def}, "Sem limite")
	if r.OK || r.Error.Code != "invalid_quest" || r.ExitCode() != localchan.ExitUsage || !strings.Contains(r.Error.Message, "review") {
		t.Fatalf("quest create = %d %+v", r.ExitCode(), r.Error)
	}
	if qs, _ := w.orc.Quests.List(w.ctx, w.slug, w.table.ID); len(qs) != 0 {
		t.Errorf("a quest was made: %+v", qs)
	}
}

// CA-CLI-COORD-07: quest run returns at once, running, and the shapes of
// create and status are the ones of the document.
func TestQuestRunReturnsAtOnceRunning(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	v := w.create(reviewDef)
	start := w.clk.Now()
	r := w.call(w.master.ID, "quest run", nil, v.Name)
	// It returned with nobody moving the clock: it did not wait for the step.
	if !r.OK || data(t, r)["status"] != "running" || !w.clk.Now().Equal(start) {
		t.Fatalf("quest run = %+v", r)
	}
	if r := w.call(w.master.ID, "quest run", nil, v.Name); r.OK || r.Error.Code != "invalid_state" && r.Error.Code != "already_running" || r.ExitCode() != localchan.ExitConflict {
		t.Errorf("a quest that already runs = %+v", r)
	}
	s := data(t, w.call(w.master.ID, "quest status", nil, v.Name))
	steps, _ := s["steps"].([]any)
	if s["state"] != "running" || s["current_step"] != float64(1) || len(steps) != 2 {
		t.Fatalf("quest status = %v", s)
	}
	first := steps[0].(map[string]any)
	for _, k := range []string{"n", "sheet", "agent", "state", "loops", "waiting_for", "issue"} {
		if _, ok := first[k]; !ok {
			t.Errorf("the step lacks %q: %v", k, first)
		}
	}
	text := w.call(w.master.ID, "quest status", nil, v.Name)
	var tx struct{ Text string }
	_ = json.Unmarshal(text.Data, &tx)
	if !strings.Contains(tx.Text, "1. dev") || !strings.Contains(tx.Text, "Dev Claude 1") {
		t.Errorf("the text of status:\n%s", tx.Text)
	}
	list := data(t, w.call(w.master.ID, "quest status", nil))
	if qs, _ := list["quests"].([]any); len(qs) != 1 {
		t.Errorf("the list of quests = %v", list)
	}
}
