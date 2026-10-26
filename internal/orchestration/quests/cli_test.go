package quests_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/orchestration/quests"
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
	if r := w.call(w.master.ID, "quest run", nil, v.Name); r.OK || r.Error.Code != "invalid_state" || r.ExitCode() != localchan.ExitConflict {
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
