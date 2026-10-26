package approvals_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/approvals"
	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/users"
)

// The panel answers with a PIN it checked in the very request: an answer that asks the
// PIN is refused without that check, and nothing is recorded; with it, it is applied at
// once; one that asks none does not need it.
func TestAnAnswerThatAsksThePINIsRefusedWithoutTheCheckOfThePanel(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Administrator, "loja")
	r := w.open(openOpts{pin: true})
	in := approvals.Input{Request: r.ID, Answer: chain.AnswerApprove}

	need, err := w.flow.NeedsPIN(w.ctx, ana.ID, in)
	if err != nil || !need {
		t.Fatalf("NeedsPIN = %v, %v", need, err)
	}
	if _, err := w.flow.AnswerWithPIN(w.ctx, ana.ID, in, false); !errors.Is(err, approvals.ErrPINNeeded) {
		t.Fatalf("without the check: %v", err)
	}
	if w.state(r.ID) != chain.Open {
		t.Fatal("the request changed without the PIN")
	}
	// a refused answer left no vote behind: the user answers again
	if _, err := w.flow.AnswerWithPIN(w.ctx, ana.ID, in, true); err != nil {
		t.Fatal(err)
	}
	if w.state(r.ID) != chain.Approved {
		t.Fatalf("state %s", w.state(r.ID))
	}

	// a denial and a plain approval of a request that asks none need no PIN
	plain := w.open(openOpts{})
	if need, _ := w.flow.NeedsPIN(w.ctx, ana.ID, approvals.Input{Request: plain.ID, Answer: chain.AnswerApprove}); need {
		t.Fatal("a plain approval asks the PIN")
	}
	if need, _ := w.flow.NeedsPIN(w.ctx, ana.ID, approvals.Input{Request: r.ID, Answer: chain.AnswerDeny}); need {
		t.Fatal("a denial asks the PIN")
	}
	if _, err := w.flow.AnswerWithPIN(w.ctx, ana.ID, approvals.Input{Request: plain.ID, Answer: chain.AnswerApprove}, false); err != nil {
		t.Fatal(err)
	}
}

// The rules of the answer stay: the panel checks the user, the level and the state
// even when it vouches for the PIN.
func TestAnAnswerWithThePINOfThePanelStillChecksTheUserAndTheLevel(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	obs := w.add("Otto", users.Observer, "loja")
	other := w.add("Bia", users.Approver, "blog")
	r := w.open(openOpts{pin: true})
	for _, u := range []users.User{obs, other} {
		if _, err := w.flow.AnswerWithPIN(w.ctx, u.ID, approvals.Input{Request: r.ID, Answer: chain.AnswerApprove}, true); !errors.Is(err, approvals.ErrNotYours) {
			t.Fatalf("%s: %v", u.Name, err)
		}
	}
	if w.state(r.ID) != chain.Open {
		t.Fatal("the request changed")
	}
}

// What the panel reads is what the flow keeps: the open questions of the user in the
// workspaces he acts in, newest first, those he can not answer left out.
func TestThePendingQuestionsAreThoseTheUserMayAnswer(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Approver, "loja")
	obs := w.add("Otto", users.Observer, "loja")
	blog := w.add("Bia", users.Approver, "blog")
	first := w.open(openOpts{})
	second := w.open(openOpts{min: classes.LevelAdmin})
	got, err := w.flow.PendingFor(w.ctx, ana)
	if err != nil || len(got) != 1 || got[0].Request.ID != first.ID {
		t.Fatalf("Ana: %v %v", got, err)
	}
	if got, _ := w.flow.PendingFor(w.ctx, w.owner); len(got) != 2 || got[0].Request.ID != second.ID {
		t.Fatalf("Owner: %v", got)
	}
	for _, u := range []users.User{obs, blog} {
		if got, _ := w.flow.PendingFor(w.ctx, u); len(got) != 0 {
			t.Fatalf("%s sees %v", u.Name, got)
		}
		if _, err := w.flow.Question(w.ctx, u, first.ID); !errors.Is(err, approvals.ErrNotYours) {
			t.Fatalf("%s reads it: %v", u.Name, err)
		}
	}
	if n, _ := w.flow.Pendings(w.ctx, ana); n != 1 {
		t.Fatalf("count %d", n)
	}
	// once decided it is no longer pending, and the closure says who decided
	if _, err := w.answer(ana, first.ID, chain.AnswerApprove); err != nil {
		t.Fatal(err)
	}
	if n, _ := w.flow.Pendings(w.ctx, ana); n != 0 {
		t.Fatalf("count %d", n)
	}
	q, err := w.flow.Question(w.ctx, ana, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	c := w.flow.Closure(w.ctx, q.Request)
	if c.State != chain.Approved || c.By != "Ana" || c.ByID != ana.ID || c.At.IsZero() {
		t.Fatalf("closure %+v", c)
	}
}

