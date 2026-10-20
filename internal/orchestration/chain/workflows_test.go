package chain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/users"
)

const wfRepo = "lucas/Meu_App"

func wfAsk(who, head string) chain.WorkflowsAsk {
	return chain.WorkflowsAsk{Requester: who, Repo: wfRepo, Branch: "mestre/42-login", Head: head, About: "ci: muda o build"}
}

// Part of CA-GH-ID-05: the request envio_workflows is born bound to the
// repository of the workspace, the branch and the head; an approval holds for
// that head only and a new commit starts another question.
func TestTheSendingOfWorkflowsWaitsForAnApprovalBoundToTheHeadAndANewCommitAsksAgain(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev")
	ok, id, err := w.ch.Workflows(w.ctx, wfAsk(dev.ID, "aaaa1111"))
	if err != nil || ok || id == 0 {
		t.Fatalf("first ask = %v, %d, %v; want a request and no go-ahead", ok, id, err)
	}
	if r := w.request(id); r.Type != "envio_workflows" || r.Requester != dev.ID || r.Impression == "" {
		t.Fatalf("the request = %+v", r)
	}
	if r := w.request(id); r.Level.Kind == chain.LevelMesa {
		t.Errorf("a request of workflows is at the Mestre da mesa without a subdelegation")
	}
	if ok, again, _ := w.ch.Workflows(w.ctx, wfAsk(dev.ID, "aaaa1111")); ok || again != id {
		t.Fatalf("asking twice = %v, %d, want the same request %d", ok, again, id)
	}
	// nobody else gets it
	other := w.recruit("QA")
	if ok, oid, _ := w.ch.Workflows(w.ctx, wfAsk(other.ID, "aaaa1111")); ok || oid == id {
		t.Fatalf("another character got %v, %d", ok, oid)
	}
	w.approveAsOwner(id)
	if ok, used, err := w.ch.Workflows(w.ctx, wfAsk(dev.ID, "aaaa1111")); err != nil || !ok || used != id {
		t.Fatalf("with the approval = %v, %d, %v", ok, used, err)
	}
	// a retry of a sending that failed after the push is not asked again
	if ok, _, err := w.ch.Workflows(w.ctx, wfAsk(dev.ID, "aaaa1111")); err != nil || !ok {
		t.Fatalf("the retry with the same head = %v, %v", ok, err)
	}
	// a commit after the approval: it is over, and a new question is open
	ok, changed, err := w.ch.Workflows(w.ctx, wfAsk(dev.ID, "bbbb2222"))
	if err != nil || ok || changed == id {
		t.Fatalf("a new head = %v, %d, %v", ok, changed, err)
	}
	if got := w.request(id); got.State != chain.Invalidated {
		t.Fatalf("the approval of the old head is %s, want invalidated", got.State)
	}
	if ok, _, _ := w.ch.Workflows(w.ctx, wfAsk(dev.ID, "aaaa1111")); ok {
		t.Error("the old head went with the invalidated approval")
	}
}

func TestAnApprovalOfWorkflowsNotUsedInTimeOrDeniedIsNotGranted(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev")
	_, id, _ := w.ch.Workflows(w.ctx, wfAsk(dev.ID, "aaaa1111"))
	w.approveAsOwner(id)
	w.clk.Advance(24*time.Hour + time.Second)
	if ok, _, err := w.ch.Workflows(w.ctx, wfAsk(dev.ID, "aaaa1111")); err != nil || ok {
		t.Fatalf("a late use = %v, %v", ok, err)
	}
	_, id2, _ := w.ch.Workflows(w.ctx, wfAsk(dev.ID, "cccc3333"))
	if _, err := w.ch.DecideByPerson(w.ctx, chain.PersonDecision{Request: id2, User: "u1", Level: string(users.Owner), Answer: chain.AnswerDeny}); err != nil {
		t.Fatal(err)
	}
	if ok, again, _ := w.ch.Workflows(w.ctx, wfAsk(dev.ID, "cccc3333")); ok || again == id2 {
		t.Fatalf("after a denial = %v, %d", ok, again)
	}
	if _, _, err := w.ch.Workflows(w.ctx, chain.WorkflowsAsk{Requester: dev.ID}); !errors.Is(err, chain.ErrNoWorkflows) {
		t.Errorf("an incomplete ask = %v", err)
	}
}

func TestARequestOfWorkflowsForAnotherRepositoryThanTheOneOfTheWorkspaceIsRefused(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev")
	a := wfAsk(dev.ID, "aaaa1111")
	a.Repo = "lucas/outro"
	if _, _, err := w.ch.Workflows(w.ctx, a); !errors.Is(err, chain.ErrWrongRepo) {
		t.Fatalf("another repository = %v, want ErrWrongRepo", err)
	}
}
