package tables_test

import (
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/tables"
)

const quiet = 3 * time.Hour

// stalled lists the tables the supervision would warn about now.
func stalled(t *testing.T, w *world) []tables.Stall {
	t.Helper()
	out, err := w.svc.Stalled(w.ctx, quiet)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Part of CA-SUPCAD-06: a table at work for 3 h with no commit, no pull request
// and no step ended is stalled, once; a new commit on its branch starts the count
// again, and the next warning only comes after 3 h more without a delivery.
func TestATableAtWorkWithoutADeliveryIsStalledOnceUntilTheNextDelivery(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(42, "Login", "epico")
	tbl := w.open(42).Table
	if len(stalled(t, w)) != 0 {
		t.Fatal("a table that just opened is stalled")
	}
	w.clk.Advance(quiet - time.Minute)
	if len(stalled(t, w)) != 0 {
		t.Fatal("a table 3 h less a minute at work is stalled")
	}
	w.clk.Advance(time.Minute)
	got := stalled(t, w)
	if len(got) != 1 || got[0].Table.ID != tbl.ID {
		t.Fatalf("after 3 h the stalled are %+v; want the table", got)
	}
	if err := w.svc.StallWarned(w.ctx, w.slug(), tbl.ID, got[0].Since); err != nil {
		t.Fatal(err)
	}
	if len(stalled(t, w)) != 0 {
		t.Fatal("the same quiet is warned twice")
	}
	// A commit after the warning: the count starts again.
	commit(t, w.be, w.git, w.repoOf(tbl), "login.go", "package login\n", "feat: login")
	if len(stalled(t, w)) != 0 { // the next round of the supervision sees the commit
		t.Fatal("the table is warned about at the moment of its commit")
	}
	w.clk.Advance(quiet - time.Minute)
	if len(stalled(t, w)) != 0 {
		t.Fatal("a new commit did not start the count again")
	}
	w.clk.Advance(time.Minute)
	if got = stalled(t, w); len(got) != 1 {
		t.Fatalf("3 h after the commit the stalled are %+v; want the table", got)
	}
}

// Part of CA-SUPCAD-06: the pull request opened or updated and a step that ended
// are deliveries too; a paused table, one in review and one that waits for another
// table are not warned about.
func TestAPullRequestAStepAPauseAndAHeldTableDoNotSpin(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(42, "Login", "epico")
	w.gh.add(43, "Busca", "epico")
	t1, t2 := w.open(42).Table, w.open(43).Table
	w.clk.Advance(quiet - time.Minute)
	if err := w.svc.Delivered(w.ctx, w.slug(), t1.ID); err != nil { // a step ended
		t.Fatal(err)
	}
	w.clk.Advance(time.Minute)
	got := stalled(t, w)
	if len(got) != 1 || got[0].Table.ID != t2.ID {
		t.Fatalf("stalled = %+v; want only %s", got, t2.ID)
	}
	if err := w.svc.Pause(w.ctx, w.slug(), t2.ID, tables.PauseRequest{Reason: "x", By: "user:1"}); err != nil {
		t.Fatal(err)
	}
	if len(stalled(t, w)) != 0 {
		t.Fatal("a paused table is warned about")
	}
	if err := w.svc.Resume(w.ctx, w.slug(), t2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.PROpened(w.ctx, w.slug(), t2.ID, 7, "abc"); err != nil { // in review: not at work
		t.Fatal(err)
	}
	w.clk.Advance(quiet)
	if got = stalled(t, w); len(got) != 1 || got[0].Table.ID != t1.ID {
		t.Fatalf("stalled after the pull request = %+v; want only %s", got, t1.ID)
	}
}

// Part of CA-SUPCAD-06: a table at work whose epic now waits for the merge of
// another one starts no step, so it is not a table that spins: it is skipped.
func TestATableThatWaitsForAnotherTableIsNotWarnedAbout(t *testing.T) {
	t.Parallel()
	deps := &fakeDeps{on: map[int][]int{}}
	w := newWorld(t, worldOpts{deps: deps})
	w.activate()
	w.gh.add(42, "Login", "epico")
	w.gh.add(43, "Busca", "epico")
	t1 := w.open(42).Table
	w.open(43)
	deps.mu.Lock()
	deps.on[42] = []int{43}
	deps.mu.Unlock()
	w.clk.Advance(quiet)
	for _, s := range stalled(t, w) {
		if s.Table.ID == t1.ID {
			t.Fatalf("the table that waits for another is warned about: %+v", s)
		}
	}
	deps.release(42)
	got := stalled(t, w)
	if len(got) != 2 {
		t.Fatalf("after the release the stalled are %+v; want both", got)
	}
}
