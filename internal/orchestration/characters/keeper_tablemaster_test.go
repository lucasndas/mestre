package characters_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
)

// The table master is kept up with the policy of the Grand Master
// (docs/orquestracao/mestre-da-mesa.md#fixo-enquanto-a-mesa-estiver-aberta):
// started again at once, then after 5 s, 30 s and 2 min; the third failure in
// 10 min is alerted once, to the Grand Master and to the users; and a table
// that is closing no longer has it started again.
func TestTheTableMasterThatDiesIsStartedAgainWithBackoffAndTheGrandMasterIsAlertedOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	r.makeGM()
	tm := r.makeTM("3-login")
	keeper := r.svc.NewKeeper(tm.Machine, func(context.Context, string) bool { return true })
	die := func() {
		r.t.Helper()
		r.mach[tm.Machine].host.emit(characters.HostEvent{Terminal: r.get(tm.ID).TerminalID, Kind: characters.HostExit})
		if r.state(tm) != characters.Interrupted {
			r.t.Fatalf("the table master is %s after its process died", r.state(tm))
		}
	}
	tick := func() {
		r.t.Helper()
		if err := keeper.Tick(r.ctx); err != nil {
			r.t.Fatal(err)
		}
	}
	starts := func() (n int) {
		log, err := r.reg.Log(r.ctx, tm.ID)
		if err != nil {
			r.t.Fatal(err)
		}
		for _, l := range log {
			if l.To == characters.Starting && l.Reason == "resume" {
				n++
			}
		}
		return n
	}
	alerts := func() []characters.Notice { return r.notes.of(characters.NoticeTableMasterFailing) }

	die()
	tick()
	if starts() != 1 || len(alerts()) != 0 {
		t.Fatalf("after the 1st failure: %d starts, %d alerts", starts(), len(alerts()))
	}
	for i, delay := range []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute} {
		before := starts()
		die()
		tick()
		if want := []int{0, 2, 2}[i]; len(alerts()) != want {
			t.Fatalf("alerts after the %dth failure = %d; want %d", i+2, len(alerts()), want)
		}
		r.clk.Advance(delay - time.Second)
		tick()
		if starts() != before {
			t.Fatalf("started again before %v", delay)
		}
		r.clk.Advance(time.Second)
		tick()
		if starts() != before+1 {
			t.Fatalf("not started again after %v", delay)
		}
	}
	got := alerts()
	if len(got) != 2 || got[0].To.Kind != characters.ToGrandMaster || got[1].To.Kind != characters.ToOwner || got[0].Table != "3-login" ||
		!strings.Contains(got[0].Text, "Mestre da mesa 3-login") {
		t.Errorf("alerts = %+v; want one to the users and one to the Grand Master, about the table master", got)
	}

	// the table closes: a master that dies is left alone
	r.tables.setState("3-login", characters.TableClosing)
	die()
	tick()
	r.clk.Advance(3 * time.Minute)
	tick()
	if r.state(tm) != characters.Interrupted {
		t.Errorf("the table master of a closing table was started again: %s", r.state(tm))
	}
}
