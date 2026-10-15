package workspaces_test

import (
	"strings"
	"testing"
	"time"
)

// The main clone is advanced only when it has the main branch checked out: an
// agent that switched it must not have origin/main merged into the other
// branch.
func TestTheMainCloneIsNotAdvancedOntoAnotherBranchItHasCheckedOut(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.active()
	clone := r.principal(w.Slug)
	if err := clone.CreateBranch(r.ctx, "outra", "main"); err != nil {
		t.Fatal(err)
	}
	other, _ := clone.ResolveRef(r.ctx, "refs/heads/outra")
	clone.SetHead("outra")
	r.push("feat: ahead", map[string]string{"a.txt": "a"})

	if err := r.svc.Fetch(r.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	if got, _ := clone.ResolveRef(r.ctx, "refs/heads/outra"); got != other {
		t.Errorf("the other branch moved to %s", got.Short())
	}
	if a := r.get(w.Slug).Advance; !strings.Contains(a.Reason, "outra") {
		t.Errorf("advance = %+v; want it held back, saying the branch", a)
	}
}

// A restarted Citadel fetches the mirrors it finds at once, even when the last
// fetch was minutes ago (docs/orquestracao/workspaces.md, failure of the
// fetch): the interval does not wait for the old process's clock.
func TestARestartedCitadelFetchesTheMirrorsAtOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.active()
	r.clk.Advance(5 * time.Minute) // five minutes after the last fetch
	base := r.fetchCount()
	svc := r.newService(r.db)
	if _, _, err := svc.Tick(r.ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.fetchCount() - base; got != 1 {
		t.Fatalf("fetches in the first tick of the restarted Citadel = %d; want 1", got)
	}
}
