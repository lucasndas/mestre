package quests_test

import (
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/quests"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
)

// A quest that runs while its table is in review takes the table back to work
// (mesas.md: "nova missão iniciada estando em revisão"), and a table that is
// closing takes no quest.
func TestARunningQuestTakesATableInReviewBackToWorkAndAClosingTableTakesNone(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	wt, err := w.orc.Tables.Store().Worktree(w.ctx, w.slug, w.table.ID, "cidadela")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.orc.Tables.PROpened(w.ctx, w.slug, w.table.ID, 9, wt.LastCommit); err != nil {
		t.Fatal(err)
	}
	if tbl, _ := w.orc.Tables.Get(w.ctx, w.slug, w.table.ID); tbl.State != tables.StateInReview {
		t.Fatalf("the table is %s; want in review", tbl.State)
	}
	v := w.create(oneStep)
	if tbl, _ := w.orc.Tables.Get(w.ctx, w.slug, w.table.ID); tbl.State != tables.StateInReview {
		t.Errorf("making the quest moved the table to %s; only running it does", tbl.State)
	}
	w.run(v)
	if tbl, _ := w.orc.Tables.Get(w.ctx, w.slug, w.table.ID); tbl.State != tables.StateWorking {
		t.Errorf("the table is %s after the quest ran; want working", tbl.State)
	}

	// A table that is closing does not take another quest: the call is refused.
	other := w.create("name: Outra\nsteps:\n  - {id: a, sheet: Dev Claude}\n")
	_, _ = w.orc.Tables.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: w.table.ID, Cancel: true, Reason: "abandonada"})
	if _, err := w.orc.Quests.Run(w.ctx, other.ID, ""); err == nil {
		t.Error("a quest ran on a table that is ending")
	}
	if _, err := w.orc.Quests.Create(w.ctx, quests.CreateRequest{Workspace: w.slug, Table: w.table.ID, Data: []byte("name: Nova\nsteps:\n  - {id: a, sheet: Dev Claude}\n")}); err == nil {
		t.Error("a quest was made on a table that is ending")
	}
}
