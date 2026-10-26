package app_test

import (
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/app"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
	"github.com/mestre-ia/mestre/internal/panel"
)

func blockOf(t *testing.T, v panel.ViewData, id string) panel.TableBlock {
	t.Helper()
	for _, b := range v.Tables {
		if b.ID == id {
			return b
		}
	}
	t.Fatalf("no block %s in %+v", id, v.Tables)
	return panel.TableBlock{}
}

// CA-VWS-01
// CA-VWS-08
// (the part of the engine; also part of CA-VWS-05, which is heavy) the block has the
// epic, the branch, the party with its machine and the Grand Master is the center; a
// table at work for four hours with no delivery is stalled, and the same table paused is not.
func TestTheViewHasTheBlockOfTheTableAndItsQuiet(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	host := app.NewViewHostForTest(w.orc, w.clk)

	v, err := host.View(w.ctx, w.slug)
	if err != nil {
		t.Fatal(err)
	}
	if v.GrandMaster == nil || v.GrandMaster.ID != w.gm.ID || v.GrandMaster.MachineName == "" {
		t.Errorf("the Grand Master is %+v", v.GrandMaster)
	}
	b := blockOf(t, v, w.table.ID)
	if b.Branch != w.table.Branch || b.Epic != w.table.Epic || b.State == "" || b.IssueWarning != "" {
		t.Errorf("block = %+v", b)
	}
	if b.PartySize != 1 || len(b.Party) != 1 || b.Party[0].MachineName == "" {
		t.Errorf("party = %d %+v", b.PartySize, b.Party)
	}
	if b.Stalled {
		t.Error("a table that just opened is stalled")
	}

	w.clk.Advance(4 * time.Hour)
	v, _ = host.View(w.ctx, w.slug)
	b = blockOf(t, v, w.table.ID)
	if b.State != string(tables.StateWorking) {
		t.Fatalf("the table is %s, not working", b.State)
	}
	if !b.Stalled || b.QuietSeconds < int64(3*time.Hour/time.Second) {
		t.Errorf("working for 4 h with no delivery: stalled=%v quiet=%d", b.Stalled, b.QuietSeconds)
	}

	if err := w.orc.Tables.Pause(w.ctx, w.slug, w.table.ID, tables.PauseRequest{Reason: "teste", By: "u-adm"}); err != nil {
		t.Fatal(err)
	}
	v, _ = host.View(w.ctx, w.slug)
	b = blockOf(t, v, w.table.ID)
	if b.Stalled || !b.Paused || len(b.Waits) == 0 || b.Waits[0].Kind != panel.WaitPaused {
		t.Errorf("a paused table: %+v", b)
	}
}

// The places of the view are kept for the workspace, by the key of the element.
func TestTheViewKeepsWhereTheUserPutTheBlocks(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	host := app.NewViewHostForTest(w.orc, w.clk)
	items := []panel.CanvasItem{{Key: "gm", X: 10, Y: 20}, {Key: "table:" + w.table.ID, X: -300, Y: 40}}
	if err := host.SaveLayout(w.ctx, w.slug, items); err != nil {
		t.Fatal(err)
	}
	v, err := host.View(w.ctx, w.slug)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][2]float64{}
	for _, it := range v.Layout {
		got[it.Key] = [2]float64{it.X, it.Y}
	}
	if got["gm"] != [2]float64{10, 20} || got["table:"+w.table.ID] != [2]float64{-300, 40} {
		t.Errorf("layout = %v", got)
	}
}
