package approvals_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/approvals"
	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/notices"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/users"
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
	if n := len(w.ch.Decisions()); n != 1 {
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
	if err != nil || i18n.Render(i18n.PtBR, rep.Msg) != "Sua aprovação foi contada: 1 de 2." {
		t.Fatalf("Ana: %+v, %v", rep, err)
	}
	if _, err := w.answer(ana, r.ID, chain.AnswerApprove); !errors.Is(err, approvals.ErrAnswered) {
		t.Errorf("Ana twice: %v", err)
	}
	contains(t, w.textTo(bia), "1 de 2 aprovações")
	if _, err := w.answer(bia, r.ID, chain.AnswerApprove); err != nil {
		t.Fatal(err)
	}
	if w.state(r.ID) != chain.Open || len(w.ch.Decisions()) != 0 {
		t.Fatalf("two approvals without Lucas decided: %s", w.state(r.ID))
	}
	// the state `aguardando_usuarios` is an open request at the level of the people
	if got, err := w.ch.Get(w.ctx, r.ID); err != nil || got.State != chain.Open || got.Level.Kind != chain.LevelPeople {
		t.Fatalf("after Ana and Bia the request is %+v (%v), want aguardando_usuarios (open, people)", got, err)
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
// CA-USR-05
//
// Part of CA-USR-05: the half of the criterion that needs the approvals (the
// action is not executed after the suspension); the marker and the
// invalidation by the Owner are in internal/users.
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
	// the action does not run: the chain recorded no decision at all
	if n := len(w.ch.Decisions()); n != 0 {
		t.Errorf("the chain recorded %d decisions, want none (the action must not run)", n)
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
	if w.state(r.ID) != chain.Open || len(w.ch.Decisions()) != 0 {
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
	r := w.open(openOpts{typ: classes.RequestPR, target: `{"repo":"lucas/loja","pr":7,"head":"aaa"}`})
	w.tg.set(r.ID, `{"head":"bbb","pr":7,"repo":"lucas/loja"}`) // same keys, another head
	if _, err := w.answer(ana, r.ID, chain.AnswerApprove); !errors.Is(err, approvals.ErrTargetChanged) {
		t.Fatalf("the answer: %v", err)
	}
	if w.state(r.ID) != chain.Invalidated || len(w.ch.Decisions()) != 0 {
		t.Fatalf("state = %s, decisions = %d", w.state(r.ID), len(w.ch.Decisions()))
	}
	contains(t, w.textTo(ana), "Invalidado")
	// the same target in another spelling is the same target
	r2 := w.open(openOpts{typ: classes.RequestPR, target: `{"repo":"lucas/loja","pr":8,"head":"aaa"}`})
	w.tg.set(r2.ID, `{ "head": "aaa", "pr": 8, "repo": "lucas/loja" }`)
	if _, err := w.answer(ana, r2.ID, chain.AnswerApprove); err != nil || w.state(r2.ID) != chain.Approved {
		t.Errorf("same target: %v, %s", err, w.state(r2.ID))
	}
	// a target that can not be read approves nothing (fail closed)
	r3 := w.open(openOpts{typ: classes.RequestPR, target: `{"pr":9}`})
	w.tg.err = errors.New("github down")
	if _, err := w.answer(ana, r3.ID, chain.AnswerApprove); !errors.Is(err, approvals.ErrNoVerify) || w.state(r3.ID) != chain.Open {
		t.Errorf("unreadable target: %v, %s", err, w.state(r3.ID))
	}
	// ...but a denial stands
	if _, err := w.answer(ana, r3.ID, chain.AnswerDeny); err != nil || w.state(r3.ID) != chain.Denied {
		t.Errorf("deny with an unreadable target: %v, %s", err, w.state(r3.ID))
	}
}

// CA-APROV-FLUXO-01
// The Dado of the criterion: a request already approved for one target (it
// counts, 1 of 2) and the target changes afterwards.
func TestAnApprovalAlreadyCountedForTheOldTargetIsDroppedWhenTheTargetMoves(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Approver, "loja")
	bia := w.add("Bia", users.Approver, "loja")
	r := w.open(openOpts{typ: classes.RequestPR, quorum: 2, target: `{"repo":"lucas/loja","pr":7,"head":"aaa"}`})
	w.tg.set(r.ID, `{"head":"aaa","pr":7,"repo":"lucas/loja"}`) // still the target that was asked
	if _, err := w.answer(ana, r.ID, chain.AnswerApprove); err != nil || w.state(r.ID) != chain.Open {
		t.Fatalf("the first approval at head aaa: %v, %s; want it counted (1 of 2, still open)", err, w.state(r.ID))
	}
	w.tg.set(r.ID, `{"head":"bbb","pr":7,"repo":"lucas/loja"}`) // the head moves
	if _, err := w.answer(bia, r.ID, chain.AnswerApprove); !errors.Is(err, approvals.ErrTargetChanged) {
		t.Fatalf("the second answer after the move: %v", err)
	}
	if w.state(r.ID) != chain.Invalidated || len(w.ch.Decisions()) != 0 {
		t.Fatalf("state = %s, decisions = %d; want invalidated and nothing applied", w.state(r.ID), len(w.ch.Decisions()))
	}
	// the approval of Ana does not count for anyone any more
	if _, err := w.answer(ana, r.ID, chain.AnswerApprove); err == nil {
		t.Error("an invalidated request took another answer")
	}
	if len(w.ch.Decisions()) != 0 {
		t.Errorf("a decision was recorded after the invalidation: %d", len(w.ch.Decisions()))
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
	if w.state(r.ID) != chain.Open || len(w.ch.Decisions()) != 0 {
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
	// a wrong PIN confirms nothing
	if err := w.svc.CheckPIN(w.ctx, rep.PIN, "654321"); err == nil {
		t.Fatal("a wrong PIN was accepted")
	}
	if _, err := w.flow.ConfirmPIN(w.ctx, owner.ID, rep.PIN); !errors.Is(err, approvals.ErrPIN) || w.state(r.ID) != chain.Open {
		t.Fatalf("after a wrong PIN: %v, %s", err, w.state(r.ID))
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
	w2 := newWorld(t)
	r4 := w2.open(openOpts{pin: true})
	if _, err := w2.answer(w2.owner, r4.ID, chain.AnswerApprove); !errors.Is(err, approvals.ErrPIN) {
		t.Errorf("no PIN: %v", err)
	}
}

func TestAChainThatRefusesLetsTheUserAnswerAgain(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Approver, "loja")
	r := w.open(openOpts{})
	w.ch.FailNext(errors.New("recusado"))
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
	if got := w.textTo(ana); !strings.Contains(got, "\nTexto novo\n") {
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

func TestAVoteOfWhoWasSuspendedMeanwhileDoesNotMakeTheQuorum(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Approver, "loja")
	bia := w.add("Bia", users.Approver, "loja")
	r := w.open(openOpts{quorum: 2})
	if _, err := w.answer(ana, r.ID, chain.AnswerApprove); err != nil {
		t.Fatal(err)
	}
	// Ana is suspended and the invalidation did not run yet (it is a race)
	if _, err := w.db.Writer().ExecContext(w.ctx, `UPDATE users SET state = 'suspended' WHERE id = ?`, ana.ID); err != nil {
		t.Fatal(err)
	}
	rep, err := w.answer(bia, r.ID, chain.AnswerApprove)
	if err != nil || w.state(r.ID) != chain.Open || len(w.ch.Decisions()) != 0 {
		t.Fatalf("Bia: %+v, %v, %s: the vote of a suspended user made the quorum", rep, err, w.state(r.ID))
	}
	if i18n.Render(i18n.PtBR, rep.Msg) != "Sua aprovação foi contada: 1 de 2." {
		t.Errorf("the count = %q", i18n.Render(i18n.PtBR, rep.Msg))
	}
	// the vote stays in the record, revoked
	var counts, revoked int64
	if err := w.db.Reader().QueryRowContext(w.ctx, `SELECT counts, revoked_at FROM approval_votes WHERE request = ? AND user_id = ?`, r.ID, ana.ID).Scan(&counts, &revoked); err != nil || counts != 0 || revoked == 0 {
		t.Errorf("the vote of Ana: counts %d, revoked %d, %v", counts, revoked, err)
	}
}

func TestALateClickApprovesNothingEvenBeforeTheTimerFires(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Approver, "loja")
	r := w.open(openOpts{late: true})
	if _, err := w.answer(ana, r.ID, chain.AnswerApprove); !errors.Is(err, approvals.ErrExpired) || len(w.ch.Decisions()) != 0 {
		t.Errorf("a late click: %v", err)
	}
}

func TestWithMoreThanOneApprovalOnlyThePlainApprovalCounts(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Administrator, "loja")
	r := w.open(openOpts{quorum: 2})
	for _, a := range []chain.Answer{chain.AnswerApproveAlways, chain.AnswerApprovePattern, chain.AnswerApproveFor} {
		if _, err := w.answer(ana, r.ID, a); !errors.Is(err, approvals.ErrQuorumEffect) {
			t.Errorf("%s: %v", a, err)
		}
	}
	if _, err := w.answer(w.owner, r.ID, chain.AnswerDelegate); !errors.Is(err, approvals.ErrQuorumEffect) {
		t.Errorf("delegate: %v", err)
	}
	if _, err := w.answer(ana, r.ID, chain.AnswerApprove); err != nil {
		t.Error(err)
	}
}

func TestARequestThatMustBeBoundToATargetIsNotApprovedWithoutOne(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Approver, "loja")
	for _, typ := range []classes.RequestType{classes.RequestPR, classes.RequestMerge, classes.RequestDestructive} {
		r := w.open(openOpts{typ: typ, min: classes.LevelApprover})
		if _, err := w.answer(ana, r.ID, chain.AnswerApprove); !errors.Is(err, approvals.ErrNoVerify) || w.state(r.ID) != chain.Open {
			t.Errorf("%s with no target: %v, %s", typ, err, w.state(r.ID))
		}
	}
}

func TestAUserSuspendedWhileTheTargetIsReadVotesNothing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Approver, "loja")
	r := w.open(openOpts{typ: classes.RequestPR, target: `{"pr":7}`})
	w.tg.on = func() {
		if _, err := w.db.Writer().ExecContext(w.ctx, `UPDATE users SET state = 'suspended' WHERE id = ?`, ana.ID); err != nil {
			t.Error(err)
		}
	}
	if _, err := w.answer(ana, r.ID, chain.AnswerApprove); !errors.Is(err, approvals.ErrNotYours) {
		t.Fatalf("the answer: %v", err)
	}
	var n int
	if err := w.db.Reader().QueryRowContext(w.ctx, `SELECT COUNT(*) FROM approval_votes WHERE request = ?`, r.ID).Scan(&n); err != nil || n != 0 || len(w.ch.Decisions()) != 0 {
		t.Errorf("votes %d, decisions %d, %v", n, len(w.ch.Decisions()), err)
	}
}

