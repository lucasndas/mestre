//go:build full

package characters_test

import (
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
)

func TestAGrandMasterThatStaysDownIsRemindedOncePerDayNotAtEachAttempt(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	gm := r.grandMaster()
	keeper := r.svc.NewKeeper(gm.Machine, nil)
	for range 24*30 + 40 { // every 2 min for a day and a bit
		r.mach[gm.Machine].host.emit(characters.HostEvent{Terminal: r.get(gm.ID).TerminalID, Kind: characters.HostExit})
		if err := keeper.Tick(r.ctx); err != nil {
			t.Fatal(err)
		}
		r.clk.Advance(2 * time.Minute)
		if err := keeper.Tick(r.ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(r.notes.of(characters.NoticeGrandMasterFailing)); n != 2 {
		t.Errorf("alerts in a day and a bit = %d; want the first and one reminder", n)
	}
}
