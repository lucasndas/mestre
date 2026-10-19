package approvals_test

import (
	"errors"
	"testing"

	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/users"
)

// CA-APROV-FLUXO-02
func TestTheFirstValidAnswerDecidesAndTheOthersReadWhoAnswered(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	lucas := w.owner
	ana := w.add("Ana", users.Approver, "loja")
	r := w.open(openOpts{})
	if _, _, ok := w.msg.To(lucas.ID); !ok {
		t.Fatal("Lucas was not asked")
	}
	if _, err := w.answer(ana, r.ID, chain.AnswerApprove); err != nil {
		t.Fatal(err)
	}
	if w.state(r.ID) != chain.Approved {
		t.Fatalf("state = %s", w.state(r.ID))
	}
	ref, _, _ := w.msg.To(lucas.ID)
	txt, keep := w.msg.Read(ref)
	if txt != "Já respondido por Ana: Aprovado às 14:30." || keep {
		t.Errorf("the message of Lucas = %q (buttons %v)", txt, keep)
	}
	ref, _, _ = w.msg.To(ana.ID)
	if txt, keep := w.msg.Read(ref); txt != "Você respondeu: Aprovado às 14:30." || keep {
		t.Errorf("the message of Ana = %q (buttons %v)", txt, keep)
	}
	// the first answer is final: Lucas, late, changes nothing
	if _, err := w.answer(lucas, r.ID, chain.AnswerDeny); !errors.Is(err, approvals.ErrNotOpen) {
		t.Errorf("a late answer: %v", err)
	}
	if n := len(w.ch.decisions()); n != 1 {
		t.Errorf("%d decisions", n)
	}
}

// CA-APROV-FLUXO-03
func TestAQuorumNeedsTheRequiredUsersAndEachUserCountsOnce(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	lucas := w.owner
	ana := w.add("Ana", users.Approver, "loja")
	bia := w.add("Bia", users.Approver, "loja")
	r := w.open(openOpts{quorum: 2, require: []string{lucas.ID}})
	rep, err := w.answer(ana, r.ID, chain.AnswerApprove)
	if err != nil || rep.Text != "Sua aprovação foi contada: 1 de 2." {
		t.Fatalf("Ana: %+v, %v", rep, err)
	}
	if _, err := w.answer(ana, r.ID, chain.AnswerApprove); !errors.Is(err, approvals.ErrAnswered) {
		t.Errorf("Ana twice: %v", err)
	}
	contains(t, w.textTo(bia), "1 de 2 aprovações")
	if _, err := w.answer(bia, r.ID, chain.AnswerApprove); err != nil {
		t.Fatal(err)
	}
	if w.state(r.ID) != chain.Open || len(w.ch.decisions()) != 0 {
		t.Fatalf("two approvals without Lucas decided: %s", w.state(r.ID))
	}
	contains(t, w.textTo(lucas), "2 de 2 aprovações")
	if _, err := w.answer(lucas, r.ID, chain.AnswerApprove); err != nil {
		t.Fatal(err)
	}
	if w.state(r.ID) != chain.Approved {
		t.Errorf("with Lucas: %s", w.state(r.ID))
	}
	// agents never count: only users reach the flow, and a deny is enough alone
	r2 := w.open(openOpts{quorum: 2})
	if _, err := w.answer(ana, r2.ID, chain.AnswerDeny); err != nil || w.state(r2.ID) != chain.Denied {
		t.Errorf("a deny: %v, %s", err, w.state(r2.ID))
	}
}

// CA-APROV-FLUXO-05
func TestWhatASuspendedUserApprovedStopsCounting(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Approver, "loja")
	bia := w.add("Bia", users.Approver, "loja")
	r := w.open(openOpts{quorum: 2})
	if _, err := w.answer(ana, r.ID, chain.AnswerApprove); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Suspend(w.ctx, w.owner, ana.ID); err != nil {
		t.Fatal(err)
	}
	contains(t, w.textTo(bia), "0 de 2 aprovações")
	if _, err := w.answer(bia, r.ID, chain.AnswerApprove); err != nil {
		t.Fatal(err)
	}
	if w.state(r.ID) != chain.Open {
		t.Fatalf("the approval of Ana still counted: %s", w.state(r.ID))
	}
	// and the suspended user answers nothing
	if _, err := w.answer(ana, r.ID, chain.AnswerApprove); !errors.Is(err, approvals.ErrNotYours) {
		t.Errorf("a suspended user answered: %v", err)
	}
}