// CA-VIDA-13: a request put to the people rings the bell of each user who decides it, once, with
// the table filled (never a slug in the text) and the link to the request; whoever does not decide
// it, or does not act in its campaign, is not told.
func TestARequestPutToThePeopleRingsTheBellOfWhoDecidesIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Approver, "loja")
	w.add("Beto", users.Observer, "loja")
	w.add("Caio", users.Approver, "blog")
	r := w.open(openOpts{action: "npm install lodash"})
	got := map[string]notices.Notice{}
	for _, n := range w.bell {
		got[n.UserID] = n
	}
	if len(w.bell) != 2 || got[w.owner.ID].Msg.IsZero() || got[ana.ID].Msg.IsZero() {
		t.Fatalf("the bell rang for %+v; want the Owner and Ana", w.bell)
	}
	n := got[ana.ID]
	if i18n.Render(i18n.PtBR, n.Msg) != "Nova aprovação" || n.Workspace != "loja" || n.Mesa != "42-login" || n.Audience != notices.User ||
		n.LinkKind != notices.LinkRequest || n.LinkRef != strconv.FormatInt(r.ID, 10) {
		t.Errorf("the notice is %+v", n)
	}
	// asking again (the question changed) does not ring again
	q := chain.PeopleQuestion{Request: r, Text: "outra", Approvals: 1, MinLevel: classes.LevelApprover, Answers: []chain.Answer{chain.AnswerApprove}}
	if err := w.flow.Ask(w.ctx, q); err != nil {
		t.Fatal(err)
	}
	if len(w.bell) != 2 {
		t.Errorf("the bell rang again: %d notices", len(w.bell))
	}
}
