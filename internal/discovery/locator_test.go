package discovery_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/discovery"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/testkit/discorig"
	"github.com/lucasndas/mestre/internal/testkit/protorig"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/pkg/clock"
)

// recVerifier remembers which addresses were checked and how it went.
type recVerifier struct {
	inner discovery.Verifier
	mu    sync.Mutex
	calls []string
	times []time.Time
	errs  map[string]error
}

func (v *recVerifier) Verify(ctx context.Context, addr string) (string, error) {
	id, err := v.inner.Verify(ctx, addr)
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.errs == nil {
		v.errs = map[string]error{}
	}
	v.calls = append(v.calls, addr)
	v.times = append(v.times, clock.Real{}.Now())
	v.errs[addr] = err
	return id, err
}

func (v *recVerifier) called() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return slices.Clone(v.calls)
}

// recording wraps the Verifier of a Locator config.
func recording(c *discovery.LocatorConfig) *recVerifier {
	rv := &recVerifier{inner: c.Verifier}
	c.Verifier = rv
	return rv
}

// CA-DESC-02
func TestAnAddressWithACertificateOfAnotherCAIsDiscardedAndTheNextIsTried(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		evilID string
		evilCA bool
	}{
		"a certificate of another CA":                    {"citadel", true},
		"a machine of the Guild that is not the Citadel": {"tower2", false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				r := newRig(t)
				ca := r.ca
				if tc.evilCA {
					var err error
					if ca, err = transport.NewCA("guild-2", r.clk); err != nil {
						t.Fatal(err)
					}
				}
				r.newMachine(t.Context(), "evil", tc.evilID, "192.168.1.66", discovery.RoleTower, 1, ca)
				real := r.newMachine(t.Context(), "citadel", "citadel", "192.168.1.10", discovery.RoleCitadel, 1, r.ca)
				cache := discovery.NewMemCache()
				now := r.clk.Now()
				_ = cache.Success(t.Context(), "192.168.1.10:7443", "citadel", discovery.SourceBroadcast, now.Add(-time.Hour))
				_ = cache.Success(t.Context(), "192.168.1.66:7443", "", discovery.SourceBroadcast, now) // the freshest is tried first

				var rv *recVerifier
				loc := r.locator("tower1", r.client("tower1", "tower1"), fixed(1, "citadel"), func(c *discovery.LocatorConfig) {
					c.Cache, c.Search = cache, discovery.SearchConfig{}
					rv = recording(c)
				})
				target, err := loc.Find(t.Context())
				if err != nil || target.Addr != real.addr() || target.MachineID != "citadel" {
					t.Fatalf("Find = %+v, %v", target, err)
				}
				if got := rv.called(); len(got) != 2 || got[0] != "192.168.1.66:7443" {
					t.Fatalf("checked %v, want the impostor first and then the Citadel", got)
				}
				if err := rv.errs["192.168.1.66:7443"]; tc.evilCA && err == nil {
					t.Fatal("the certificate of another CA passed the identity check")
				}
				// what failed the check is not a success of the cache
				es, _ := cache.List(t.Context())
				for _, e := range es {
					if e.Addr == "192.168.1.66:7443" && !e.LastSuccess.Equal(now) {
						t.Fatalf("the impostor was recorded as a success: %+v", e)
					}
				}
			})
		})
	}
}

// CA-DESC-04
func TestWithTheBroadcastBlockedTheAddressOfTheMDNSAnnouncementIsUsed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		c := r.newMachine(t.Context(), "citadel", "citadel", "192.168.1.10", discovery.RoleCitadel, 1, r.ca)
		c.answer(t.Context())
		discorig.Announce(r.net, "citadel", discovery.Announcement{
			GuildID: guildID, MachineID: "citadel", Role: discovery.RoleCitadel, Epoch: 1, EnginePort: enginePort, GuardianPort: 7444,
		})
		r.net.BlockBroadcast(true)
		loc := r.locator("tower1", r.client("tower1", "tower1"), fixed(1, "citadel"), nil)
		target, err := loc.Find(t.Context())
		if err != nil || target.Addr != "192.168.1.10:7443" || target.Source != discovery.SourceMDNS {
			t.Fatalf("Find = %+v, %v; want the address of the mDNS", target, err)
		}
	})
}

