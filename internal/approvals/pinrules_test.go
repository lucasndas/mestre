package approvals_test

import (
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/users"
)

// An answer that makes a permanent rule that frees, or a delegation, asks the PIN
// whatever the policy of the type says; "Negar sempre" only restricts and asks none.
func TestTheAnswersThatMakeRulesAskThePINWhateverThePolicySays(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	adm := w.add("Adm", users.Administrator, "loja")
	if err := w.svc.SetPIN(w.ctx, adm.ID, "123456"); err != nil {
		t.Fatal(err)
	}
	for _, a := range []chain.Answer{chain.AnswerApproveAlways, chain.AnswerApprovePattern, chain.AnswerApproveFor} {
		r := w.open(openOpts{}) // the policy of the request asks no PIN
		rep, err := w.flow.Answer(w.ctx, adm.ID, approvals.Input{Request: r.ID, Answer: a, Pattern: "x *", For: time.Hour})
		if err != nil || rep.PIN == "" || w.state(r.ID) != chain.Open {
			t.Fatalf("%s: %+v, %v (state %s)", a, rep, err, w.state(r.ID))
		}
	}
	r := w.open(openOpts{})
	if rep, err := w.flow.Answer(w.ctx, adm.ID, approvals.Input{Request: r.ID, Answer: chain.AnswerDenyAlways}); err != nil || rep.PIN != "" {
		t.Fatalf("deny always: %+v, %v", rep, err)
	}
}
