package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/testkit/fakelock"
)

type decided struct {
	join string
	d    guild.Decision
}

func newEntryApprover(decide func(context.Context, string, guild.Decision) error, told *[]string) EntryApprover {
	return EntryApprover{
		Chain:  func() *chain.Service { return nil },
		Decide: decide,
		Tell:   func(_ context.Context, text string) { *told = append(*told, text) },
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func approvedBy(by string, state chain.State) chain.Result {
	return chain.Result{Request: chain.Request{ID: 7, Type: classes.RequestGuildEntry, State: state, Target: json.RawMessage(`{"join_id":"j1","name":"notebook"}`),
		Decision: &chain.Closing{State: state, By: by}}, Approved: state == chain.Approved}
}

// With no chain in the period the Owner can not be asked: the Citadel is told
// so and asks again (the entry stays pending), it never lets the machine in.
func TestTheOwnerIsNotAskedAboutAnEntryWhereThereIsNoChainOfApproval(t *testing.T) {
	t.Parallel()
	var told []string
	a := newEntryApprover(nil, &told)
	if id, err := a.Ask(t.Context(), guild.ApprovalRequest{JoinID: "j1", Name: "notebook"}); err == nil || id != "" {
		t.Fatalf("Ask with no chain = %q, %v", id, err)
	}
	a.Withdraw(t.Context(), "7") // nothing to take down, and no panic
	a.Withdraw(t.Context(), "not a number")
}

// What the Owner answers is applied to the join the request names, by the user
// that answered, and only an approval or a denial of a user counts.
func TestTheAnswerOfTheOwnerReachesTheCitadelAndOnlyThatAnswerDoes(t *testing.T) {
	t.Parallel()
	var got []decided
	var told []string
	a := newEntryApprover(func(_ context.Context, join string, d guild.Decision) error {
		got = append(got, decided{join, d})
		return nil
	}, &told)
	ctx := t.Context()
	if err := a.Decided(ctx, approvedBy("user:u1", chain.Approved)); err != nil {
		t.Fatal(err)
	}
	if err := a.Decided(ctx, approvedBy("user:u1", chain.Denied)); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (decided{"j1", guild.Decision{Accept: true, ApprovedBy: "u1"}}) || got[1] != (decided{"j1", guild.Decision{Accept: false, ApprovedBy: "u1"}}) {
		t.Fatalf("decisions = %+v", got)
	}
	// the end of the hour, a cancellation or a changed target leave the entry to its own hour
	for _, st := range []chain.State{chain.Expired, chain.Cancelled, chain.Invalidated} {
		if err := a.Decided(ctx, approvedBy("", st)); err != nil {
			t.Errorf("%s: %v", st, err)
		}
	}
	if len(got) != 2 {
		t.Errorf("a request that ended without an answer was applied: %+v", got)
	}
	// an approval that is not of a user (an agent, nobody) is never an entry
	for _, by := range []string{"", "gm-1", "user:"} {
		if err := a.Decided(ctx, approvedBy(by, chain.Approved)); err == nil {
			t.Errorf("an approval by %q was applied", by)
		}
	}
	// another type is not this owner's
	other := approvedBy("user:u1", chain.Approved)
	other.Request.Type = classes.RequestPR
	if err := a.Decided(ctx, other); err != nil || len(got) != 2 {
		t.Errorf("another type: %v, %+v", err, got)
	}
	// a request that names no entry applies nothing
	bad := approvedBy("user:u1", chain.Approved)
	bad.Request.Target = json.RawMessage(`{}`)
	if err := a.Decided(ctx, bad); err == nil || len(got) != 2 {
		t.Errorf("no entry named: %v, %+v", err, got)
	}
	if len(told) != 0 {
		t.Errorf("the Owner was told %q for nothing", told)
	}
}

// An answer the Citadel could not apply is told to the Owner, who is not left
// believing that the machine entered.
func TestAnAnswerTheCitadelCouldNotApplyIsToldToTheOwner(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{guild.ErrExpired, guild.ErrNotPending, guild.ErrSuspended} {
		var told []string
		a := newEntryApprover(func(context.Context, string, guild.Decision) error { return cause }, &told)
		if err := a.Decided(t.Context(), approvedBy("user:u1", chain.Approved)); !errors.Is(err, cause) {
			t.Errorf("%v: %v", cause, err)
		}
		if len(told) != 1 || !strings.Contains(told[0], "notebook") || !strings.Contains(strings.ToLower(told[0]), "nada foi emitido") {
			t.Errorf("%v: the Owner was told %q", cause, told)
		}
	}
}

// The period gives the Citadel its own approver and lock, the engine's come
// first (the tests and the installation set them), and with neither there is
// nobody to ask and no promotion.
func TestTheApproverAndTheLockOfThePeriodComeAfterTheOnesTheEngineWasGiven(t *testing.T) {
	t.Parallel()
	var told []string
	period := newEntryApprover(nil, &told)
	run := &citadelRun{approver: period, lock: fakelock.New(1, "m1").For("m1")}
	if _, ok := (&Engine{}).approverOf(&citadelRun{}).(noOwner); !ok {
		t.Error("no approver anywhere is not the one that refuses")
	}
	if got := (&Engine{}).approverOf(run); got == nil {
		t.Fatal("the approver of the period was not used")
	} else if _, ok := got.(EntryApprover); !ok {
		t.Errorf("approver = %T", got)
	}
	given := noOwner{}
	if _, ok := (&Engine{deps: Deps{Approver: given}}).approverOf(run).(noOwner); !ok {
		t.Error("the approver given to the engine was overridden by the period's")
	}
	if (&Engine{}).lockOf(&citadelRun{}) != nil {
		t.Error("a lock out of nowhere")
	}
	if (&Engine{}).lockOf(run) != run.lock {
		t.Error("the lock of the period was not used")
	}
	own := fakelock.New(1, "m2").For("m2")
	if (&Engine{deps: Deps{Lock: own}}).lockOf(run) != own {
		t.Error("the lock given to the engine was overridden by the period's")
	}
}

// Before the repository of the lock exists the integration has no lock, and the
// Guild has no promotion; it is made once the repository is recorded.
func TestTheIntegrationHasNoLockUntilTheRepositoryOfTheLockIsRecorded(t *testing.T) {
	t.Parallel()
	i := newInteg(t, false)
	run := i.open()
	defer run.Close()
	lock, err := run.GuildLock(i.ctx)
	if err != nil || lock != nil {
		t.Fatalf("no repository: lock %v, err %v", lock, err)
	}
	if _, err := i.db.Writer().ExecContext(i.ctx, `INSERT INTO github_settings (key, value) VALUES ('lair_repo', 'lucas/mestre-guilda-abc')`); err != nil {
		t.Fatal(err)
	}
	if lock, err = run.GuildLock(i.ctx); err != nil || lock == nil {
		t.Fatalf("repository recorded: lock %v, err %v", lock, err)
	}
	// with no App "Grão-Mestre" the lock fails closed: GitHub fora, never a higher epoch
	if n, err := lock.Check(i.ctx); err == nil || n != 0 {
		t.Errorf("Check with no App = %d, %v", n, err)
	}
}

// The name of the new machine is its own word: the marks that become a link or
// bold in the channel are dropped, so it cannot imitate the code of the engine.
func TestTheNameOfTheNewMachineCannotDressAsALinkOrAsTheCode(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"[Código 123 456](https://evil.x/y)": "Código 123 456https://evil.x/y",
		"**Código: 482 913**":                "Código: 482 913",
		"a`b_c<d>e~f|g":                      "abcdefg",
		"notebook-ana":                       "notebook-ana",
	} {
		if got := plainText(in); got != want {
			t.Errorf("plainText(%q) = %q, want %q", in, got, want)
		}
	}
}
