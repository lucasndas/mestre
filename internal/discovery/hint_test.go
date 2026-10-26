package discovery_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/mestre-ia/mestre/internal/discovery"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// A forged answer of a huge epoch that points to a machine of the Guild that
// is not the Citadel: the first pass takes it as a hint; when it does not
// prove the epoch (the 4007 brings no newer certificate), it is skipped and
// the real Citadel is found. A hint whose machine is not the one it named is
// not taken at all.
func TestAHintOfAHigherEpochThatDoesNotHoldIsSkippedWithBackoff(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		ctx := t.Context()
		citadel := r.newMachine(ctx, "citadel", "citadel", "192.168.1.10", discovery.RoleCitadel, 1, r.ca)
		citadel.answer(ctx)
		r.newMachine(ctx, "towerb", "towerb", "192.168.1.30", discovery.RoleTower, 1, r.ca)
		r.newMachine(ctx, "towerc", "towerc", "192.168.1.31", discovery.RoleTower, 1, r.ca)
		forged := r.newMachine(ctx, "evil", "evil", "192.168.1.66", discovery.RoleTower, 1<<62, r.ca)
		forged.citadel, forged.citadelI = "towerb", "192.168.1.30"
		forged.answer(ctx)
		liar := r.newMachine(ctx, "liar", "liar", "192.168.1.67", discovery.RoleTower, 1<<62, r.ca)
		liar.citadel, liar.citadelI = "citadel9", "192.168.1.31" // towerc is there, not citadel9
		liar.answer(ctx)

		var rv *recVerifier
		loc := r.locator("tower1", r.client("tower1", "tower1"), fixed(1, "citadel"), func(c *discovery.LocatorConfig) { rv = recording(c) })
		first, err := loc.Find(ctx)
		if err != nil || first.Addr != "192.168.1.30:7443" {
			t.Fatalf("first pass = %+v, %v; want the hint of the higher epoch", first, err)
		}
		// The protocol reached towerb, got 4007 with epoch 1 and asks again.
		loc.Rediscover(ctx)
		if got, _ := loc.Current(); got.Addr != "192.168.1.10:7443" || got.MachineID != "citadel" {
			t.Fatalf("after the refuted hint the target is %+v, want the real Citadel", got)
		}
		checked := func(addr string) int {
			n := 0
			for _, a := range rv.called() {
				if a == addr {
					n++
				}
			}
			return n
		}
		if n := checked("192.168.1.30:7443"); n != 1 {
			t.Fatalf("the refuted hint was checked %d times, want once", n)
		}
		if n := checked("192.168.1.31:7443"); n != 1 {
			t.Fatalf("the hint naming another machine was checked %d times, want once", n)
		}
	})
}

// With no Citadel in sight, a refuted hint comes back after 30 s, and after
// 60 s when it fails again: the 4007 loop of a forged answer is slowed down.
func TestARefutedHintIsSkippedFor30SecondsThenTwiceAsLong(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		ctx := t.Context()
		r.newMachine(ctx, "towerb", "towerb", "192.168.1.30", discovery.RoleTower, 1, r.ca)
		forged := r.newMachine(ctx, "evil", "evil", "192.168.1.66", discovery.RoleTower, 1<<62, r.ca)
		forged.citadel, forged.citadelI = "towerb", "192.168.1.30"
		forged.answer(ctx)
		loc := r.locator("tower1", r.client("tower1", "tower1"), fixed(1, "citadel"), nil)
		if got, err := loc.Find(ctx); err != nil || got.Addr != "192.168.1.30:7443" {
			t.Fatalf("first pass = %+v, %v", got, err)
		}
		for _, want := range []time.Duration{30 * time.Second, 60 * time.Second} {
			start := r.clk.Now()
			loc.Rediscover(ctx) // the 4007 brought no newer certificate
			if d := clock.Since(r.clk, start); d < want || d >= 2*want {
				t.Fatalf("the hint came back after %v, want from %v", d, want)
			}
		}
	})
}

// A machine asked after a pass that found no Citadel keeps the source of its
// address: configured or cache.
func TestAskingAnotherMachineKeepsTheSourceOfTheAddress(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		ctx := t.Context()
		r.newMachine(ctx, "towerb", "towerb", "192.168.1.30", discovery.RoleTower, 1, r.ca)
		cache := discovery.NewMemCache()
		_ = cache.Add(ctx, discovery.CacheEntry{Addr: "192.168.1.30:7443", MachineID: "towerb", Source: discovery.SourceGuildConfig, AddedAt: r.clk.Now()})
		loc := r.locator("tower1", r.client("tower1", "tower1"), fixed(1, "gone"), func(c *discovery.LocatorConfig) {
			c.Cache, c.Disabled, c.AskOthers = cache, true, true
		})
		got, err := loc.Find(ctx)
		if err != nil || got.Addr != "192.168.1.30:7443" || got.Source != discovery.SourceCache {
			t.Fatalf("asked %+v, %v; want towerb from the cache", got, err)
		}
	})
}