func TestTheOrderIsConfiguredThenCacheThenTheSearch(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		c := r.newMachine(t.Context(), "citadel", "citadel", "192.168.1.10", discovery.RoleCitadel, 1, r.ca)
		c.answer(t.Context())
		cache := discovery.NewMemCache()
		now := r.clk.Now()
		_ = cache.Success(t.Context(), "192.168.1.31:7443", "", discovery.SourceBroadcast, now.Add(-2*time.Hour))
		_ = cache.Success(t.Context(), "192.168.1.32:7443", "", discovery.SourceBroadcast, now.Add(-time.Hour))
		_ = cache.Add(t.Context(), discovery.CacheEntry{Addr: "192.168.1.33:7443", Source: discovery.SourceGuildConfig, AddedAt: now})
		var rv *recVerifier
		loc := r.locator("tower1", r.client("tower1", "tower1"), fixed(1, "citadel"), func(cfg *discovery.LocatorConfig) {
			cfg.Cache, cfg.Configured = cache, []string{"192.168.1.30"} // the port is the default one
			rv = recording(cfg)
		})
		target, err := loc.Find(t.Context())
		if err != nil || target.Addr != "192.168.1.10:7443" {
			t.Fatalf("Find = %+v, %v", target, err)
		}
		want := "192.168.1.30:7443 192.168.1.32:7443 192.168.1.31:7443 192.168.1.33:7443 192.168.1.10:7443"
		if got := strings.Join(rv.called(), " "); got != want {
			t.Fatalf("checked\n%s\nwant\n%s", got, want)
		}
		// the answer is kept for the next time
		es, _ := cache.List(t.Context())
		for _, e := range es {
			if e.Addr == "192.168.1.10:7443" && (e.MachineID != "citadel" || e.LastSuccess.IsZero()) {
				t.Fatalf("the Citadel was not recorded: %+v", e)
			}
		}
	})
}

func TestAMachineOutsideTheLocalNetworkTriesTheAddressesOfGuildConfigAfterAChangeOfCitadel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		r.newMachine(t.Context(), "old", "citadel", "10.1.0.1", discovery.RoleTower, 2, r.ca)
		r.newMachine(t.Context(), "new", "citadel2", "10.1.0.2", discovery.RoleCitadel, 2, r.ca)
		var rv *recVerifier
		loc := r.locator("tower1", r.client("tower1", "tower1"), fixed(2, "citadel2"), func(cfg *discovery.LocatorConfig) {
			cfg.Disabled = true // no broadcast and no mDNS beyond the local network
			rv = recording(cfg)
		})
		if err := loc.RememberMachines(t.Context(), []discovery.MachineAddrs{
			{MachineID: "citadel", Addrs: []string{"10.1.0.1"}, Port: 7443},
			{MachineID: "citadel2", Addrs: []string{"10.1.0.2"}, Port: 7443},
		}); err != nil {
			t.Fatal(err)
		}
		target, err := loc.Find(t.Context())
		if err != nil || target.Addr != "10.1.0.2:7443" || target.MachineID != "citadel2" {
			t.Fatalf("Find = %+v, %v", target, err)
		}
		// the machine that is no longer the Citadel is not even dialed
		if got := rv.called(); len(got) != 1 {
			t.Fatalf("checked %v, want only the Citadel of the certificate", got)
		}
		if r.net.Datagrams() != 0 {
			t.Fatal("the discovery was off and datagrams were sent")
		}
	})
}

func TestWithoutAnyAddressTheSearchRepeatsWithTheBackoffAndTheStateIsReported(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		var states []bool
		var mu sync.Mutex
		var rv *recVerifier
		loc := r.locator("tower1", r.client("tower1", "tower1"), fixed(1, "citadel"), func(cfg *discovery.LocatorConfig) {
			cfg.Configured = []string{"192.168.1.10"}
			cfg.Search = discovery.SearchConfig{} // nothing to search with: the attempts are the checks of the configured address
			cfg.OnState = func(found bool) { mu.Lock(); states = append(states, found); mu.Unlock() }
			rv = recording(cfg)
		})
		start := r.clk.Now()
		done := make(chan error, 1)
		go func() { _, err := loc.Run(t.Context()); done <- err }()
		_ = clock.Sleep(t.Context(), r.clk, 8*time.Second) // attempts at 0 s, 1 s, 3 s and 7 s
		if !loc.NotFound() {
			t.Fatal("the state is not 'Citadel not found'")
		}
		r.newMachine(t.Context(), "citadel", "citadel", "192.168.1.10", discovery.RoleCitadel, 1, r.ca)
		_ = clock.Sleep(t.Context(), r.clk, 8*time.Second)
		if err := <-done; err != nil {
			t.Fatalf("Run = %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(states) != 2 || states[0] || !states[1] {
			t.Fatalf("states = %v, want [false true]", states)
		}
		want := []time.Duration{0, time.Second, 3 * time.Second, 7 * time.Second, 15 * time.Second}
		if len(rv.times) != len(want) {
			t.Fatalf("%d attempts, want %d", len(rv.times), len(want))
		}
		for i, w := range want {
			if got := rv.times[i].Sub(start); got != w {
				t.Fatalf("attempt %d at %v, want %v", i, got, w)
			}
		}
	})
}

