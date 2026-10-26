package app_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/quests"
)

const returnDef = `
name: Revisão
max_returns: %d
steps:
  - {id: dev,    sheet: Dev Claude, instructions: "Implemente"}
  - {id: review, sheet: Revisor Codex, after: [dev], can_return_to: dev}
`

// startReview makes the quest, runs it and has the Dev give its done, over the
// real CLI; it returns the Dev and the Reviewer of the steps.
func (w *commWorld) startReview(maxReturns int) (quests.Quest, characters.Character, characters.Character) {
	w.t.Helper()
	v, err := w.orc.Quests.Create(w.ctx, quests.CreateRequest{Workspace: w.table.Workspace, Table: w.table.ID, Data: []byte(fmt.Sprintf(returnDef, maxReturns)), Designer: w.master.ID})
	if err != nil {
		w.t.Fatalf("create: %v", err)
	}
	q, err := w.orc.Quests.Run(w.ctx, v.ID, "")
	if err != nil {
		w.t.Fatalf("run: %v", err)
	}
	step := func(id string) characters.Character {
		st, err := w.orc.Quests.Store().Step(w.ctx, q.ID+"/"+id)
		if err != nil {
			w.t.Fatal(err)
		}
		c, err := w.orc.Registry.Get(w.ctx, st.Character)
		if err != nil {
			w.t.Fatalf("the character of %s: %v", id, err)
		}
		return c
	}
	dev := step("dev")
	if r := w.run(dev, "done", "primeira versão", "--json"); r.code != 0 {
		w.t.Fatalf("done of the Dev = %+v", r)
	}
	w.mach.Report(w.ctx, dev.ID, characters.Idle)
	return q, dev, step("review")
}

// CA-CLI-COM-10: a Reviewer step with a return to the Dev; when the Reviewer
// runs `mestre done "faltam testes" --return "sem testes de erro"` it exits 0
// and `returned_to` points to the step of the Dev (over the real CLI).
func TestDoneWithReturnOverTheCLIExitsZeroAndPointsToTheStepOfTheDev(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	_, _, rev := w.startReview(3)
	r := w.run(rev, "done", "faltam testes", "--return", "sem testes de erro", "--json")
	v := decodeJSON(t, r)
	if r.code != 0 || !v.OK || v.Data["returned_to"] != "dev" {
		t.Fatalf("done --return = %d %s", r.code, r.stdout)
	}
}

// CA-CLI-COM-12: with the limit of returns already used, `mestre done "x"
// --return "y"` exits 0, `returned_to` is null, the quest is paused and the
// table master gets return_limit (over the real CLI).
func TestDoneWithReturnPastTheLimitOverTheCLIPausesTheQuest(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)

	q, dev, rev := w.startReview(1)
	if r := w.run(rev, "done", "não", "--return", "motivo", "--json"); r.code != 0 {
		t.Fatalf("first return = %+v", r)
	}
	w.mach.Report(w.ctx, rev.ID, characters.Idle)
	if r := w.run(dev, "done", "de novo", "--json"); r.code != 0 {
		t.Fatalf("second done of the Dev = %+v", r)
	}
	w.mach.Report(w.ctx, dev.ID, characters.Idle)
	r := w.run(rev, "done", "x", "--return", "y", "--json")
	v := decodeJSON(t, r)
	if v2, has := v.Data["returned_to"]; r.code != 0 || !v.OK || !has || v2 != nil || v.Data["quest_state"] != "paused" {
		t.Fatalf("done past the limit = %d %s", r.code, r.stdout)
	}
	if got, err := w.orc.Quests.Store().Quest(w.ctx, q.ID); err != nil || got.State != quests.Paused {
		t.Errorf("the quest: %+v %v", got, err)
	}
	found := false
	for _, m := range w.mach.Delivered(w.get(w.master)) {
		found = found || strings.Contains(m, "[return_limit]")
	}
	if !found {
		t.Error("the table master was not told return_limit")
	}
}
