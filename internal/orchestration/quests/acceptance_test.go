package quests_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/quests"
)

func (w *world) acceptance(live bool) quests.Acceptance {
	a := quests.Acceptance{Store: w.orc.Quests.Store()}
	if live {
		a.Body = w.hub.IssueBody
	}
	return a
}

func (w *world) unproven(live bool) []string {
	w.t.Helper()
	got, err := w.acceptance(live).Unproven(w.ctx, w.slug+"/"+w.table.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	return got
}

// fixQuest is a fix mission: the same issue, only the step that is missing.
func fixQuest(name string, issue int) string {
	return fmt.Sprintf("name: %s\nissue: %d\nsteps:\n  - {id: qa, sheet: QA Antigravity}\n", name, issue)
}

func TestTheAcceptanceAsksForTheCriteriaWithNoProofOfTheCompletedMissionsUntilAFixMissionProvesThem(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	n := w.issueOf(criteriaBody)
	q := w.run(w.create(proofQuest(n)))
	if got := w.unproven(true); len(got) != 0 {
		t.Errorf("a mission that did not end is asked: %v", got)
	}
	w.finish("Dev Claude 1", "feito", map[string]string{"login.go": "1"}, "")
	if r := w.done("QA Antigravity 1", map[string]any{"proof": []string{"C1=a_test.go", "C2=b_test.go"}}, "ok"); !r.OK {
		t.Fatalf("done = %+v", r.Error)
	}
	if got := w.quest(q.ID); got.State != quests.Completed {
		t.Fatalf("the quest is %s, want completed", got.State)
	}
	want := []string{fmt.Sprintf("C3 (issue #%d)", n)}
	for _, live := range []bool{true, false} {
		if got := w.unproven(live); !slices.Equal(got, want) {
			t.Errorf("unproven (live body %v) = %v, want %v", live, got, want)
		}
	}

	w.machines.Report(w.ctx, w.char("QA Antigravity 1").ID, "idle") // its turn ends
	// the fix mission with the same issue: nothing is unmarked, and the proof of C3 clears the list
	fix := w.run(w.create(fixQuest("Correção 1", n)))
	if got := w.unproven(true); !slices.Equal(got, want) {
		t.Errorf("while the fix mission runs = %v, want %v", got, want)
	}
	if r := w.done("QA Antigravity 1", map[string]any{"proof": []string{"C3=c_test.go"}}, "ok"); !r.OK {
		t.Fatalf("done of the fix = %+v", r.Error)
	}
	if got := w.quest(fix.ID); got.State != quests.Completed {
		t.Fatalf("the fix mission is %s", got.State)
	}
	if got := w.unproven(true); len(got) != 0 {
		t.Errorf("after the fix = %v, want none", got)
	}
	body := w.issueBody(n)
	for _, c := range []string{"C1 aceita senha correta", "C2 recusa senha vazia", "C3 bloqueia depois de cinco erros"} {
		if !strings.Contains(body, "- [x] "+c) {
			t.Errorf("%q is not marked after the fix mission:\n%s", c, body)
		}
	}
	if !strings.Contains(body, "prova (declarada por QA Antigravity 1): a_test.go") {
		t.Errorf("the old proof is gone:\n%s", body)
	}
}

func TestACriterionTakenOutOfTheIssueIsNotAskedWhenTheBodyIsReadAndAFailingReadStopsTheOpening(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	n := w.issueOf(criteriaBody)
	w.run(w.create(proofQuest(n)))
	w.finish("Dev Claude 1", "feito", map[string]string{"login.go": "1"}, "")
	w.done("QA Antigravity 1", map[string]any{"proof": []string{"C1=a", "C2=b"}}, "ok")
	w.hub.mu.Lock()
	w.hub.issues[n].Body = strings.Replace(w.hub.issues[n].Body, "- [ ] C3 bloqueia depois de cinco erros\n", "", 1)
	w.hub.mu.Unlock()
	if got := w.unproven(true); len(got) != 0 {
		t.Errorf("C3 was taken out of the issue and is still asked: %v", got)
	}
	// the key must be a table, and a read that fails stops the opening
	if _, err := w.acceptance(true).Unproven(w.ctx, "sem-barra"); err == nil {
		t.Error("a key that is not a table was accepted")
	}
	w.hub.fail = true
	if got, err := w.acceptance(true).Unproven(w.ctx, w.slug+"/"+w.table.ID); err == nil || len(got) != 0 {
		t.Errorf("GitHub failed: %v, %v; want an error", got, err)
	}
}

func TestAnIssueEmptiedOfItsCriteriaIsNotAnAcceptance(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	n := w.issueOf(criteriaBody)
	w.run(w.create(proofQuest(n)))
	w.finish("Dev Claude 1", "feito", map[string]string{"login.go": "1"}, "")
	w.done("QA Antigravity 1", map[string]any{"proof": []string{"C1=a"}}, "ok")
	w.hub.mu.Lock()
	w.hub.issues[n].Body = "nada aqui\n"
	w.hub.mu.Unlock()
	if got, err := w.acceptance(true).Unproven(w.ctx, w.slug+"/"+w.table.ID); err == nil || len(got) != 0 {
		t.Errorf("an emptied issue = %v, %v; want an error", got, err)
	}
}

func TestOnlyThreeFixMissionsPerEpicAreMadeAndTheFourthIsAskedToTheGrandMaster(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	n := w.issueOf(criteriaBody)
	w.run(w.create(proofQuest(n)))
	w.finish("Dev Claude 1", "feito", map[string]string{"login.go": "1"}, "")
	w.done("QA Antigravity 1", map[string]any{"proof": []string{"C1=a", "C2=b"}}, "ok")
	for i := 1; i <= 3; i++ {
		w.create(fixQuest(fmt.Sprintf("Correção %d", i), n)) // made, not run: it is a mission of the epic all the same
	}
	_, err := w.orc.Quests.Create(w.ctx, quests.CreateRequest{Workspace: w.slug, Table: w.table.ID, Data: []byte(fixQuest("Correção 4", n)), Designer: w.master.ID})
	if !errors.Is(err, quests.ErrCorrectionLimit) {
		t.Fatalf("the fourth fix mission = %v, want the limit", err)
	}
	if r := quests.Refusal(err); r.Error == nil || r.Error.Code != "correction_limit" || !strings.Contains(r.Error.Hint, `mestre ask "Grão-Mestre"`) {
		t.Errorf("the answer = %+v", r.Error)
	}
	// another issue of the epic is not a fix mission
	other := w.issueOf("- [ ] C1 outra coisa\n")
	if _, err := w.orc.Quests.Create(w.ctx, quests.CreateRequest{Workspace: w.slug, Table: w.table.ID, Data: []byte(fixQuest("Outra coisa", other)), Designer: w.master.ID}); err != nil {
		t.Errorf("a new issue: %v", err)
	}
}

type onlyIssues map[int]bool

func (o onlyIssues) Owns(_ context.Context, _, _ string, issue int) (bool, error) {
	return o[issue], nil
}

func TestAMissionTakesNoIssueThatIsNotTheTables(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{issues: onlyIssues{101: true}})
	own, foreign := w.issueOf(criteriaBody), w.issueOf(criteriaBody)
	if own != 101 {
		t.Fatalf("the first issue is #%d", own)
	}
	if _, err := w.orc.Quests.Create(w.ctx, quests.CreateRequest{Workspace: w.slug, Table: w.table.ID, Data: []byte(proofQuest(foreign)), Designer: w.master.ID}); !errors.Is(err, quests.ErrOtherIssue) {
		t.Fatalf("the issue of another table = %v, want ErrOtherIssue", err)
	}
	if r := quests.Refusal(quests.ErrOtherIssue); r.Error.Code != "outside_epic" || r.ExitCode() != 3 {
		t.Errorf("the answer = %+v", r)
	}
	if _, err := w.orc.Quests.Create(w.ctx, quests.CreateRequest{Workspace: w.slug, Table: w.table.ID, Data: []byte(proofQuest(own)), Designer: w.master.ID}); err != nil {
		t.Errorf("the issue of the table: %v", err)
	}
}