func TestAddressesWithoutSuccessFor30DaysLeaveTheCache(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		c := r.newMachine(t.Context(), "citadel", "citadel", "192.168.1.10", discovery.RoleCitadel, 1, r.ca)
		_ = c
		cache := discovery.NewMemCache()
		var rv *recVerifier
		loc := r.locator("tower1", r.client("tower1", "tower1"), fixed(1, "citadel"), func(cfg *discovery.LocatorConfig) {
			cfg.Cache, cfg.Search = cache, discovery.SearchConfig{}
			rv = recording(cfg)
		})
		_ = cache.Success(t.Context(), "192.168.1.77:7443", "", discovery.SourceBroadcast, r.clk.Now())
		_ = cache.Success(t.Context(), "192.168.1.10:7443", "citadel", discovery.SourceBroadcast, r.clk.Now())
		_ = clock.Sleep(t.Context(), r.clk, 29*24*time.Hour)
		_ = cache.Success(t.Context(), "192.168.1.10:7443", "citadel", discovery.SourceBroadcast, r.clk.Now())
		_ = clock.Sleep(t.Context(), r.clk, 2*24*time.Hour) // the other address is 31 days old
		if _, err := loc.Find(t.Context()); err != nil {
			t.Fatal(err)
		}
		if slices.Contains(rv.called(), "192.168.1.77:7443") {
			t.Fatal("an address without success for 31 days was tried")
		}
		loc.Connected(t.Context()) // a connection opens: the old address leaves the cache
		es, _ := cache.List(t.Context())
		if len(es) != 1 || es[0].Addr != "192.168.1.10:7443" {
			t.Fatalf("cache = %+v", es)
		}
	})
}

// tower is a Tower machine wired as the engine wires it: the protocol client
// over the transport, the Locator behind the dialer and the hooks.
type tower struct {
	parts protorig.Parts
	loc   *discovery.Locator
	cache *discovery.MemCache
	rv    *recVerifier
	dials atomic.Int32
}

func (r *rig) startTower(ctx context.Context, epoch uint64, citadel string) *tower {
	r.t.Helper()
	r.net.SetIP("tower1", "192.168.1.5")
	cfg, parts := protorig.Config(r.clk, "tower1", epoch, citadel)
	tw := &tower{parts: parts, cache: discovery.NewMemCache()}
	known := func() discovery.Known {
		return discovery.Known{Epoch: parts.Epochs.Epoch(), CitadelMachineID: parts.Epochs.Certificate().CitadelMachineID}
	}
	tc := r.client("tower1", "tower1")
	tw.loc = r.locator("tower1", tc, known, func(c *discovery.LocatorConfig) {
		c.Cache = tw.cache
		tw.rv = recording(c)
	})
	cfg.Hooks.Rediscover = tw.loc.Rediscover
	cfg.Hooks.OnConnect = func(*protocol.Session) { tw.loc.Connected(ctx) }
	cli, err := protocol.NewClient(cfg, protocol.TransportDialer(tc, func() (string, string) {
		tw.dials.Add(1)
		return tw.loc.Target()
	}))
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := tw.loc.Run(ctx); err != nil {
		r.t.Fatalf("the first discovery: %v", err)
	}
	done := make(chan struct{})
	go func() { _ = cli.Run(ctx); close(done) }()
	r.t.Cleanup(func() { <-done })
	return tw
}

// CA-DESC-05
func TestACitadelThatChangedIPIsFoundAgainAfterThreeFailuresAndTheCacheIsUpdated(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		c := r.newMachine(ctx, "citadel", "citadel", "192.168.1.10", discovery.RoleCitadel, 1, r.ca)
		c.serveProtocol(ctx)
		c.answer(ctx)
		tw := r.startTower(ctx, 1, "citadel")
		synctest.Wait()
		if !c.protocol.Link("tower1").Connected() {
			t.Fatal("the Tower did not connect")
		}
		if es, _ := tw.cache.List(ctx); len(es) != 1 || es[0].Addr != "192.168.1.10:7443" || es[0].LastSuccess.IsZero() {
			t.Fatalf("the cache after the first connection = %+v", es)
		}

		before := tw.dials.Load()
		c.moveTo("192.168.1.99") // the lease ended: the connection dies and the old address answers nobody
		_ = clock.Sleep(ctx, r.clk, time.Minute)
		synctest.Wait()
		if !c.protocol.Link("tower1").Connected() {
			t.Fatal("the Tower did not find the Citadel at its new address")
		}
		// the old address failed three times before the machine looked again,
		// then one more dial opened the connection
		if got := tw.dials.Load() - before; got != 4 {
			t.Fatalf("%d dials after the change, want 3 failures and the connection", got)
		}
		if got, _ := tw.loc.Target(); got != "192.168.1.99:7443" {
			t.Fatalf("target = %s", got)
		}
		es, _ := tw.cache.List(ctx)
		var fresh bool
		for _, e := range es {
			fresh = fresh || e.Addr == "192.168.1.99:7443" && e.MachineID == "citadel" && !e.LastSuccess.IsZero()
		}
		if !fresh {
			t.Fatalf("the cache does not know the new address: %+v", es)
		}
	})
}

