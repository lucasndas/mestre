package epicdeps_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/epicdeps"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/waits"
)

// CA-DEPMESA-01 (the engine side: the epic of a table that depends on a table
// in review is held by a wait of the kind epic, and Waiting names the table it
// waits for; the opening that holds the first message and the `table list` are
// asserted in internal/orchestration/tables)
func TestAnEpicThatDependsOnATableInReviewIsHeldByAWaitOfTheGrandMaster(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.tables.put(12, tables.StateInReview, false)
	w.tables.put(15, tables.StateOpen, false)

	res, err := w.svc.Depend(w.ctx, w.gm, 15, 12)
	if err != nil || !res.Waiting || res.State != epicdeps.Pending {
		t.Fatalf("Depend = %+v, %v; want pending and waiting", res, err)
	}
	if !w.held(15) {
		t.Fatal("the epic #15 is not held")
	}
	if on, _ := w.svc.Waiting(w.ctx, workspace, 15); !slices.Equal(on, []int{12}) {
		t.Fatalf("Waiting = %v, want [12]", on)
	}
	ws := w.activeWaits()
	if len(ws) != 1 || ws[0].Kind != waits.KindEpic || ws[0].Waiter != w.gm.ID || ws[0].Table != "15-t" || ws[0].Reason != "a mesa #12" {
		t.Fatalf("active waits = %+v, want one of the kind epic for the Grand Master, for the table 15-t, waiting for a mesa #12", ws)
	}
	if held, _ := w.svc.Held(w.ctx, workspace, 12); held {
		t.Fatal("the epic #12, that depends on nothing, is held")
	}
}

// CA-DEPMESA-02
func TestTheMergeThatIsConfirmedSatisfiesTheWaitAndWakesTheGrandMasterAndOnlyThen(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	t12 := w.tables.put(12, tables.StateInReview, false)
	w.tables.put(15, tables.StateOpen, false)
	if _, err := w.svc.Depend(w.ctx, w.gm, 15, 12); err != nil {
		t.Fatal(err)
	}

	// The pull request is merged but the commit is not in the base yet: the
	// tables do not tell Merged, and nothing changes.
	if !w.held(15) || len(w.activeWaits()) != 1 || w.mentions("#12", "#15") {
		t.Fatalf("before the confirmation: held=%v waits=%d inbox=%q; want held, one wait, no wake", w.held(15), len(w.activeWaits()), w.inboxTexts())
	}

	t12.Merged = true
	if err := w.svc.Merged(w.ctx, workspace, t12); err != nil {
		t.Fatal(err)
	}
	if w.held(15) || len(w.activeWaits()) != 0 {
		t.Fatalf("after the merge: held=%v waits=%d; want released and no active wait", w.held(15), len(w.activeWaits()))
	}
	if !w.mentions("#12", "#15", "pode seguir") {
		t.Fatalf("the Grand Master got %q, want a wake that cites #12 and #15", w.inboxTexts())
	}
	if !slices.Equal(w.released, []string{"15-t"}) {
		t.Fatalf("released = %v, want the table 15-t", w.released)
	}
	// Telling it twice changes nothing: the dependency is not satisfied again.
	if err := w.svc.Merged(w.ctx, workspace, t12); err != nil || w.held(15) || len(w.activeWaits()) != 0 {
		t.Fatalf("the second Merged = %v, held=%v", err, w.held(15))
	}
}