// CA-APROV-FLUXO-06
func TestAnAnswerOfWhoCanNotAnswerIsRefusedAndNothingChanges(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	obs := w.add("Otto", users.Observer, "loja")
	out := w.add("Ivo", users.Approver, "blog")
	ana := w.add("Ana", users.Approver, "loja")
	r := w.open(openOpts{})
	for name, u := range map[string]users.User{"observer": obs, "another workspace": out} {
		if _, err := w.answer(u, r.ID, chain.AnswerApprove); !errors.Is(err, approvals.ErrNotYours) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if w.state(r.ID) != chain.Open || len(w.ch.decisions()) != 0 {
		t.Fatalf("a refused answer changed the request: %s", w.state(r.ID))
	}
	if _, _, ok := w.msg.To(obs.ID); ok {
		t.Error("an Observer was asked")
	}
	if _, _, ok := w.msg.To(out.ID); ok {
		t.Error("a user of another workspace was asked")
	}
	// a level below the minimum of the request
	hi := w.open(openOpts{min: classes.LevelAdmin})
	if _, err := w.answer(ana, hi.ID, chain.AnswerApprove); !errors.Is(err, approvals.ErrNotYours) {
		t.Errorf("below the minimum: %v", err)
	}
	if _, _, ok := w.msg.To(ana.ID); !ok {
		t.Fatal("Ana was not asked the first one")
	}
	// the Approver does not give a permanent answer
	if _, err := w.answer(ana, r.ID, chain.AnswerApproveAlways); !errors.Is(err, approvals.ErrLevel) {
		t.Errorf("approve always by an Approver: %v", err)
	}
	// unknown request
	if _, err := w.answer(ana, 999, chain.AnswerApprove); !errors.Is(err, approvals.ErrNotYours) {
		t.Errorf("unknown request: %v", err)
	}
	// the channel locked
	w.lock(true)
	if _, err := w.answer(ana, r.ID, chain.AnswerApprove); !errors.Is(err, approvals.ErrLocked) {
		t.Errorf("locked: %v", err)
	}
	w.lock(false)
	if _, err := w.answer(ana, r.ID, chain.AnswerApprove); err != nil {
		t.Errorf("unlocked: %v", err)
	}
}

// CA-APROV-FLUXO-01
func TestAnApprovalOfAnotherTargetIsInvalidatedAndNothingIsApplied(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Approver, "loja")
	r := w.open(openOpts{target: `{"repo":"lucas/loja","pr":7,"head":"aaa"}`})
	w.tg.set(r.ID, `{"head":"bbb","pr":7,"repo":"lucas/loja"}`) // same keys, another head
	if _, err := w.answer(ana, r.ID, chain.AnswerApprove); !errors.Is(err, approvals.ErrTargetChanged) {
		t.Fatalf("the answer: %v", err)
	}
	if w.state(r.ID) != chain.Invalidated || len(w.ch.decisions()) != 0 {
		t.Fatalf("state = %s, decisions = %d", w.state(r.ID), len(w.ch.decisions()))
	}
	contains(t, w.textTo(ana), "Invalidado")
	// the same target in another spelling is the same target
	r2 := w.open(openOpts{target: `{"repo":"lucas/loja","pr":8,"head":"aaa"}`})
	w.tg.set(r2.ID, `{ "head": "aaa", "pr": 8, "repo": "lucas/loja" }`)
	if _, err := w.answer(ana, r2.ID, chain.AnswerApprove); err != nil || w.state(r2.ID) != chain.Approved {
		t.Errorf("same target: %v, %s", err, w.state(r2.ID))
	}
	// a target that can not be read approves nothing (fail closed)
	r3 := w.open(openOpts{target: `{"pr":9}`})
	w.tg.err = errors.New("github down")
	if _, err := w.answer(ana, r3.ID, chain.AnswerApprove); !errors.Is(err, approvals.ErrNoVerify) || w.state(r3.ID) != chain.Open {
		t.Errorf("unreadable target: %v, %s", err, w.state(r3.ID))
	}
	// ...but a denial stands
	if _, err := w.answer(ana, r3.ID, chain.AnswerDeny); err != nil || w.state(r3.ID) != chain.Denied {
		t.Errorf("deny with an unreadable target: %v, %s", err, w.state(r3.ID))
	}
}

