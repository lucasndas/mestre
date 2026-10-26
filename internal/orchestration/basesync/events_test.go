package basesync_test

import (
	"testing"

	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/orchestration/basesync"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
)

// quiet moves the base in the mirror without telling the services: nothing
// has noticed that the branch is behind.
func (w *world) quiet(file string) {
	w.t.Helper()
	w.git.Handle(w.remote).Data().Commit("main", "feat: "+file, map[string]string{file: "x\n"})
	if err := w.mirror().Fetch(w.ctx, "origin", workspaces.FetchRefspec); err != nil {
		w.t.Fatal(err)
	}
}

func TestAChangedPRIsTheSecondSignalThatMakesTheStepSyncOnce(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.devWorks()
	sink := basesync.EventSink{Base: w.orc.BaseSync}
	watch := github.Watch{Workspace: w.slug}
	changed := []github.Event{{Kind: github.EventPRChanged, Number: 7, Key: "issue:1:a"}}

	// the PR changed while the branch has the base: nothing to make
	if err := sink.Deliver(w.ctx, watch, w.table.ID, changed); err != nil || len(w.syncQuests()) != 0 {
		t.Fatalf("deliver = %v with %d missions, want none: the branch is not behind", err, len(w.syncQuests()))
	}
	w.quiet("login.go")
	// events that are not a changed PR, or of no table, wake nothing
	other := []github.Event{{Kind: github.EventComment, Number: 7, Key: "c1"}, {Kind: github.EventCI, Key: "ci1"}}
	if err := sink.Deliver(w.ctx, watch, w.table.ID, other); err != nil || len(w.syncQuests()) != 0 {
		t.Fatalf("deliver = %v with %d missions after events that say nothing of the PR", err, len(w.syncQuests()))
	}
	if err := sink.Deliver(w.ctx, watch, "", changed); err != nil || len(w.syncQuests()) != 0 {
		t.Fatalf("deliver = %v with %d missions for an event of no table", err, len(w.syncQuests()))
	}
	// the changed PR of the table behind the base: the step
	if err := sink.Deliver(w.ctx, watch, w.table.ID, changed); err != nil {
		t.Fatal(err)
	}
	if sq := w.syncQuests(); len(sq) != 1 {
		t.Fatalf("missions of sync = %d, want 1", len(sq))
	}
	// the same signal again, or another, does not make a second one
	if err := sink.Deliver(w.ctx, watch, w.table.ID, append(changed, changed...)); err != nil || len(w.syncQuests()) != 1 {
		t.Fatalf("deliver = %v with %d missions, want still 1", err, len(w.syncQuests()))
	}
	// a table that is gone is not retried forever
	if err := sink.Deliver(w.ctx, watch, "99-gone", changed); err != nil {
		t.Fatalf("deliver for a table that is gone = %v, want nil", err)
	}
}