// CA-DEPMESA-03
func TestADependencyThatClosesARingIsRefusedWithTheRingWritten(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if _, err := w.svc.Depend(w.ctx, w.gm, 12, 15); err != nil {
		t.Fatal(err)
	}
	_, err := w.svc.Depend(w.ctx, w.gm, 15, 12)
	c, ok := isCycle(err)
	if !ok || c.Text != "#12 → #15 → #12" {
		t.Fatalf("Depend(15, 12) = %v, want the ring #12 → #15 → #12", err)
	}
	if w.held(15) || len(w.activeWaits()) != 0 {
		t.Fatal("the refused dependency left something behind")
	}
	// A longer ring is written whole.
	if _, err := w.svc.Depend(w.ctx, w.gm, 15, 18); err != nil {
		t.Fatal(err)
	}
	_, err = w.svc.Depend(w.ctx, w.gm, 18, 12)
	if c, ok := isCycle(err); !ok || c.Text != "#12 → #15 → #18 → #12" {
		t.Fatalf("Depend(18, 12) = %v, want the ring #12 → #15 → #18 → #12", err)
	}
	if _, err := w.svc.Depend(w.ctx, w.gm, 12, 12); err == nil {
		t.Fatal("an epic that depends on itself was accepted")
	}
}

// CA-DEPMESA-04
func TestTwoTablesOnTheSameAreaTheOneOfTheHigherNumberWaitsAndIsWokenByTheMerge(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	t12 := w.tables.put(12, tables.StateWorking, false)
	w.tables.put(15, tables.StateWorking, false)

	res, err := w.svc.Depend(w.ctx, w.gm, 15, 12)
	if err != nil || !res.Waiting {
		t.Fatalf("Depend = %+v, %v", res, err)
	}
	if w.held(12) || !w.held(15) {
		t.Fatalf("held(12)=%v held(15)=%v; the #12 goes on and the #15 waits", w.held(12), w.held(15))
	}
	// Declaring it again changes nothing: one wait only.
	if _, err := w.svc.Depend(w.ctx, w.gm, 15, 12); err != nil || len(w.activeWaits()) != 1 {
		t.Fatalf("the second Depend = %v with %d waits, want one wait", err, len(w.activeWaits()))
	}
	t12.Merged = true
	if err := w.svc.Merged(w.ctx, workspace, t12); err != nil {
		t.Fatal(err)
	}
	if w.held(15) || !w.mentions("#12", "#15") {
		t.Fatalf("held(15)=%v inbox=%q; want the #15 woken", w.held(15), w.inboxTexts())
	}
}

// CA-DEPMESA-05
func TestTheTableThatIsCancelledOrTheEpicClosedAsNotPlannedCancelsTheWaitAndKeepsTheOtherHeld(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	t12 := w.tables.put(12, tables.StateWorking, false)
	w.tables.put(15, tables.StateWorking, false)
	if _, err := w.svc.Depend(w.ctx, w.gm, 15, 12); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Cancelled(w.ctx, workspace, t12); err != nil {
		t.Fatal(err)
	}
	if len(w.activeWaits()) != 0 {
		t.Fatalf("active waits = %+v, want the wait cancelled", w.activeWaits())
	}
	if !w.mentions("cancelada", "#15") {
		t.Fatalf("the Grand Master got %q, want the cancellation told", w.inboxTexts())
	}
	// The Grand Master decides what to do with the dependent: until then it
	// stays held, and it is not a table that stopped (the notice of the tables
	// that turn skips the held ones).
	if !w.held(15) || len(w.released) != 0 {
		t.Fatalf("held(15)=%v released=%v; want held until the Grand Master decides", w.held(15), w.released)
	}
	if err := w.svc.Release(w.ctx, w.gm, 15, 12); err != nil {
		t.Fatal(err)
	}
	if w.held(15) || !slices.Equal(w.released, []string{"15-t"}) {
		t.Fatalf("held(15)=%v released=%v; want released by the Grand Master", w.held(15), w.released)
	}
	if err := w.svc.Release(w.ctx, w.gm, 15, 12); err == nil {
		t.Fatal("a dependency that was dropped was dropped again")
	}

	// The epic #20 has no table and is closed as not planned.
	w2 := newWorld(t)
	w2.tables.put(15, tables.StateWorking, false)
	w2.epics.by[20] = tables.Epic{Number: 20, Open: true}
	if res, err := w2.svc.Depend(w2.ctx, w2.gm, 15, 20); err != nil || !res.Waiting {
		t.Fatalf("Depend on an epic without table = %+v, %v; want waiting", res, err)
	}
	if !w2.mentions("#20", "não tem mesa") {
		t.Fatalf("the Grand Master got %q, want the notice that #20 has no table", w2.inboxTexts())
	}
	if err := w2.svc.EpicClosed(w2.ctx, workspace, 20, true); err != nil {
		t.Fatal(err)
	}
	if len(w2.activeWaits()) != 0 || !w2.held(15) || !w2.mentions("não planejado", "#15") {
		t.Fatalf("waits=%d held=%v inbox=%q; want the wait cancelled, the #15 held and the Grand Master told", len(w2.activeWaits()), w2.held(15), w2.inboxTexts())
	}
}

