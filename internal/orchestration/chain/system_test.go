package chain_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/users"
)

func (w *world) entry() int64 {
	w.t.Helper()
	id, err := w.ch.RequestSystem(w.ctx, chain.SystemRequest{Type: classes.RequestGuildEntry, About: `Entrada na Guilda: a máquina "notebook"`,
		Target: json.RawMessage(`{"join_id":"j1","code":"123 456"}`)})
	if err != nil {
		w.t.Fatal(err)
	}
	return id
}

// CA-APROV-POL-12: the request to enter the Guild is the engine's own: no
// workspace, no agent in front, only the Owner is asked, with two answers, and the
// PIN after the button; not even a delegation of the Owner lets the Grand Master
// decide it, and a policy file that cites promocao_cidadela is invalid.
func TestARequestToEnterTheGuildGoesOnlyToTheOwnerWhoApprovesOrDeniesWithThePIN(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{flow: true})
	ana, err := w.usvc.Create(w.ctx, w.owner, users.NewUser{Name: "Ana", Level: users.Administrator, Workspaces: []string{w.slug}})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.usvc.SetPIN(w.ctx, w.owner.ID, "123456"); err != nil {
		t.Fatal(err)
	}
	id := w.entry()
	r := w.request(id)
	if r.State != chain.Open || r.Level.Kind != chain.LevelPeople || r.Workspace != "" || r.Requester != "" || r.Risk.Risk != classes.RiskFixed {
		t.Fatalf("the request = %+v", r)
	}
	if _, _, ok := w.chn.To(ana.ID); ok {
		t.Error("an Administrator was asked about the entry in the Guild")
	}
	_, msg, ok := w.chn.To(w.owner.ID)
	if !ok {
		t.Fatal("the Owner was not asked")
	}
	if len(msg.Answers) != 2 || msg.Answers[0] != chain.AnswerApprove || msg.Answers[1] != chain.AnswerDeny {
		t.Errorf("the Owner was offered %v, want only approve and deny", msg.Answers)
	}
	if !strings.Contains(msg.Text, "123 456") && !strings.Contains(msg.Text, "notebook") {
		t.Errorf("the question does not say what is asked: %q", msg.Text)
	}
	// a delegation of the Owner does not give it to the Grand Master
	w.deleg.set(true)
	if ref := w.refusal(chain.Decision{Request: id, By: w.gm.ID, Answer: chain.AnswerApprove}); ref.Code != chain.CodeHumanRequired && ref.Code != chain.CodeNotAtLevel {
		t.Errorf("the Grand Master deciding the entry: %+v", ref)
	}
	if _, _, err := chain.ParsePolicyFile([]byte("versao: 1\ntipos:\n  promocao_cidadela: { sozinho: nunca }\n")); err == nil {
		t.Error("a policy file that cites promocao_cidadela was accepted")
	}
	// nobody else answers it, and the Owner answers nothing else
	if _, err := w.flow.Answer(w.ctx, ana.ID, approvals.Input{Request: id, Answer: chain.AnswerApprove}); !errors.Is(err, approvals.ErrNotYours) {
		t.Errorf("an Administrator answering: %v", err)
	}
	for _, a := range []chain.Answer{chain.AnswerApproveAlways, chain.AnswerApprovePattern, chain.AnswerDelegate} {
		if rep, err := w.flow.Answer(w.ctx, w.owner.ID, approvals.Input{Request: id, Answer: a, Pattern: "x *"}); !errors.Is(err, approvals.ErrLevel) || rep.PIN != "" {
			t.Errorf("%s: %+v, %v, want it refused before the PIN", a, rep, err)
		}
	}
	// the chain refuses it too, whoever calls
	for _, a := range []chain.Answer{chain.AnswerApproveAlways, chain.AnswerDelegate, chain.AnswerAskBack, chain.AnswerClimb, chain.AnswerApproveFor} {
		if _, err := w.ch.DecideByPerson(w.ctx, chain.PersonDecision{Request: id, User: w.owner.ID, Level: string(users.Owner), Answer: a, Pattern: "x *"}); err == nil {
			t.Errorf("the chain applied %s to an entry", a)
		}
	}
	rep, err := w.flow.Answer(w.ctx, w.owner.ID, approvals.Input{Request: id, Answer: chain.AnswerApprove})
	if err != nil || rep.PIN == "" {
		t.Fatalf("the button: %+v, %v", rep, err)
	}
	if got := w.entries.results(); len(got) != 0 {
		t.Fatalf("the button alone told the owner of the type: %+v", got)
	}
	if err := w.usvc.CheckPIN(w.ctx, rep.PIN, "123456"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.flow.ConfirmPIN(w.ctx, w.owner.ID, rep.PIN); err != nil {
		t.Fatal(err)
	}
	got := w.entries.results()
	if len(got) != 1 || !got[0].Approved || got[0].Request.ID != id || got[0].Request.Decision == nil || got[0].Request.Decision.By != "user:"+w.owner.ID ||
		!strings.Contains(string(got[0].Request.Target), "j1") {
		t.Fatalf("the owner of the type was told %+v", got)
	}
}

