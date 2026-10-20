package characters_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
)

// CA-GM-01
func TestTheGrandMasterIsBornPinnedAndNothingEndsItNorUnpinsIt(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	tm := r.makeTM("3-login")
	gm := r.grandMaster()
	if !r.get(gm.ID).Pinned {
		t.Fatal("the Grand Master was not born pinned")
	}
	proc := r.mach[gm.Machine].host.proc(gm.ID)

	// The cycle of idleness.
	r.advance(5 * time.Hour)
	if r.state(gm) != characters.Idle || len(proc.stopCalls()) != 0 {
		t.Fatalf("idleness: state %s, %d stops", r.state(gm), len(proc.stopCalls()))
	}
	// Any agent, and the Owner, ask for the end.
	if _, err := r.svc.Dismiss(r.ctx, characters.DismissRequest{ID: gm.ID, By: byChar(tm)}); err == nil {
		t.Error("dismiss by a table master of the Grand Master was accepted")
	}
	if _, err := r.svc.Dismiss(r.ctx, characters.DismissRequest{ID: gm.ID, By: owner}); !errors.Is(err, characters.ErrPinned) {
		t.Errorf("dismiss by the Owner = %v; want it refused as pinned", err)
	}
	if r.state(gm) != characters.Idle || len(proc.stopCalls()) != 0 || r.audited("character.end_refused_pinned") != 1 {
		t.Errorf("dismiss: state %s, %d stops, %d refusals recorded", r.state(gm), len(proc.stopCalls()), r.audited("character.end_refused_pinned"))
	}
	// Not even a user unpins it.
	if err := r.svc.Pin(r.ctx, gm.ID, false, owner); !errors.Is(err, characters.ErrLevelDenied) || !r.get(gm.ID).Pinned {
		t.Errorf("unpin = %v, pinned %v; want it refused", err, r.get(gm.ID).Pinned)
	}
}

// CA-GM-02: the Grand Master that dies as it starts is started at once, then
// after 5 s, 30 s and 2 min; the third failure in 10 min is alerted once.
func TestTheGrandMasterThatDiesAtOnceIsStartedAgainWithBackoffAndAlertedOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	gm := r.grandMaster()
	keeper := r.svc.NewKeeper(gm.Machine)
	t0 := r.clk.Now()

	die := func() {
		r.t.Helper()
		r.mach[gm.Machine].host.emit(characters.HostEvent{Terminal: r.get(gm.ID).TerminalID, Kind: characters.HostExit})
		if r.state(gm) != characters.Interrupted {
			r.t.Fatalf("the Grand Master is %s after its process died", r.state(gm))
		}
	}
	tick := func() {
		r.t.Helper()
		if err := keeper.Tick(r.ctx); err != nil {
			r.t.Fatal(err)
		}
	}
	starts := func() (at []time.Duration) {
		log, err := r.reg.Log(r.ctx, gm.ID)
		if err != nil {
			r.t.Fatal(err)
		}
		for _, l := range log {
			if l.To == characters.Starting && l.Reason == "resume" {
				at = append(at, l.At.Sub(t0))
			}
		}
		return at
	}

	die()
	tick() // the first failure: at once
	for _, delay := range []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute} {
		before := len(starts())
		die()
		tick() // the failure is seen; the start waits
		r.clk.Advance(delay - time.Second)
		tick()
		if len(starts()) != before {
			t.Fatalf("started again before %v: %v", delay, starts())
		}
		r.clk.Advance(time.Second)
		tick()
		if len(starts()) != before+1 {
			t.Fatalf("not started again after %v: %v", delay, starts())
		}
	}
	if want := []time.Duration{0, 5 * time.Second, 35 * time.Second, 155 * time.Second}; !slices.Equal(starts(), want) {
		t.Errorf("restarts at %v; want %v", starts(), want)
	}
	if ns := r.notes.of(characters.NoticeGrandMasterFailing); len(ns) != 1 || ns[0].To.Kind != characters.ToOwner {
		t.Errorf("alerts = %+v; want exactly one", ns)
	}
	// it goes on every 2 min, with no new alert
	die()
	tick()
	r.clk.Advance(2 * time.Minute)
	tick()
	if n := len(starts()); n != 5 || len(r.notes.of(characters.NoticeGrandMasterFailing)) != 1 {
		t.Errorf("after the alert: %d starts, %d alerts", n, len(r.notes.of(characters.NoticeGrandMasterFailing)))
	}
}
