//go:build full

package app_test

import (
	"errors"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/connections"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
)

// CA-MM-01: a table just opened has its table master, called Mestre, pinned,
// connected to the Grand Master and started in the worktree of the table (the
// request of the session zero carries that folder); nobody ends it while the
// table is open: not another agent, not the Grand Master, not the idle time.
func TestTheTableMasterOfAFreshTableIsCalledMestrePinnedConnectedAndStartedInTheWorktree(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	before := len(w.mach.Started())
	table, master := w.openTable(43)

	if master.Name != characters.NameTableMaster || master.Role != characters.RoleTableMaster || master.Table != table.ID {
		t.Fatalf("the table master = %+v", master)
	}
	got := w.get(master)
	if !got.Pinned {
		t.Error("the table master was not born pinned")
	}
	if c, ok, err := w.orc.Connections.Store().Between(w.ctx, connections.Char(master.ID), connections.Char(w.gm.ID)); err != nil || !ok || !c.Has(connections.ReasonMaster) {
		t.Errorf("connection to the Grand Master = %+v, %v, %v", c, ok, err)
	}
	rec, err := w.orc.Tables.Store().Worktree(w.ctx, w.slug, table.ID, "cidadela")
	if err != nil || rec.Dir == "" {
		t.Fatalf("the worktree record = %+v, %v", rec, err)
	}
	var started []characters.StartCommand
	for _, s := range w.mach.Started()[before:] {
		if s.Character == master.ID {
			started = append(started, s)
		}
	}
	if len(started) != 1 || started[0].Request.Dir != rec.Dir || started[0].Request.Name != "Mestre" || started[0].Request.Table.ID != table.ID {
		t.Fatalf("the start of the table master = %+v; want it once, as Mestre, in %s", started, rec.Dir)
	}

	// pinned for the life of the table: no agent, no Grand Master and no idle time ends it
	for _, by := range []characters.Creator{{Kind: characters.CreatedByCharacter, ID: w.gm.ID}, {Kind: characters.CreatedByCharacter, ID: "x"}, {Kind: characters.CreatedByEngine}} {
		_, err := w.orc.Characters.Dismiss(w.ctx, characters.DismissRequest{ID: master.ID, By: by, Reason: "teste"})
		if err == nil || !errors.Is(err, characters.ErrPinned) && !errors.Is(err, characters.ErrLevelDenied) {
			t.Errorf("dismiss by %+v = %v; want it refused", by, err)
		}
	}
	w.clk.Advance(72 * time.Hour)
	if err := w.orc.Characters.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	if now := w.get(master); !now.State.Alive() {
		t.Errorf("the idle time ended the table master: %s", now.State)
	}
	if tb, err := w.orc.Tables.Get(w.ctx, w.slug, table.ID); err != nil || tb.State == tables.StateClosed {
		t.Errorf("table = %+v, %v", tb, err)
	}
}
