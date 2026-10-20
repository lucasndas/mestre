package approvals_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/users"
)

// approvePattern is the tap on Aprovar o padrão and the PIN after it.
func (w *world) approvePattern(u users.User, request int64, pattern string) approvals.Reply {
	w.t.Helper()
	rep, err := w.flow.Answer(w.ctx, u.ID, approvals.Input{Request: request, Answer: chain.AnswerApprovePattern, Pattern: pattern})
	if err != nil || rep.PIN == "" {
		w.t.Fatalf("approve the pattern: %+v, %v", rep, err)
	}
	if err := w.svc.CheckPIN(w.ctx, rep.PIN, "123456"); err != nil {
		w.t.Fatal(err)
	}
	done, err := w.flow.ConfirmPIN(w.ctx, u.ID, rep.PIN)
	if err != nil {
		w.t.Fatalf("confirm the PIN: %v", err)
	}
	return done
}

// CA-APROV-FLUXO-13: three requests open with the people, of the same table, for
// npm install a, b and c, are one message with the three actions; an
// Administrator who taps Aprovar o padrão "npm install *" and confirms the PIN
// approves the three and makes one decree of the pattern; a request whose target
// changed before leaves the group and stays open, with a message of its own.
func TestSimilarRequestsAreOneQuestionAndThePatternApprovesTheOnesItCovers(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T, cTarget string) (*world, users.User, [3]chain.Request) {
		w := newWorld(t)
		ana := w.add("Ana", users.Administrator, "loja")
		if err := w.svc.SetPIN(w.ctx, ana.ID, "123456"); err != nil {
			t.Fatal(err)
		}
		a := w.open(openOpts{action: "npm install a"})
		b := w.open(openOpts{action: "npm install b"})
		c := w.open(openOpts{action: "npm install c", target: cTarget})
		return w, ana, [3]chain.Request{a, b, c}
	}

	w, ana, rs := setup(t, "")
	// one message for each user (the Owner and Ana), not one for each request
	if got := w.msg.Sent(); got != 2 {
		t.Fatalf("%d messages were sent; want 2 (one question for each of the two users)", got)
	}
	text := w.textTo(ana)
	for _, want := range []string{"npm install a", "npm install b", "npm install c", "#" + itoa(rs[0].ID), "#" + itoa(rs[2].ID)} {
		contains(t, text, want)
	}
	w.approvePattern(ana, rs[0].ID, "npm install *")
	for _, r := range rs {
		if got := w.state(r.ID); got != chain.Approved {
			t.Errorf("request %d is %s; want approved", r.ID, got)
		}
	}
	var patterns, plain int
	for _, d := range w.ch.Decisions() {
		switch d.Answer {
		case chain.AnswerApprovePattern:
			patterns++
			if d.Pattern != "npm install *" {
				t.Errorf("the pattern of the decree is %q", d.Pattern)
			}
		case chain.AnswerApprove:
			plain++
		}
	}
	if patterns != 1 || plain != 2 {
		t.Errorf("decisions: %d with the pattern and %d plain; want one decree of the pattern and the other two approved by it", patterns, plain)
	}
	if got := w.textTo(ana); !strings.Contains(got, "Você respondeu: Aprovado") {
		t.Errorf("the shared message ends as %q", got)
	}

	// the target of npm install c changed before the tap: it leaves the group and stays open
	w, ana, rs = setup(t, `{"cmd":"npm install c"}`)
	w.tg.set(rs[2].ID, `{"cmd":"npm install c --global"}`)
	w.approvePattern(ana, rs[0].ID, "npm install *")
	if a, b := w.state(rs[0].ID), w.state(rs[1].ID); a != chain.Approved || b != chain.Approved {
		t.Errorf("the others are %s and %s; want approved", a, b)
	}
	if got := w.state(rs[2].ID); got != chain.Open {
		t.Fatalf("the request that changed is %s; want it open", got)
	}
	_, own, ok := w.msg.To(ana.ID)
	if !ok || own.Request.ID != rs[2].ID {
		t.Fatalf("the last message of Ana is for request %d; want a message of its own for %d", own.Request.ID, rs[2].ID)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// Aprovar agora and Negar on the shared question stand for every request listed;
// a request of another table, another class or another risk is not put in the
// question; and a request that arrives while a PIN waits is asked apart, so the
// PIN approves only what the user read.
func TestTheAnswerOfAGroupStandsForTheListedRequestsAndNoOtherJoinsIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Administrator, "loja")
	if err := w.svc.SetPIN(w.ctx, ana.ID, "123456"); err != nil {
		t.Fatal(err)
	}
	a := w.open(openOpts{action: "npm install a"})
	b := w.open(openOpts{action: "npm install b"})
	// not alike: another table, another class, a command with no pattern in common
	otherTable := w.open(openOpts{action: "npm install t", table: "43-busca"})
	otherClass := w.open(openOpts{action: "npm install k", class: "qa"})
	unlike := w.open(openOpts{action: "make deploy"})
	if got := w.msg.Sent(); got != 2*4 { // the group (a, b) and the three alone, for each of the two users
		t.Fatalf("%d messages were sent; want 8", got)
	}
	if rep, err := w.flow.Answer(w.ctx, ana.ID, approvals.Input{Request: a.ID, Answer: chain.AnswerDeny}); err != nil || rep.Text != "Negado." {
		t.Fatalf("deny: %+v, %v", rep, err)
	}
	for _, r := range []chain.Request{a, b} {
		if got := w.state(r.ID); got != chain.Denied {
			t.Errorf("request %d is %s; want denied with the group", r.ID, got)
		}
	}
	for _, r := range []chain.Request{otherTable, otherClass, unlike} {
		if got := w.state(r.ID); got != chain.Open {
			t.Errorf("request %d (not alike) is %s; want it open", r.ID, got)
		}
	}

	// a PIN waits for the pattern of two requests; a third arrives meanwhile
	w = newWorld(t)
	ana = w.add("Ana", users.Administrator, "loja")
	if err := w.svc.SetPIN(w.ctx, ana.ID, "123456"); err != nil {
		t.Fatal(err)
	}
	x := w.open(openOpts{action: "npm install x"})
	y := w.open(openOpts{action: "npm install y"})
	rep, err := w.flow.Answer(w.ctx, ana.ID, approvals.Input{Request: x.ID, Answer: chain.AnswerApprovePattern, Pattern: "npm install *"})
	if err != nil || rep.PIN == "" {
		t.Fatalf("pattern: %+v, %v", rep, err)
	}
	late := w.open(openOpts{action: "npm install z"})
	if err := w.svc.CheckPIN(w.ctx, rep.PIN, "123456"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.flow.ConfirmPIN(w.ctx, ana.ID, rep.PIN); err != nil {
		t.Fatal(err)
	}
	if w.state(x.ID) != chain.Approved || w.state(y.ID) != chain.Approved {
		t.Errorf("the two read by the user are %s and %s; want approved", w.state(x.ID), w.state(y.ID))
	}
	if got := w.state(late.ID); got != chain.Open {
		t.Errorf("the request that arrived while the PIN waited is %s; want it open", got)
	}
}
