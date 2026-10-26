package quests_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/quests"
)

const beforeDef = `
name: Com portão
steps:
  - {id: a, sheet: Dev Claude, instructions: "Faça", approval: before}
`

const afterDef = `
name: Com revisão
max_returns: 1
steps:
  - {id: a, sheet: Dev Claude, approval: after}
  - {id: b, sheet: Revisor Codex, after: [a]}
`

// An approval point before the step: the request is of the quest, names the
// designer so the chain never gives it the decision, and the step waits in
// preparing with no character made. The designer's own decision is refused; a
// person's goes on.
func TestAnApprovalBeforeHoldsTheStepAndTheDesignerNeverDecidesIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	started := len(w.machines.Started())
	q := w.run(w.create(beforeDef))
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepPreparing})
	if len(w.machines.Started()) != started {
		t.Error("a character was made before the approval")
	}
	if w.appr.count() != 1 {
		t.Fatalf("requests = %d", w.appr.count())
	}
	p := w.appr.last()
	if p.When != quests.ApprovalBefore || p.Designer != w.master.ID || p.Quest != q.ID || p.StepID != "a" || p.Table != w.table.ID {
		t.Errorf("the request: %+v", p)
	}
	st := w.step(q, "a")
	if !strings.HasPrefix(st.Blocker, "approval:") || st.Approval == 0 {
		t.Fatalf("the step: %+v", st)
	}
	// The same pass again asks nothing more.
	if err := w.orc.Quests.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	if w.appr.count() != 1 {
		t.Errorf("the request was asked again: %d", w.appr.count())
	}

	err := w.orc.Quests.Decided(w.ctx, quests.Decision{Request: st.Approval, Approved: true, By: w.master.ID})
	if !errors.Is(err, quests.ErrSelfApproval) {
		t.Fatalf("the designer decided its own point: %v", err)
	}
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepPreparing})
	if err := w.orc.Quests.Decided(w.ctx, quests.Decision{Request: st.Approval, Approved: true, By: w.gm.ID}); err != nil {
		t.Fatal(err)
	}
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepRunning})
	if w.char("Dev Claude 1").Quest != q.ID {
		t.Error("the character of the step was not made after the approval")
	}
	if err := w.orc.Quests.Decided(w.ctx, quests.Decision{Request: 999}); !errors.Is(err, quests.ErrNoRequest) {
		t.Errorf("a request nobody waits for = %v", err)
	}
}

// An approval before that is denied pauses the quest and tells the Master;
// resuming asks again.
func TestAnApprovalBeforeThatIsDeniedPausesTheQuestAndTellsTheMaster(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	q := w.run(w.create(beforeDef))
	req := w.step(q, "a").Approval
	if err := w.orc.Quests.Decided(w.ctx, quests.Decision{Request: req, Approved: false, By: "lucas", Reason: "não agora"}); err != nil {
		t.Fatal(err)
	}
	if got := w.quest(q.ID); got.State != quests.Paused || !strings.Contains(got.PausedReason, "negada") {
		t.Fatalf("the quest: %s (%q)", got.State, got.PausedReason)
	}
	if !w.told(w.master, "aprovação antes da etapa a foi negada: não agora") {
		t.Error("the Master was not told")
	}
	if _, err := w.orc.Quests.Resume(w.ctx, q.ID); err != nil {
		t.Fatal(err)
	}
	if w.appr.count() != 2 {
		t.Errorf("after the resume the point was asked %d times; want a new request", w.appr.count())
	}
}

