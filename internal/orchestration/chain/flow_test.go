package chain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/users"
)

// peopleRequest takes a request of a command up to the people and gives the
// users the flow will ask: an Approver of the workspace and the Owner.
func peopleRequest(t *testing.T, w *world) (id int64, ana users.User) {
	t.Helper()
	ana, err := w.usvc.Create(w.ctx, w.owner, users.NewUser{Name: "Ana", Level: users.Approver, Workspaces: []string{w.slug}})
	if err != nil {
		t.Fatal(err)
	}
	id = w.shell(w.recruit("Dev"), "npm install lodash")
	w.clk.Advance(12 * time.Minute) // the agent levels: 2 + 10 min
	if w.level(id) != chain.LevelPeople {
		t.Fatalf("the request is at %+v, want the people", w.request(id).Level)
	}
	return id, ana
}

// CA-APROV-FLUXO-04
func TestARequestThatNobodyAnswersExpiresAndTheQuestionsSayIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{flow: true})
	// the time of the people is 24 h, as the criterion says (the default file
	// gives 2 h only to the permission of a command)
	st, _, _ := w.policy.Chain(w.ctx, w.slug)
	st.PeopleDeadlines = map[classes.RequestType]time.Duration{}
	w.policy.set(st)
	id, ana := peopleRequest(t, w)
	if got := w.request(id).Deadline.Sub(w.clk.Now()); got != 24*time.Hour {
		t.Fatalf("the time of the people = %s, want 24h", got)
	}
	ref, _, ok := w.chn.To(ana.ID)
	if !ok {
		t.Fatal("Ana was not asked")
	}
	asked := w.chn.Sent()
	w.clk.Advance(24*time.Hour - time.Second)
	if w.request(id).State != chain.Open {
		t.Fatal("it expired before the time of the people")
	}
	w.clk.Advance(time.Second)
	if w.request(id).State != chain.Expired {
		t.Fatalf("state = %s, want expired", w.request(id).State)
	}
	if txt, buttons := w.chn.Read(ref); !strings.HasPrefix(txt, "Expirado") || buttons {
		t.Errorf("the question after the expiry = %q (buttons %v)", txt, buttons)
	}
	if w.chn.Sent() != asked {
		t.Error("a new question was sent after the expiry")
	}
	var told bool
	for _, it := range w.itemsOf(w.get(w.request(id).Requester), inbox.KindWake) {
		told = told || strings.Contains(it.Text, "negado")
	}
	if !told {
		t.Error("the requester was not told the request was denied")
	}
	// the answer that comes late is refused
	if _, err := w.flow.Answer(w.ctx, ana.ID, approvals.Input{Request: id, Answer: chain.AnswerApprove}); !errors.Is(err, approvals.ErrNotOpen) {
		t.Errorf("an answer after the expiry: %v", err)
	}
}

// CA-APROV-FLUXO-14
func TestAnApproverOfAClassPermissionHasOnlyTheAnswersOfItsLevel(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{flow: true})
	id, ana := peopleRequest(t, w)
	_, msg, _ := w.chn.To(ana.ID)
	want := map[chain.Answer]bool{chain.AnswerApprove: true, chain.AnswerDeny: true, chain.AnswerDenyInstruction: true, chain.AnswerAskBack: true}
	for _, a := range msg.Answers {
		if !want[a] {
			t.Errorf("the Approver was offered %q", a)
		}
		delete(want, a)
	}
	if len(want) != 0 {
		t.Errorf("missing answers: %v", want)
	}
	if _, err := w.flow.Answer(w.ctx, ana.ID, approvals.Input{Request: id, Answer: chain.AnswerApproveAlways}); !errors.Is(err, approvals.ErrLevel) {
		t.Errorf("approve always by an Approver: %v", err)
	}
	if w.request(id).State != chain.Open {
		t.Fatal("a refused answer changed the request")
	}
	if _, err := w.flow.Answer(w.ctx, ana.ID, approvals.Input{Request: id, Answer: chain.AnswerDenyInstruction, Instruction: "use pnpm"}); err != nil {
		t.Fatal(err)
	}
	r := w.request(id)
	if r.State != chain.Denied {
		t.Fatalf("state = %s, want denied", r.State)
	}
	var instructed bool
	for _, it := range w.itemsOf(w.get(r.Requester), inbox.KindWake) {
		instructed = instructed || strings.Contains(it.Text, "use pnpm")
	}
	if !instructed {
		t.Error("the requester did not receive the instruction")
	}
}

// Part of CA-APROV-POL-11: the types that ask the PIN after the button are the
// fixed ones, always, and those the file marks with pin.
func TestTheTypesThatAskThePINAfterTheButton(t *testing.T) {
	t.Parallel()
	rules, err := chain.ParsePolicyRules([]byte("versao: 1\ntipos:\n  pr: { pin: true }\n"))
	if err != nil {
		t.Fatal(err)
	}
	for typ, want := range map[classes.RequestType]bool{
		classes.RequestOperator: true, classes.RequestGuildEntry: true, classes.RequestCitadelPromotion: true, classes.RequestRepoSettings: true,
		classes.RequestPR: true, classes.RequestMerge: false, classes.RequestClassPermission: false,
	} {
		if got := rules.NeedsPIN(chain.Request{Type: typ}); got != want {
			t.Errorf("NeedsPIN(%s) = %v, want %v", typ, got, want)
		}
	}
	if chain.DefaultRules().NeedsPIN(chain.Request{Type: classes.RequestOperator}) != true || chain.DefaultRules().NeedsPIN(chain.Request{Type: classes.RequestPR}) {
		t.Error("the default rules ask the wrong PINs")
	}
}
