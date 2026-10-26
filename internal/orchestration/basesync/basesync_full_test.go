//go:build full

package basesync_test

import (
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/orchestration/quests"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
)

// CA-SYNC-01: a table with a PR open, and the base moved: the engine makes the
// step sync for the Dev, the table goes to working and the users hear nothing.
func TestSyncStepForTheDevWhenTheBaseMoves(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.devWorks()
	if got := w.tableNow().State; got != tables.StateInReview {
		t.Fatalf("the table is %s before the base moves; want in_review", got)
	}
	w.moveBase("login.go") // the file the Dev changed: a conflict

	sq := w.syncQuests()
	if len(sq) != 1 || sq[0].Name != "sync" || sq[0].State != quests.Running {
		t.Fatalf("missions of the step sync = %+v; want one running", sq)
	}
	// risk baixo: the step has no point of approval (the mission has no field of
	// risk), so no request of approval is born for it
	if len(sq[0].Def.Steps) != 1 || sq[0].Def.Steps[0].Approval != "" {
		t.Errorf("the step sync has an approval point: %+v", sq[0].Def.Steps)
	}
	steps, err := w.orc.Quests.Store().Steps(w.ctx, sq[0].ID)
	if err != nil || len(steps) != 1 || steps[0].ID != "sync" {
		t.Fatalf("steps = %+v, %v", steps, err)
	}
	if steps[0].Character != dev.ID {
		t.Errorf("the step is for %q; want the Dev %q", steps[0].Character, dev.ID)
	}
	if steps[0].Approval != 0 || steps[0].Approved {
		t.Errorf("the step waits for an approval: %+v", steps[0])
	}
	if got := w.tableNow().State; got != tables.StateWorking {
		t.Errorf("the table is %s; want working", got)
	}
	found := false
	for _, x := range w.itemsOf(dev.ID, inbox.KindQuest) {
		found = found || strings.Contains(x, "git merge origin/main") && strings.Contains(x, "Nunca use rebase")
	}
	if !found {
		t.Errorf("the Dev did not get the instruction of the merge: %q", w.itemsOf(dev.ID, inbox.KindQuest))
	}
	if told := w.users.Told(); len(told) != 0 {
		t.Errorf("the Owner was told %q; a conflict is a task for the Dev, not a decision", told)
	}
	for _, kind := range []inbox.Kind{inbox.KindAgent, inbox.KindApproval} {
		if got := w.itemsOf(w.gm.ID, kind); len(got) != 0 {
			t.Errorf("the Grand Master got %v items: %q", kind, got)
		}
	}
	if got := w.itemsOf(w.master.ID, inbox.KindSystem); len(got) == 0 {
		t.Error("the Master of the table was not told that the step sync was made")
	}
}

// CA-SYNC-02: a red verification gives the step back to the Dev with the
// output and counts a volta; a green one ends it.
func TestARedVerificationReturnsTheSyncToTheDevAndAGreenOneEndsIt(t *testing.T) {
	t.Parallel()
	v := &fakeVerifier{confirmed: true, results: []quests.VerifyResult{red("build failed\nlogin.go:3: undefined: x", "login.go:3")}}
	w, dev := syncWorld(t, v)

	if res := w.doneOfSync(dev); res.Verification != quests.VerificationPending {
		t.Fatalf("verification = %q; want pending (the class of the Dev does not ask for it)", res.Verification)
	}
	w.orc.Quests.WaitVerifications()
	q := w.syncState()
	if q.State != quests.Running {
		t.Fatalf("the sync is %s after a red verification", q.State)
	}
	steps, err := w.orc.Quests.Store().Steps(w.ctx, q.ID)
	if err != nil || len(steps) != 1 || steps[0].Returns != 1 || steps[0].State != quests.StepRunning {
		t.Fatalf("steps = %+v, %v; want the step running with one volta", steps, err)
	}
	told := strings.Join(w.itemsOf(dev.ID, inbox.KindSystem), "\n")
	for _, want := range []string{"go test ./...", "login.go:3: undefined: x"} {
		if !strings.Contains(told, want) {
			t.Errorf("the Dev was not told %q:\n%s", want, told)
		}
	}

	w.doneOfSync(dev) // the second run is green
	w.orc.Quests.WaitVerifications()
	if got := w.syncState().State; got != quests.Completed {
		t.Fatalf("the sync is %s after the green verification", got)
	}
	if v.count() != 2 {
		t.Errorf("verifications = %d; want 2", v.count())
	}
	// the sync took the table back to work; the `pr update` of the Dev (it tells
	// the table with PROpened, as the commands of production do) puts it in review
	if got := w.tableNow().State; got != tables.StateWorking {
		t.Fatalf("the table is %s after the sync ended; want working until the pr update", got)
	}
	if _, err := w.orc.Tables.PROpened(w.ctx, w.slug, w.table.ID, 7, "head2"); err != nil {
		t.Fatal(err)
	}
	if now := w.tableNow(); now.State != tables.StateInReview || now.PRHead != "head2" {
		t.Errorf("after the pr update the table is %s with head %q; want in_review with head2", now.State, now.PRHead)
	}
}