func TestADeniedEntryAsksNoPINAndTellsTheOwnerOfTheType(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{flow: true})
	id := w.entry()
	rep, err := w.flow.Answer(w.ctx, w.owner.ID, approvals.Input{Request: id, Answer: chain.AnswerDeny})
	if err != nil || rep.PIN != "" {
		t.Fatalf("deny: %+v, %v", rep, err)
	}
	got := w.entries.results()
	if len(got) != 1 || got[0].Approved || got[0].Request.State != chain.Denied {
		t.Fatalf("told %+v", got)
	}
}

// The entry waits the hour of the entry, whatever the file of the policy says,
// and then it is over: never approved on its own.
func TestAnEntryExpiresInTheHourOfTheEntry(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{flow: true})
	id := w.entry()
	w.clk.Advance(chain.GuildEntryValidity - time.Second)
	if w.request(id).State != chain.Open {
		t.Fatal("it expired before the hour")
	}
	w.clk.Advance(time.Second)
	if w.request(id).State != chain.Expired {
		t.Fatalf("state = %s, want expired", w.request(id).State)
	}
	if got := w.entries.results(); len(got) != 1 || got[0].Approved || got[0].Request.State != chain.Expired {
		t.Fatalf("told %+v", got)
	}
}

func TestAnEntryTakenDownIsCancelledAndOnlyTheTypesOfTheEngineAreOpenedThisWay(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{flow: true})
	id := w.entry()
	if err := w.ch.Withdraw(w.ctx, id, "acabou"); err != nil {
		t.Fatal(err)
	}
	if w.request(id).State != chain.Cancelled {
		t.Fatalf("state = %s, want cancelled", w.request(id).State)
	}
	if err := w.ch.Withdraw(w.ctx, id, "de novo"); err != nil {
		t.Errorf("taking down a closed request: %v", err)
	}
	if _, err := w.flow.Answer(w.ctx, w.owner.ID, approvals.Input{Request: id, Answer: chain.AnswerApprove}); !errors.Is(err, approvals.ErrNotOpen) {
		t.Errorf("an answer after it was taken down: %v", err)
	}
	for _, typ := range []classes.RequestType{classes.RequestClassPermission, classes.RequestPR, classes.RequestOperator, classes.RequestDestructive, ""} {
		if _, err := w.ch.RequestSystem(w.ctx, chain.SystemRequest{Type: typ}); !errors.Is(err, chain.ErrNotSystem) {
			t.Errorf("type %q opened as a request of the engine: %v", typ, err)
		}
	}
}

// Taking a question down never cancels a request of another kind.
func TestWithdrawingAnEntryCannotCancelAnotherKindOfRequest(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{flow: true})
	id := w.shell(w.recruit("Dev"), "npm install lodash")
	if err := w.ch.Withdraw(w.ctx, id, "x"); !errors.Is(err, chain.ErrNotSystem) {
		t.Fatalf("Withdraw of a permission request = %v", err)
	}
	if w.request(id).State != chain.Open {
		t.Fatal("the permission request was cancelled")
	}
}
