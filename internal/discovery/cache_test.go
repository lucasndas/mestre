package discovery_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/discovery"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
)

var day = 24 * time.Hour

func TestTheCacheKeepsTheHistoryOfEachAddressAndForgetsTheOldOnes(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	caches := map[string]func(t *testing.T) (discovery.Cache, func() discovery.Cache){
		"memory": func(*testing.T) (discovery.Cache, func() discovery.Cache) {
			c := discovery.NewMemCache()
			return c, func() discovery.Cache { return c }
		},
		"machine database": func(t *testing.T) (discovery.Cache, func() discovery.Cache) {
			path := filepath.Join(t.TempDir(), "maquina.db")
			open := func() *store.DB {
				db, err := guildtpl.OpenMachine(context.Background(), path, store.Options{Synchronous: "OFF"})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { db.Close() })
				return db
			}
			// the second handle is the machine after a restart: the cache is durable
			first := open()
			return discovery.NewSQLCache(first), func() discovery.Cache {
				first.Close()
				return discovery.NewSQLCache(open())
			}
		},
	}
	for name, mk := range caches {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			c, reopen := mk(t)
			// two machines of guild.config that never worked, one Citadel that did
			must(t, c.Add(ctx, discovery.CacheEntry{Addr: "10.0.0.1:7443", MachineID: "m1", Source: discovery.SourceGuildConfig, AddedAt: t0.Add(-40 * day)}))
			must(t, c.Add(ctx, discovery.CacheEntry{Addr: "10.0.0.2:7443", MachineID: "m2", Source: discovery.SourceGuildConfig, AddedAt: t0.Add(-day)}))
			must(t, c.Success(ctx, "10.0.0.3:7443", "m3", discovery.SourceBroadcast, t0.Add(-2*day)))
			must(t, c.Success(ctx, "10.0.0.2:7443", "m2", discovery.SourceMDNS, t0)) // m2 became the Citadel
			// adding an address again does not erase what happened to it
			must(t, c.Add(ctx, discovery.CacheEntry{Addr: "10.0.0.2:7443", Source: discovery.SourceGuildConfig, AddedAt: t0.Add(time.Hour)}))

			c = reopen()
			es, err := c.List(ctx)
			if err != nil || len(es) != 3 {
				t.Fatalf("List = %+v, %v", es, err)
			}
			discovery.SortForTrying(es)
			order := []string{es[0].Addr, es[1].Addr, es[2].Addr}
			if order[0] != "10.0.0.2:7443" || order[1] != "10.0.0.3:7443" || order[2] != "10.0.0.1:7443" {
				t.Fatalf("order = %v, want the most recent success, the older one, and the one that never worked", order)
			}
			if es[0].MachineID != "m2" || !es[0].LastSuccess.Equal(t0) || es[0].Source != discovery.SourceGuildConfig || !es[0].AddedAt.Equal(t0.Add(-day)) {
				t.Fatalf("history of m2 = %+v", es[0])
			}

			must(t, c.Prune(ctx, t0.Add(-30*day)))
			es, _ = c.List(ctx)
			if len(es) != 2 {
				t.Fatalf("after pruning 30 days: %+v", es)
			}
			for _, e := range es {
				if e.Addr == "10.0.0.1:7443" {
					t.Fatal("an address unused for 40 days stayed")
				}
			}
		})
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
