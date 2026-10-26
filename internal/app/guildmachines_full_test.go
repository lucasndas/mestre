//go:build full

package app

import (
	"context"
	"testing"

	"github.com/mestre-ia/mestre/pkg/clock"
)

// Part of the /guilda listing: GuildMachines says which machines are online. The
// Citadel is online; a Tower is online while its link to the Citadel is up and
// offline before it runs and after its engine stops.
func TestGuildMachinesSaysWhichTowersAreOnline(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	b := r.node("b", a)
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	r.join(b, a, clock.Real{})

	online := func() map[string]bool {
		ms, _, err := a.e.GuildMachines(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, m := range ms {
			out[m.ID] = m.Online
		}
		return out
	}
	// joined but not running: the Tower is not connected
	if got := online(); !got[a.id] || got[b.id] || len(got) != 2 {
		t.Fatalf("before b runs: %v", got)
	}
	r.start(b, clock.Real{})
	r.waitFor("b to be online", func() bool { return b.connected(1) && online()[b.id] })
	if !online()[a.id] {
		t.Fatal("the Citadel is not online")
	}
	b.stop()
	r.waitFor("b to be offline", func() bool { return !online()[b.id] })
}
