package quests_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/scrolls"
)

// proofAll is the class that asks for a proof of every criterion.
type proofAll struct{}

func (proofAll) DoneRequires(context.Context, string, string, string) (classes.DoneRequires, error) {
	return classes.DoneRequires{Proof: classes.ProofAll}, nil
}

const criteriaBody = "Login com senha.\n\n## Critérios\n- [ ] C1 aceita senha correta\n- [ ] C2 recusa senha vazia\n- [ ] C3 bloqueia depois de cinco erros\n"

// issueOf makes the issue the Master of the table would have made.
func (w *world) issueOf(body string) int {
	w.t.Helper()
	w.hub.mu.Lock()
	defer w.hub.mu.Unlock()
	return w.hub.make(42, "Login", body)
}

func (w *world) issueBody(n int) string { return w.hub.get(n).Body }

func (w *world) done(name string, flags map[string]any, summary string) localchan.Response {
	w.t.Helper()
	return w.call(w.char(name).ID, "done", flags, summary)
}

func proofQuest(issue int) string {
	return fmt.Sprintf(`
name: Prova
issue: %d
steps:
  - {id: dev, sheet: Dev Claude}
  - {id: qa, sheet: QA Antigravity, after: [dev]}
`, issue)
}

// CA-PROVA-01
func TestAProofMarksTheCriterionOnTheIssueAndGoesToTheScroll(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	n := w.issueOf(criteriaBody)
	q := w.run(w.create(proofQuest(n)))
	if got := w.hub.calls; contains(got, "issue") {
		t.Fatalf("a second issue was made: %v", got)
	}
	w.finish("Dev Claude 1", "feito", map[string]string{"login.go": "1"}, "")

	r := w.done("QA Antigravity 1", map[string]any{"proof": []string{"C1=login_test.go:TestVazia", "C2=captura em docs/c2.png"}}, "ok")
	if !r.OK {
		t.Fatalf("done = %+v", r.Error)
	}
	body := w.issueBody(n)
	for _, want := range []string{"- [x] C1 aceita senha correta", "  - prova (declarada por QA Antigravity 1): login_test.go:TestVazia",
		"- [x] C2 recusa senha vazia", "captura em docs/c2.png", "- [ ] C3 bloqueia depois de cinco erros", "Login com senha.", "<!-- mestre:etapas -->"} {
		if !strings.Contains(body, want) {
			t.Errorf("the issue lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "C3 bloqueia depois de cinco erros\n  - prova") {
		t.Error("C3 has a proof")
	}
	text := w.scroll(w.quest(q.ID))
	for _, want := range []string{"**Provas:**", "C1 (declarada por QA Antigravity 1)", "login_test.go:TestVazia", "C2 (declarada", "captura em docs/c2.png"} {
		if !strings.Contains(text, want) {
			t.Errorf("the scroll lacks %q:\n%s", want, text)
		}
	}

	// a criterion the quest does not have: refused, nothing registered
	w2 := newWorld(t)
	n2 := w2.issueOf(criteriaBody)
	q2 := w2.run(w2.create(proofQuest(n2)))
	w2.finish("Dev Claude 1", "feito", nil, "")
	r = w2.done("QA Antigravity 1", map[string]any{"proof": []string{"C9=x"}}, "ok")
	if r.OK || r.Error.Code != "not_found" || r.ExitCode() != localchan.ExitNotFound {
		t.Fatalf("a proof of C9 = %+v", r)
	}
	w2.wantStates(q2, map[string]quests.StepState{"qa": quests.StepRunning})
	if ps, _ := w2.orc.Quests.Store().Proofs(w2.ctx, q2.ID, 1); len(ps) != 0 {
		t.Errorf("a refused done kept proofs: %+v", ps)
	}
	if dones, _ := w2.orc.Registry.Dones(w2.ctx, w2.char("QA Antigravity 1").ID); len(dones) != 0 {
		t.Errorf("a refused done was registered: %+v", dones)
	}
}

// CA-PROVA-02
func TestAClassThatAsksForProofRefusesTheDoneThatLacksOne(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{done: proofAll{}})
	n := w.issueOf("- [ ] C1 primeiro\n- [ ] C2 segundo\n")
	q := w.run(w.create(fmt.Sprintf("name: Prova\nissue: %d\nsteps:\n  - {id: qa, sheet: QA Antigravity}\n", n)))

	r := w.done("QA Antigravity 1", map[string]any{"proof": []string{"C1=t"}}, "x")
	if r.OK || r.Error.Code != "proof_missing" || r.ExitCode() != localchan.ExitConflict || !strings.Contains(r.Error.Message, "C2") || strings.Contains(r.Error.Message, "C1") {
		t.Fatalf("done with C1 only = %+v", r)
	}
	w.wantStates(q, map[string]quests.StepState{"qa": quests.StepRunning})
	r = w.done("QA Antigravity 1", map[string]any{"proof": []string{"C2=u"}}, "y")
	if !r.OK {
		t.Fatalf("done with C2 after C1 = %+v", r.Error)
	}
	if got := w.quest(q.ID); got.State != quests.Completed {
		t.Errorf("the quest is %s, want completed", got.State)
	}
	if !strings.Contains(w.issueBody(n), "- [x] C1 primeiro") || !strings.Contains(w.issueBody(n), "- [x] C2 segundo") {
		t.Errorf("the issue:\n%s", w.issueBody(n))
	}
}

// CA-PROVA-06
func TestTheSameFailureThreeTimesPausesBeforeTheReturnsRunOut(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	q := w.run(w.create("same_failure: 3\nmax_returns: 5\nname: Revisão\nsteps:\n  - {id: dev, sheet: Dev Claude}\n  - {id: review, sheet: Revisor Codex, after: [dev], can_return_to: dev}\n"))
	w.finish("Dev Claude 1", "v1", map[string]string{"a.go": "1"}, "")
	returnWith := func(where string) localchan.Response {
		r := w.done("Revisor Codex 1", map[string]any{"return": "sem teste", "cause": "test", "where": []string{where}}, "reprovado")
		if !r.OK {
			t.Fatalf("return = %+v", r.Error)
		}
		return r
	}
	for i := 1; i <= 2; i++ {
		returnWith("src/login.go:40")
		w.machines.Report(w.ctx, w.char("Revisor Codex 1").ID, "idle")
		w.wantStates(q, map[string]quests.StepState{"dev": quests.StepRunning})
		w.finish("Dev Claude 1", fmt.Sprintf("v%d", i+1), map[string]string{"a.go": fmt.Sprint(i + 1)}, "")
	}
	r := returnWith("./src/login.go:40:7") // the same print, as written another way
	var res quests.DoneResult
	if err := json.Unmarshal(r.Data, &res); err != nil {
		t.Fatal(err)
	}
	if res.ReturnedTo != nil || res.QuestState != "paused" {
		t.Fatalf("the third = %+v, want paused and not returned", res)
	}
	if got := w.quest(q.ID); got.PausedReason != "mesma falha repetida" {
		t.Errorf("paused for %q", got.PausedReason)
	}
	var event string
	for _, d := range w.machines.Delivered(w.master) {
		if strings.Contains(d, "return_limit") {
			event = d
		}
	}
	for _, want := range []string{"mesma falha repetida", "src/login.go:40", "3 tentativas", "sem teste"} {
		if !strings.Contains(event, want) {
			t.Errorf("the event return_limit lacks %q: %q", want, event)
		}
	}

	// another print in the middle starts the count again
	w2 := newWorld(t)
	q2 := w2.run(w2.create("same_failure: 3\nmax_returns: 5\nname: Revisão\nsteps:\n  - {id: dev, sheet: Dev Claude}\n  - {id: review, sheet: Revisor Codex, after: [dev], can_return_to: dev}\n"))
	w2.finish("Dev Claude 1", "v1", map[string]string{"a.go": "1"}, "")
	for i, where := range []string{"src/login.go:40", "src/login.go:52", "src/login.go:40"} {
		r := w2.done("Revisor Codex 1", map[string]any{"return": "sem teste", "cause": "test", "where": []string{where}}, "reprovado")
		var res quests.DoneResult
		if err := json.Unmarshal(r.Data, &res); err != nil || res.ReturnedTo == nil || res.QuestState != "running" {
			t.Fatalf("return %d = %+v (%v), want it back to the Dev", i+1, res, err)
		}
		w2.machines.Report(w2.ctx, w2.char("Revisor Codex 1").ID, "idle")
		w2.finish("Dev Claude 1", fmt.Sprintf("v%d", i+2), map[string]string{"a.go": fmt.Sprint(i + 2)}, "")
	}
	if got := w2.quest(q2.ID); got.State != quests.Running {
		t.Errorf("the quest is %s", got.State)
	}
}

// CA-PROVA-07
func TestReturnsWithNoPrintNeverPauseForTheSameFailure(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	q := w.run(w.create("same_failure: 2\nmax_returns: 5\nname: Revisão\nsteps:\n  - {id: dev, sheet: Dev Claude}\n  - {id: review, sheet: Revisor Codex, after: [dev], can_return_to: dev}\n"))
	w.finish("Dev Claude 1", "v1", map[string]string{"a.go": "1"}, "")
	for i, flags := range []map[string]any{{}, {"cause": "test"}, {"cause": "test"}, {}} {
		flags["return"] = "ainda não"
		r := w.done("Revisor Codex 1", flags, "reprovado")
		var res quests.DoneResult
		if err := json.Unmarshal(r.Data, &res); err != nil || res.ReturnedTo == nil {
			t.Fatalf("return %d = %+v (%v)", i+1, res, err)
		}
		w.machines.Report(w.ctx, w.char("Revisor Codex 1").ID, "idle")
		w.finish("Dev Claude 1", fmt.Sprintf("v%d", i+2), map[string]string{"a.go": fmt.Sprint(i + 2)}, "")
	}
	if got := w.quest(q.ID); got.State != quests.Running {
		t.Errorf("the quest is %s: a return with no place paused it", got.State)
	}
}

// CA-PROVA-08
func TestAQuestThatReturnsHasAReviewScrollThatTheSecondPassReads(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	q := w.run(w.create(reviewDef))
	_, text, err := w.orc.Scrolls.Read(w.ctx, scrolls.ReadRequest{Workspace: w.slug, Table: w.table.ID, Actor: scrolls.Engine, Name: "Revisão: Revisão"})
	if err != nil {
		t.Fatalf("the review scroll: %v", err)
	}
	if !strings.Contains(text, "| ID | Etapa dona | Tipo | Onde | Achado | Estado |") {
		t.Errorf("the review scroll:\n%s", text)
	}
	w.finish("Dev Claude 1", "v1", map[string]string{"a.go": "1"}, "")
	if item := w.itemOf("Revisor Codex 1"); strings.Contains(item, "Pergaminho de revisão") {
		t.Errorf("the first pass already has the review scroll:\n%s", item)
	}
	// the reviewer writes a finding of the dev and hands the work back
	_, _, err = w.orc.Scrolls.Read(w.ctx, scrolls.ReadRequest{Workspace: w.slug, Table: w.table.ID, Actor: scrolls.Engine, Name: "Revisão: Revisão"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.orc.Scrolls.AppendSection(w.ctx, w.slug, w.table.ID, "Revisão: Revisão", "| R1 | dev | Bug | `src/login.go:40` | aceita senha vazia | aberto |\n| R2 | review | Gap | `x.go:1` | outro | aberto |"); err != nil {
		t.Fatal(err)
	}
	w.finish("Revisor Codex 1", "reprovado", nil, "aceita senha vazia")
	devItem := w.itemOf("Dev Claude 1")
	if !strings.Contains(devItem, "R1") || strings.Contains(devItem, "R2") || !strings.Contains(devItem, "Achados da revisão que são seus") {
		t.Errorf("the dev did not get exactly its findings:\n%s", devItem)
	}
	w.finish("Dev Claude 1", "v2", map[string]string{"a.go": "2"}, "")
	item := w.itemOf("Revisor Codex 1")
	review := strings.Index(item, "Pergaminho de revisão")
	instr := strings.Index(item, "Instruções:")
	if review < 0 || !strings.Contains(item, "aceita senha vazia") || !strings.Contains(item, "aberto") || !strings.Contains(item, "confira cada achado") || (instr >= 0 && review > instr) {
		t.Errorf("the second pass of the reviewer does not start by the review scroll:\n%s", item)
	}
	_ = q
}

// CA-PROVA-11
func TestTheCriteriaAreFrozenForTheRoundAndTheNextRoundReadsThemAgain(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	n := w.issueOf("- [ ] C1 um\n- [ ] C2 dois\n")
	q := w.run(w.create("name: Uma\nissue: " + fmt.Sprint(n) + "\nsteps:\n  - {id: dev, sheet: Dev Claude}\n"))
	w.hub.mu.Lock()
	w.hub.issues[n].Body += "- [ ] C3 três\n"
	w.hub.mu.Unlock()
	crit, _ := w.orc.Quests.Store().Criteria(w.ctx, q.ID)
	if len(crit) != 2 {
		t.Fatalf("criteria = %+v, want C1 and C2 for the round", crit)
	}
	if r := w.done("Dev Claude 1", map[string]any{"proof": []string{"C3=x"}}, "ok"); r.OK || r.Error.Code != "not_found" {
		t.Fatalf("a proof of the criterion added meanwhile = %+v", r)
	}
	w.finish("Dev Claude 1", "feito", nil, "")
	if contains(w.hub.calls, "issue") {
		t.Errorf("an issue was made: %v", w.hub.calls)
	}
	if got := w.quest(q.ID); got.State != quests.Completed {
		t.Fatalf("the quest is %s", got.State)
	}
	if _, err := w.orc.Quests.Run(w.ctx, q.ID, ""); err != nil {
		t.Fatal(err)
	}
	crit, _ = w.orc.Quests.Store().Criteria(w.ctx, q.ID)
	if len(crit) != 3 {
		t.Errorf("the next round has %+v, want C1 to C3", crit)
	}
}

// CA-PROVA-12, the half of the done
func TestProofDoesNotGoWithReturnAndTheCauseMustBeOneOfTheList(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	n := w.issueOf("- [ ] C1 um\n")
	w.run(w.create("name: Rev\nissue: " + fmt.Sprint(n) + "\nsteps:\n  - {id: dev, sheet: Dev Claude}\n  - {id: review, sheet: Revisor Codex, after: [dev], can_return_to: dev}\n"))
	w.finish("Dev Claude 1", "v1", nil, "")
	for name, flags := range map[string]map[string]any{
		"proof with return":    {"return": "y", "proof": []string{"C1=t"}},
		"unknown cause":        {"return": "y", "cause": "urgente"},
		"cause with no return": {"cause": "test"},
		"where with no return": {"where": []string{"a.go:1"}},
		"four places":          {"return": "y", "where": []string{"a:1", "b:1", "c:1", "d:1"}},
		"proof with no equal":  {"proof": []string{"C1"}},
	} {
		r := w.done("Revisor Codex 1", flags, "x")
		if r.OK || r.ExitCode() != localchan.ExitUsage {
			t.Errorf("%s = %+v, want usage", name, r)
		}
	}
	if r := w.done("Revisor Codex 1", map[string]any{"proof": []string{"C1=" + strings.Repeat("x", quests.MaxProof+1)}}, "x"); r.OK || r.ExitCode() != localchan.ExitLimit {
		t.Errorf("a proof of 501 characters = %+v, want too_large", r)
	}
	// a task with no quest takes no proof
	if r := w.call(w.master.ID, "done", map[string]any{"proof": []string{"C1=t"}}, "x"); r.OK || r.ExitCode() != localchan.ExitUsage {
		t.Errorf("a proof of a character with no step = %+v", r)
	}
}

// CA-PROVA-15, the half of the print and of the quest with no criteria
func TestThePlacesOfAFailureAreNormalizedAndAnIssueWithoutCriteriaRunsWithoutThem(t *testing.T) {
	t.Parallel()
	a, pa := quests.Fingerprint("review", "test", []string{"./a.go:12:5"}, "")
	b, pb := quests.Fingerprint("review", "test", []string{"a.go:12", "a.go:12"}, "")
	if a == "" || a != b || len(pa) != 1 || len(pb) != 1 {
		t.Errorf("prints %q %v and %q %v, want the same", a, pa, b, pb)
	}
	if c, _ := quests.Fingerprint("review", "test", []string{"a.go:13"}, ""); c == a {
		t.Error("another line has the same print")
	}
	if c, _ := quests.Fingerprint("qa", "test", []string{"a.go:12"}, ""); c == a {
		t.Error("another step has the same print")
	}
	if d, _ := quests.Fingerprint("review", "test", []string{`C:\wt\src\a.go:3`}, `C:\wt`); d == "" {
		t.Error("a place under the worktree has no print")
	}
	if e, _ := quests.Fingerprint("review", "test", nil, ""); e != "" {
		t.Errorf("no place has the print %q", e)
	}

	w := newWorld(t, worldOpts{done: proofAll{}})
	n := w.issueOf("Só texto, sem itens.\n")
	q := w.run(w.create(proofQuest(n)))
	if crit, _ := w.orc.Quests.Store().Criteria(w.ctx, q.ID); len(crit) != 0 {
		t.Fatalf("criteria = %+v", crit)
	}
	w.finish("Dev Claude 1", "feito", nil, "")
	w.finish("QA Antigravity 1", "ok", nil, "") // proof: all asks nothing of a quest with no criteria
	if got := w.quest(q.ID); got.State != quests.Completed {
		t.Errorf("the quest is %s", got.State)
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