// Part of CA-DEPMESA-05 (the service refuses everybody but the Grand Master; the
// `level_denied` of the command `gh epic update` closes with the command, M5 stage 6)
func TestOnlyTheGrandMasterDeclaresADependency(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	master := w.add(characters.RoleTableMaster, "15-t", "Mestre")
	if _, err := w.svc.Depend(w.ctx, master, 15, 12); err != epicdeps.ErrNotGrandMaster {
		t.Fatalf("Depend by a table master = %v, want ErrNotGrandMaster", err)
	}
	if err := w.svc.Release(w.ctx, master, 15, 12); err != epicdeps.ErrNotGrandMaster {
		t.Fatalf("Release by a table master = %v, want ErrNotGrandMaster", err)
	}
	if w.held(15) {
		t.Fatal("a refused dependency holds the epic")
	}
}

func TestADependencyDeclaredBeforeTheTableOpensHoldsItWhenItOpens(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.tables.put(12, tables.StateWorking, false)
	if res, err := w.svc.Depend(w.ctx, w.gm, 15, 12); err != nil || res.Waiting || res.State != epicdeps.Pending {
		t.Fatalf("Depend with no table = %+v, %v; want pending without a wait", res, err)
	}
	if len(w.activeWaits()) != 0 || !w.held(15) {
		t.Fatalf("waits=%d held=%v; want held and no wait yet", len(w.activeWaits()), w.held(15))
	}
	t15 := w.tables.put(15, tables.StateOpen, false)
	if err := w.svc.Opened(w.ctx, workspace, t15); err != nil {
		t.Fatal(err)
	}
	if ws := w.activeWaits(); len(ws) != 1 || ws[0].Table != "15-t" {
		t.Fatalf("waits = %+v, want one for 15-t", ws)
	}
	// Opening again does not make a second wait.
	if err := w.svc.Opened(w.ctx, workspace, t15); err != nil || len(w.activeWaits()) != 1 {
		t.Fatalf("the second Opened = %v with %d waits", err, len(w.activeWaits()))
	}
}

func TestADependencyOnATableAlreadyMergedOrOnAClosedEpicIsSatisfiedAtOnce(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.tables.put(12, tables.StateInReview, true)
	w.tables.put(15, tables.StateOpen, false)
	w.tables.put(16, tables.StateOpen, false)
	w.epics.by[13] = tables.Epic{Number: 13, Open: false}
	if res, err := w.svc.Depend(w.ctx, w.gm, 15, 12); err != nil || res.State != epicdeps.Satisfied || res.Waiting {
		t.Fatalf("Depend on a merged table = %+v, %v", res, err)
	}
	if res, err := w.svc.Depend(w.ctx, w.gm, 16, 13); err != nil || res.State != epicdeps.Satisfied {
		t.Fatalf("Depend on a closed epic = %+v, %v", res, err)
	}
	if w.held(15) || w.held(16) || len(w.activeWaits()) != 0 {
		t.Fatal("a dependency that is already met holds a table")
	}
}

func TestADependencySurvivesARestartOfTheEngine(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.tables.put(12, tables.StateWorking, false)
	w.tables.put(15, tables.StateOpen, false)
	if _, err := w.svc.Depend(w.ctx, w.gm, 15, 12); err != nil {
		t.Fatal(err)
	}
	w.svc = w.service()
	if !w.held(15) || len(w.activeWaits()) != 1 {
		t.Fatalf("after the restart: held=%v waits=%d", w.held(15), len(w.activeWaits()))
	}
	t12 := w.tables.put(12, tables.StateInReview, true)
	if err := w.svc.Merged(w.ctx, workspace, t12); err != nil || w.held(15) {
		t.Fatalf("Merged after the restart = %v, held=%v", err, w.held(15))
	}
}