// The panel hears of every change: a question put, a vote counted and one taken back.
func TestTheFlowTellsTheListenerOfEachChange(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	var mu sync.Mutex
	var seen []int64
	w.flow = rebuild(t, w, func(_ context.Context, id int64) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, id)
	})
	ana := w.add("Ana", users.Approver, "loja")
	bia := w.add("Bia", users.Approver, "loja")
	r := w.open(openOpts{quorum: 2})
	if _, err := w.answer(ana, r.ID, chain.AnswerApprove); err != nil {
		t.Fatal(err)
	}
	if _, err := w.answer(bia, r.ID, chain.AnswerApprove); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	// asked, one vote counted (the question is edited), the request closed (taken back)
	if len(seen) < 3 {
		t.Fatalf("heard %v", seen)
	}
	for _, id := range seen {
		if id != r.ID {
			t.Fatalf("heard %v", seen)
		}
	}
}

func rebuild(t *testing.T, w *world, on func(context.Context, int64)) *approvals.Flow {
	t.Helper()
	f, err := approvals.New(approvals.Options{DB: w.db, Clock: w.clk, Chain: w.ch, Users: w.svc, PINs: w.svc, Messenger: w.msg, Targets: w.tg,
		Locked: func(context.Context) bool { return false }, OnChange: on})
	if err != nil {
		t.Fatal(err)
	}
	w.ch.Flow = f
	return f
}

func TestTheAnswersFollowTheLevelAndTheTypeOfTheRequest(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	apr := w.add("Ana", users.Approver, "loja")
	adm := w.add("Bia", users.Administrator, "loja")
	r := w.open(openOpts{action: "npm install lodash"})
	q, err := w.flow.Question(w.ctx, adm, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	has := func(u users.User, a chain.Answer) bool {
		for _, x := range w.flow.AnswersOf(u, q.Request, q) {
			if x == a {
				return true
			}
		}
		return false
	}
	if !has(adm, chain.AnswerApprovePattern) || has(apr, chain.AnswerApprovePattern) || !has(apr, chain.AnswerApprove) {
		t.Fatal("pattern/approve by level")
	}
	// a request that is not a permission of a class makes no rule for the class
	cost := w.open(openOpts{})
	qc, _ := w.flow.Question(w.ctx, adm, cost.ID)
	for _, a := range w.flow.AnswersOf(adm, qc.Request, qc) {
		if a == chain.AnswerApproveAlways || a == chain.AnswerApprovePattern || a == chain.AnswerApproveFor {
			t.Fatalf("a request of cost is given %s", a)
		}
	}
	// with more than one approval required only the plain approval is left
	two := w.open(openOpts{action: "npm test", quorum: 2})
	q2, _ := w.flow.Question(w.ctx, adm, two.ID)
	for _, a := range w.flow.AnswersOf(adm, q2.Request, q2) {
		if a.Approves() && a != chain.AnswerApprove {
			t.Fatalf("a quorum of two is given %s", a)
		}
	}
}

func TestThePatternTheEngineSuggests(t *testing.T) {
	t.Parallel()
	shell := func(cmd string) chain.Request {
		return chain.Request{Type: classes.RequestClassPermission, Class: "dev", Table: "42-login", Action: &classes.Action{Kind: providers.KindShell, Command: cmd}}
	}
	for cmd, want := range map[string]string{
		"npm install lodash": "npm install *",
		"npm test":           "npm *",
		"git push origin x":  "git push *",
		"ls":                 "",
		"bash -c 'rm -rf /'": "",
	} {
		got := approvals.SuggestPattern(shell(cmd))
		// a suggestion the engine would refuse is never made; one it accepts is the shape above
		if want == "" && got != "" || want != "" && got != "" && got != want {
			t.Errorf("%q: %q, want %q", cmd, got, want)
		}
	}
	if got := approvals.SuggestPattern(shell("npm install lodash")); got != "npm install *" {
		t.Errorf("the pattern is %q", got)
	}
	if got := approvals.SuggestPattern(chain.Request{Type: classes.RequestExtraCost}); got != "" {
		t.Errorf("a request with no action has pattern %q", got)
	}
	read := chain.Request{Type: classes.RequestClassPermission, Action: &classes.Action{Kind: providers.KindRead, Paths: []string{"a"}}}
	if got := approvals.SuggestPattern(read); got != "" {
		t.Errorf("a read has pattern %q", got)
	}
}