// CA-DESC-08
func TestATowerFollowsTheCitadelOfAHigherEpochThatAnotherTowerPointsTo(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		// the Citadel of epoch 2 does not answer the broadcast; a Tower tells where it is
		c2 := r.newMachine(ctx, "citadel2", "citadel2", "192.168.1.20", discovery.RoleCitadel, 2, r.ca)
		c2.serveProtocol(ctx)
		tb := r.newMachine(ctx, "towerb", "towerb", "192.168.1.30", discovery.RoleTower, 2, r.ca)
		tb.citadel, tb.citadelI = "citadel2", "192.168.1.20"
		tb.answer(ctx)

		tw := r.startTower(ctx, 1, "citadel")
		synctest.Wait()
		if !c2.protocol.Link("tower1").Connected() {
			t.Fatal("the Tower did not connect to the Citadel of epoch 2")
		}
		if got := tw.parts.Epochs.Followed(); len(got) != 1 || got[0] != 2 {
			t.Fatalf("the Tower followed %v, want the new certificate of epoch 2", got)
		}
		if got, _ := tw.loc.Target(); got != "192.168.1.20:7443" {
			t.Fatalf("target = %s", got)
		}
	})
}

// CA-DESC-08
func TestATowerDoesNotConnectToACitadelOfAnOlderEpoch(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		old := r.newMachine(t.Context(), "citadel", "citadel", "192.168.1.10", discovery.RoleCitadel, 1, r.ca)
		old.answer(t.Context())
		var rv *recVerifier
		loc := r.locator("tower1", r.client("tower1", "tower1"), fixed(2, "citadel2"), func(c *discovery.LocatorConfig) { rv = recording(c) })
		_, err := loc.Find(t.Context())
		if !errors.Is(err, discovery.ErrNotFound) {
			t.Fatalf("Find = %v, want ErrNotFound", err)
		}
		if got := rv.called(); len(got) != 0 {
			t.Fatalf("the Tower opened TLS to %v, a Citadel of epoch 1", got)
		}
	})
}

func TestAMachineThatIsNotTheCitadelSendsTheTowerToTheCitadelOfTheCertificate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		// the cache points to the old Citadel, which is now a Tower of epoch 2
		oldC := r.newMachine(ctx, "old", "citadel", "192.168.1.10", discovery.RoleTower, 2, r.ca)
		oldC.citadel, oldC.citadelI = "citadel2", "192.168.1.20"
		oldC.serveProtocol(ctx)
		newC := r.newMachine(ctx, "new", "citadel2", "192.168.1.20", discovery.RoleCitadel, 2, r.ca)
		newC.serveProtocol(ctx)
		r.net.SetIP("tower1", "192.168.1.5")
		cfg, parts := protorig.Config(r.clk, "tower1", 1, "citadel") // still on epoch 1
		known := func() discovery.Known {
			return discovery.Known{Epoch: parts.Epochs.Epoch(), CitadelMachineID: parts.Epochs.Certificate().CitadelMachineID}
		}
		cache := discovery.NewMemCache()
		_ = cache.Success(ctx, "192.168.1.10:7443", "citadel", discovery.SourceBroadcast, r.clk.Now())
		_ = cache.Add(ctx, discovery.CacheEntry{Addr: "192.168.1.20:7443", MachineID: "citadel2", Source: discovery.SourceGuildConfig, AddedAt: r.clk.Now()})
		tc := r.client("tower1", "tower1")
		loc := r.locator("tower1", tc, known, func(c *discovery.LocatorConfig) { c.Cache, c.Disabled = cache, true })
		cfg.Hooks.Rediscover = loc.Rediscover
		cli, err := protocol.NewClient(cfg, protocol.TransportDialer(tc, loc.Target))
		if err != nil {
			t.Fatal(err)
		}
		if tg, err := loc.Run(ctx); err != nil || tg.Addr != "192.168.1.10:7443" {
			t.Fatalf("first pass = %+v, %v", tg, err)
		}
		done := make(chan struct{})
		go func() { _ = cli.Run(ctx); close(done) }()
		defer func() { cancel(); <-done }()
		_ = clock.Sleep(ctx, r.clk, 10*time.Second)
		synctest.Wait()
		// the old Citadel answered 4007 with the certificate of epoch 2; the Tower followed it and went to the new one
		if !newC.protocol.Link("tower1").Connected() {
			t.Fatal("the Tower did not reach the Citadel named by the certificate")
		}
		if got := parts.Epochs.Followed(); len(got) != 1 || got[0] != 2 {
			t.Fatalf("followed %v", got)
		}
	})
}