// CA-APROV-FLUXO-07
func TestAnApprovalThatAsksThePINAppliesOnlyAfterTheRightPIN(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	owner := w.owner
	if err := w.svc.SetPIN(w.ctx, owner.ID, "123456"); err != nil {
		t.Fatal(err)
	}
	r := w.open(openOpts{typ: classes.RequestGuildEntry, min: classes.LevelOwner, pin: true})
	rep, err := w.answer(owner, r.ID, chain.AnswerApprove)
	if err != nil || rep.PIN == "" {
		t.Fatalf("the button: %+v, %v", rep, err)
	}
	if w.state(r.ID) != chain.Open || len(w.ch.decisions()) != 0 {
		t.Fatal("the button alone approved a request that asks the PIN")
	}
	// the PIN of another user's request does not apply it
	if _, err := w.flow.ConfirmPIN(w.ctx, w.add("Ana", users.Administrator, "loja").ID, rep.PIN); !errors.Is(err, approvals.ErrPIN) {
		t.Errorf("another user: %v", err)
	}
	// not confirmed by CheckPIN: the request of PIN is not 'done'
	if _, err := w.flow.ConfirmPIN(w.ctx, owner.ID, rep.PIN); !errors.Is(err, approvals.ErrPIN) || w.state(r.ID) != chain.Open {
		t.Fatalf("without the PIN: %v, %s", err, w.state(r.ID))
	}
	if err := w.svc.CheckPIN(w.ctx, rep.PIN, "123456"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.flow.ConfirmPIN(w.ctx, owner.ID, rep.PIN); err != nil || w.state(r.ID) != chain.Approved {
		t.Fatalf("with the PIN: %v, %s", err, w.state(r.ID))
	}
	// once: the PIN is spent
	if _, err := w.flow.ConfirmPIN(w.ctx, owner.ID, rep.PIN); !errors.Is(err, approvals.ErrPIN) {
		t.Errorf("the PIN twice: %v", err)
	}
	// a denial asks no PIN
	r2 := w.open(openOpts{typ: classes.RequestGuildEntry, min: classes.LevelOwner, pin: true})
	if rep, err := w.answer(owner, r2.ID, chain.AnswerDeny); err != nil || rep.PIN != "" || w.state(r2.ID) != chain.Denied {
		t.Errorf("deny: %+v, %v, %s", rep, err, w.state(r2.ID))
	}
	// a user with no PIN can not approve what asks it
	r3 := w.open(openOpts{typ: classes.RequestGuildEntry, min: classes.LevelOwner, pin: true})
	w2 := newWorld(t)
	r4 := w2.open(openOpts{pin: true})
	if _, err := w2.answer(w2.owner, r4.ID, chain.AnswerApprove); !errors.Is(err, approvals.ErrPIN) {
		t.Errorf("no PIN: %v", err)
	}
	_ = r3
}

func TestAChainThatRefusesLetsTheUserAnswerAgain(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Approver, "loja")
	r := w.open(openOpts{})
	w.ch.failNext = errors.New("recusado")
	if _, err := w.answer(ana, r.ID, chain.AnswerApprove); err == nil {
		t.Fatal("the refusal was swallowed")
	}
	if _, err := w.answer(ana, r.ID, chain.AnswerApprove); err != nil || w.state(r.ID) != chain.Approved {
		t.Errorf("again: %v, %s", err, w.state(r.ID))
	}
}

func TestAskingAgainUpdatesTheQuestionAndAskBackKeepsItOpen(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Approver, "loja")
	bia := w.add("Bia", users.Approver, "blog")
	w.msg.Unreachable(bia.ID)
	r := w.open(openOpts{})
	n := w.msg.Sent()
	q := chain.PeopleQuestion{Request: r, Text: "Texto novo", Approvals: 1, MinLevel: classes.LevelApprover, Answers: []chain.Answer{chain.AnswerApprove}}
	if err := w.flow.Ask(w.ctx, q); err != nil {
		t.Fatal(err)
	}
	if w.msg.Sent() != n {
		t.Error("a second Ask sent the question again")
	}
	if got := w.textTo(ana); got != "Texto novo" {
		t.Errorf("the question = %q", got)
	}
	if _, err := w.answer(ana, r.ID, chain.AnswerAskBack); err != nil || w.state(r.ID) != chain.Open {
		t.Errorf("ask back: %v, %s", err, w.state(r.ID))
	}
	// asking back is not an answer: Ana still approves
	if _, err := w.answer(ana, r.ID, chain.AnswerApprove); err != nil {
		t.Error(err)
	}
}