// An approval after the step: the work is done, the baton waits for the
// approval; approved, it goes on; denied, the step goes back to preparing with
// the reason, which counts as a return.
func TestAnApprovalAfterHoldsTheBatonAndADenialIsAReturn(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	q := w.run(w.create(afterDef))
	res := w.finish("Dev Claude 1", "primeiro", map[string]string{"a.go": "1"}, "")
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepApproving, "b": quests.StepPending})
	if res.QuestState != "running" {
		t.Errorf("quest state = %s", res.QuestState)
	}
	p := w.appr.last()
	if p.When != quests.ApprovalAfter || !strings.Contains(p.Summary, "primeiro") || !strings.Contains(p.Summary, "A a.go") || p.Character == "" || p.Designer != w.master.ID {
		t.Errorf("the request after: %+v", p)
	}
	st := w.step(q, "a")
	if waits, _ := w.orc.Waits.Store().ActiveOf(w.ctx, st.Character); len(waits) != 1 || waits[0].Kind != "approval" {
		t.Errorf("the character does not wait for the approval: %+v", waits)
	}

	// Denied: back to preparing, with the reason, delivered to the same character.
	if err := w.orc.Quests.Decided(w.ctx, quests.Decision{Request: st.Approval, Approved: false, By: w.gm.ID, Reason: "faltam testes"}); err != nil {
		t.Fatal(err)
	}
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepRunning, "b": quests.StepPending})
	if got := w.step(q, "a"); got.Returns != 1 || got.Approval != 0 {
		t.Errorf("the step after the denial: %+v", got)
	}
	if item := w.itemOf("Dev Claude 1"); !strings.Contains(item, "faltam testes") {
		t.Errorf("the character did not get the reason:\n%s", item)
	}
	if runs, _ := w.orc.Quests.Store().Runs(w.ctx, q.ID+"/a"); len(runs) != 2 {
		t.Errorf("a new run was not made: %+v", runs)
	}
	if waits, _ := w.orc.Waits.Store().ActiveOf(w.ctx, st.Character); len(waits) != 0 {
		t.Errorf("the wait for the approval stayed: %+v", waits)
	}

	// Done again, approved: the baton goes on.
	w.finish("Dev Claude 1", "segundo", nil, "")
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepApproving})
	if w.appr.count() != 2 {
		t.Fatalf("requests = %d", w.appr.count())
	}
	if err := w.orc.Quests.Decided(w.ctx, quests.Decision{Request: w.step(q, "a").Approval, Approved: true}); err != nil {
		t.Fatal(err)
	}
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepCompleted, "b": quests.StepRunning})
	if item := w.itemOf("Revisor Codex 1"); !strings.Contains(item, "segundo") || strings.Contains(item, "primeiro") {
		t.Errorf("b got:\n%s", item)
	}
}

// Denials count as returns: over max_returns the quest pauses with the limit
// and the Master gets the event.
func TestDenialsPastTheLimitOfReturnsPauseTheQuest(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	q := w.run(w.create(afterDef)) // max_returns: 1
	w.finish("Dev Claude 1", "um", nil, "")
	if err := w.orc.Quests.Decided(w.ctx, quests.Decision{Request: w.step(q, "a").Approval, Approved: false, Reason: "não 1"}); err != nil {
		t.Fatal(err)
	}
	w.finish("Dev Claude 1", "dois", nil, "")
	if err := w.orc.Quests.Decided(w.ctx, quests.Decision{Request: w.step(q, "a").Approval, Approved: false, Reason: "não 2"}); err != nil {
		t.Fatal(err)
	}
	if got := w.quest(q.ID); got.State != quests.Paused || got.PausedReason != "limite de voltas" {
		t.Fatalf("the quest: %s (%q)", got.State, got.PausedReason)
	}
	if !w.told(w.master, "[return_limit]") || !w.told(w.master, "não 2") {
		t.Error("the Master did not get the event return_limit")
	}
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepPreparing})
}

// Without the flow of approval (milestone 5) a quest pauses at a point of
// approval and says why; it never runs past it.
func TestWithoutTheFlowOfApprovalTheQuestPausesAtThePoint(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{noApprovals: true})
	q := w.run(w.create(beforeDef))
	got := w.quest(q.ID)
	if got.State != quests.Paused || !strings.Contains(got.PausedReason, "fluxo de aprovação") {
		t.Errorf("the quest: %s (%q)", got.State, got.PausedReason)
	}
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepPreparing})
	if !w.told(w.master, "fluxo de aprovação") {
		t.Error("the Master was not told")
	}
}

// A return that reaches a step waiting for its approval after makes its work
// useless: the step goes back to pending at once, and a late decision of the old
// request completes nothing (docs/orquestracao/missoes.md#devolução-com-limite).
func TestAReturnThatReachesAStepWaitingForItsApprovalAfterSendsItBackToPending(t *testing.T) {
	t.Parallel()
	{
		w := newWorld(t)
		q := w.run(w.create(`
name: Devolução
max_returns: 3
steps:
  - {id: dev, sheet: Dev Claude}
  - {id: review, sheet: Revisor Codex, after: [dev], can_return_to: dev}
  - {id: qa, sheet: QA Antigravity, after: [dev], machine: windows-pc, approval: after}
`))
		w.finish("Dev Claude 1", "feito", map[string]string{"a.go": "1"}, "")
		w.finish("QA Antigravity 1", "qa ok", nil, "")
		w.wantStates(q, map[string]quests.StepState{"qa": quests.StepApproving, "review": quests.StepRunning})
		req := w.step(q, "qa").Approval
		w.finish("Revisor Codex 1", "falta teste", nil, "faltam testes")
		if got := w.states(q)["qa"]; got != quests.StepPending {
			t.Errorf("qa is %s after the return; want pending", got)
		}
		if st := w.step(q, "qa"); st.DiscardResult || st.Approval != 0 {
			t.Errorf("qa keeps the marks of the old work: %+v", st)
		}
		_ = w.orc.Quests.Decided(w.ctx, quests.Decision{Request: req, Approved: true, Reason: "ok"})
		if got := w.states(q)["qa"]; got == quests.StepCompleted {
			t.Error("a late approval completed a step whose work was discarded")
		}
	}
}
