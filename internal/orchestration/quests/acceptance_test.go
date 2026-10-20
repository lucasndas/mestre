package quests_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
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

// The count of the fix missions includes the ones that were cancelled and the
// first mission of an issue the engine made; the missions of another table do
// not count, nor are they asked for in this one.
func TestTheLimitCountsCancelledMissionsAndEngineMadeIssuesButNotAnotherTable(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{epics: []int{43}})
	first := w.run(w.create("name: Livre\nsteps:\n  - {id: dev, sheet: Dev Claude}\n")) // the engine makes its issue
	n := w.quest(first.ID).Issue
	if n == 0 {
		t.Fatal("the engine made no issue for the first mission")
	}
	other, err := w.orc.Tables.Open(w.ctx, tables.OpenRequest{By: w.gm, Epic: 43})
	if err != nil {
		t.Fatal(err)
	}
	// a mission of another table on a number that happens to be the same
	if _, err := w.orc.Quests.Create(w.ctx, quests.CreateRequest{Workspace: w.slug, Table: other.Table.ID, Data: []byte(fixQuest("De outra mesa", n)), Designer: other.Master.ID}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		v := w.create(fixQuest(fmt.Sprintf("Correção %d", i), n))
		if i == 1 {
			if _, err := w.orc.Quests.Cancel(w.ctx, v.ID, "não serve"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := w.orc.Quests.Create(w.ctx, quests.CreateRequest{Workspace: w.slug, Table: w.table.ID, Data: []byte(fixQuest("Correção 4", n)), Designer: w.master.ID}); !errors.Is(err, quests.ErrCorrectionLimit) {
		t.Fatalf("the fourth fix mission = %v, want the limit", err)
	}
	// nothing is asked of another table whose missions did not complete
	if got, err := w.acceptance(true).Unproven(w.ctx, w.slug+"/"+other.Table.ID); err != nil || len(got) != 0 {
		t.Errorf("another table: %v, %v", got, err)
	}
}

// Two creations at once cannot both take the last place: the check runs in the
// transaction that inserts the mission.
func TestTheLastFixMissionGoesToOnlyOneOfSeveralCreationsAtOnce(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	n := w.issueOf(criteriaBody)
	w.run(w.create(proofQuest(n)))
	w.create(fixQuest("Correção 1", n))
	w.create(fixQuest("Correção 2", n))
	var made atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := w.orc.Quests.Create(w.ctx, quests.CreateRequest{Workspace: w.slug, Table: w.table.ID, Data: []byte(fixQuest(fmt.Sprintf("Concorrente %d", i), n)), Designer: w.master.ID})
			if err == nil {
				made.Add(1)
			} else if !errors.Is(err, quests.ErrCorrectionLimit) {
				t.Errorf("create %d: %v", i, err)
			}
		}()
	}
	wg.Wait()
	if made.Load() != 1 {
		t.Errorf("%d creations took the last place, want 1", made.Load())
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
