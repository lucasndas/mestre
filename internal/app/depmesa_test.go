package app_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/app"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/waits"
	"github.com/lucasndas/mestre/internal/store"
)

// CA-DEPMESA-05: the table #43 waits for #42. When the table #42 is cancelled,
// or the epic #42 is closed as not planned, the wait becomes cancelled and the
// Grand Master is told (the table stays held for him to decide); a table master
// that tries `gh epic update --depends-on` leaves with level_denied and nothing
// changes; and the table #43, while it waits, is not warned about as a stalled
// table (when the one that works the same time is).
func TestTheCancelledTableOrTheEpicNotPlannedCancelsTheWaitTheTableMasterIsDeniedAndTheWaitingTableIsNotStalled(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		end    func(w *commWorld)
		inform string
	}{
		{"the table is cancelled", func(w *commWorld) {
			req := tables.CloseRequest{By: w.gm, Table: w.table.ID, Cancel: true, Reason: "desistiu"}
			_, err := w.orc.Tables.Close(w.ctx, req)
			var miss *tables.MissingError
			if !errors.As(err, &miss) || len(miss.Decide) != 0 {
				w.t.Fatalf("Close(cancel) = %v; want it waiting for the table master to reach its safe point", err)
			}
			// the table master gives its done, its turn ends and the sweep of the engine takes the cancellation on
			if derr := w.orc.Characters.Done(w.ctx, w.master.ID, "mesa cancelada"); derr != nil {
				w.t.Fatal(derr)
			}
			w.mach.Report(w.ctx, w.master.ID, characters.Idle)
			if serr := w.orc.Tables.Sweep(w.ctx); serr != nil {
				w.t.Fatal(serr)
			}
			if got, gerr := w.orc.Tables.Get(w.ctx, w.slug, w.table.ID); gerr != nil || got.State != tables.StateCancelled {
				w.t.Fatalf("the table is %s (%v); want cancelled", got.State, gerr)
			}

		}, "cancelada"},
		{"the epic is closed as not planned", func(w *commWorld) {
			if err := w.orc.EpicDeps.EpicClosed(w.ctx, w.slug, w.table.Epic, true); err != nil {
				w.t.Fatal(err)
			}
		}, "não planejado"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := &mm{}
			var guild *store.DB
			w := newCommWorld(t, m.setup, func(_ *commWorld, oo *app.OrchestrationOptions) { guild = oo.Guild })
			dependent := 43
			if _, err := w.orc.EpicDeps.Depend(w.ctx, w.gm, dependent, w.table.Epic); err != nil {
				t.Fatal(err)
			}
			waiting, waitingMaster := w.openTable(dependent)
			active := func() int {
				t.Helper()
				all, err := (&waits.Store{DB: guild}).ActiveWaits(w.ctx)
				if err != nil {
					t.Fatal(err)
				}
				n := 0
				for _, x := range all {
					if x.Kind == waits.KindEpic && x.Table == waiting.ID {
						n++
					}
				}
				return n
			}
			epicWait := func() waits.Wait {
				t.Helper()
				all, err := (&waits.Store{DB: guild}).ActiveWaits(w.ctx)
				if err != nil {
					t.Fatal(err)
				}
				for _, x := range all {
					if x.Kind == waits.KindEpic && x.Table == waiting.ID {
						return x
					}
				}
				t.Fatal("the table has no active epic wait")
				return waits.Wait{}
			}
			held := func() bool {
				t.Helper()
				h, err := w.orc.EpicDeps.Held(w.ctx, w.slug, dependent)
				if err != nil {
					t.Fatal(err)
				}
				return h
			}
			if !held() || active() != 1 {
				t.Fatalf("before: held=%v, %d active waits; want held and one wait", held(), active())
			}

			// the table master tries to declare a dependency: level_denied, nothing changes
			r := w.run(waitingMaster, "gh", "epic", "update", fmt.Sprint(dependent), "--depends-on", fmt.Sprint(w.table.Epic), "--json")
			if v := decodeJSON(t, r); r.code != 3 || v.OK || v.Error.Code != "level_denied" {
				t.Errorf("gh epic update --depends-on by a table master = %d ok=%v %+v; want exit 3 level_denied", r.code, v.OK, v.Error)
			}
			if !held() || active() != 1 {
				t.Errorf("after the refused command: held=%v, %d active waits; want it unchanged", held(), active())
			}

			// the waiting table is not a table that stopped, the one that works is
			w.clk.Advance(4 * time.Hour)
			stalled, err := w.orc.Tables.Stalled(w.ctx, 3*time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			for _, s := range stalled {
				seen[s.Table.ID] = true
			}
			if seen[waiting.ID] || !seen[w.table.ID] {
				t.Errorf("stalled = %v; want %s (it works) and not %s (it waits)", seen, w.table.ID, waiting.ID)
			}

			// the end of the other: the wait is cancelled, the table stays held, the Grand Master is told
			theWait := epicWait()
			tc.end(w)
			if ended, err := (&waits.Store{DB: guild}).GetWait(w.ctx, theWait.ID); err != nil || ended.State != waits.Cancelled {
				t.Errorf("the wait is %q (%v) after the end; want cancelled, not satisfied or removed", ended.State, err)
			}
			if active() != 0 {
				t.Errorf("%d active waits after the end; want the wait cancelled", active())
			}
			if !held() {
				t.Error("the waiting table was released; it stays held for the Grand Master to decide")
			}
			items, err := w.orc.Inbox.Store().All(w.ctx, w.gm.ID)
			if err != nil {
				t.Fatal(err)
			}
			told := false
			for _, it := range items {
				told = told || strings.Contains(it.Text, tc.inform) && strings.Contains(it.Text, fmt.Sprintf("#%d", dependent))
			}
			if !told {
				t.Errorf("the Grand Master was not told (%q, #%d)", tc.inform, dependent)
			}
		})
	}
}