func TestADependencyThatCouldNotBeArmedLeavesTheStateItHad(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	t12 := w.tables.put(12, tables.StateWorking, false)
	w.tables.put(15, tables.StateWorking, false)
	if _, err := w.svc.Depend(w.ctx, w.gm, 15, 12); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Cancelled(w.ctx, workspace, t12); err != nil {
		t.Fatal(err)
	}
	// The table #12 is gone, so the declaration again reads the epic, which fails.
	w.tables.drop(12)
	w.epics.err = errors.New("github is down")
	if _, err := w.svc.Depend(w.ctx, w.gm, 15, 12); err == nil {
		t.Fatal("Depend succeeded with GitHub down")
	}
	if !w.held(15) {
		t.Fatal("the #15 was set free by a declaration that failed: it must stay held until the Grand Master decides")
	}
	// A dependency that is new and fails leaves nothing.
	if _, err := w.svc.Depend(w.ctx, w.gm, 15, 30); err == nil {
		t.Fatal("Depend on an epic that cannot be read succeeded")
	}
	if on, _ := w.svc.Waiting(w.ctx, workspace, 15); !slices.Equal(on, []int{12}) {
		t.Fatalf("Waiting = %v, want only the cancelled #12", on)
	}
}

func TestAnOpeningThatFailsHalfWayLeavesNoWaitBehind(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.tables.put(12, tables.StateWorking, false)
	if _, err := w.svc.Depend(w.ctx, w.gm, 15, 12); err != nil {
		t.Fatal(err)
	}
	// The #20 has no table and cannot be read: the second dependency fails.
	if _, err := w.svc.Depend(w.ctx, w.gm, 15, 20); err != nil {
		t.Fatal(err)
	}
	w.epics.err = errors.New("github is down")
	t15 := w.tables.put(15, tables.StateOpen, false)
	if err := w.svc.Opened(w.ctx, workspace, t15); err == nil {
		t.Fatal("Opened succeeded with an epic that cannot be read")
	}
	if n := len(w.activeWaits()); n != 0 {
		t.Fatalf("%d waits left behind by the opening that failed", n)
	}
	// The table is gone; the next table of the epic is held again.
	w.epics.err = nil
	w.epics.by[20] = tables.Epic{Number: 20, Open: true}
	if err := w.svc.Dropped(w.ctx, workspace, t15); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Opened(w.ctx, workspace, t15); err != nil || len(w.activeWaits()) != 2 {
		t.Fatalf("Opened = %v with %d waits, want two", err, len(w.activeWaits()))
	}
	// A cancelled table ends its waits too.
	if err := w.svc.Dropped(w.ctx, workspace, t15); err != nil || len(w.activeWaits()) != 0 || !w.held(15) {
		t.Fatalf("Dropped = %v, waits=%d held=%v; want no wait and the epic still held", err, len(w.activeWaits()), w.held(15))
	}
}

func TestTellingTheMergeAgainRepairsAReleaseThatFailed(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.tables.put(12, tables.StateWorking, false)
	w.tables.put(15, tables.StateOpen, false)
	if _, err := w.svc.Depend(w.ctx, w.gm, 15, 12); err != nil {
		t.Fatal(err)
	}
	t12 := w.tables.put(12, tables.StateInReview, true)
	if err := w.svc.Merged(w.ctx, workspace, t12); err != nil {
		t.Fatal(err)
	}
	n := len(w.released)
	// The poll tells the merge again: the table is released once more, which
	// delivers what the first release may have left.
	if err := w.svc.Merged(w.ctx, workspace, t12); err != nil || len(w.released) != n+1 {
		t.Fatalf("the second Merged = %v, released %v; want the release repeated", err, w.released)
	}
}
