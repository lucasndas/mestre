package discovery

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Source is where an address came from.
type Source string

// The sources of an address (docs/conectividade/descoberta.md#cache).
const (
	SourceConfigured  Source = "configured"
	SourceCache       Source = "cache"
	SourceBroadcast   Source = "broadcast"
	SourceMDNS        Source = "mdns"
	SourceGuildConfig Source = "guild.config"
)

// DefaultCacheMaxAge is how long an address stays without a success.
const DefaultCacheMaxAge = 30 * 24 * time.Hour

// CacheEntry is an address of a machine of the Guild.
type CacheEntry struct {
	// Addr is "host:port" of the WSS port of the engine.
	Addr string
	// MachineID is the machine at the address, when known.
	MachineID string
	// Source is where the address came from the first time.
	Source Source
	// AddedAt is when it was first remembered; LastSuccess when a
	// connection to it last worked (zero if never).
	AddedAt, LastSuccess time.Time
}

// activity is the last time the entry was of any use.
func (e CacheEntry) activity() time.Time {
	if e.LastSuccess.After(e.AddedAt) {
		return e.LastSuccess
	}
	return e.AddedAt
}

// Cache remembers the addresses of the Guild between runs: in the machine
// database for the engine (SQLCache), in a small file for the Guardian.
type Cache interface {
	// List returns the entries, in no particular order.
	List(ctx context.Context) ([]CacheEntry, error)
	// Add remembers an address that has not worked yet (the machines of
	// guild.config). An address already there keeps its history.
	Add(ctx context.Context, e CacheEntry) error
	// Success records that a connection to addr worked at the given time,
	// and who answered.
	Success(ctx context.Context, addr, machineID string, src Source, at time.Time) error
	// Prune forgets the entries whose last use is before the cutoff.
	Prune(ctx context.Context, before time.Time) error
}

// SortForTrying orders entries as the discovery tries them: the ones that
// worked, the most recent first, then the others, the most recently added
// first. The order is stable for equal times.
func SortForTrying(es []CacheEntry) {
	sort.SliceStable(es, func(i, j int) bool {
		a, b := es[i], es[j]
		aw, bw := !a.LastSuccess.IsZero(), !b.LastSuccess.IsZero()
		switch {
		case aw != bw:
			return aw
		case aw:
			return a.LastSuccess.After(b.LastSuccess)
		default:
			return a.AddedAt.After(b.AddedAt)
		}
	})
}

// MemCache is a Cache in memory.
type MemCache struct {
	mu      sync.Mutex
	entries map[string]CacheEntry
}

var _ Cache = (*MemCache)(nil)

// NewMemCache returns an empty cache.
func NewMemCache() *MemCache { return &MemCache{entries: map[string]CacheEntry{}} }

// List implements Cache.
func (m *MemCache) List(context.Context) ([]CacheEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]CacheEntry, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr < out[j].Addr })
	return out, nil
}

// Add implements Cache.
func (m *MemCache) Add(_ context.Context, e CacheEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.entries[e.Addr]; ok {
		if e.MachineID != "" {
			old.MachineID = e.MachineID
			m.entries[e.Addr] = old
		}
		return nil
	}
	m.entries[e.Addr] = e
	return nil
}

// Success implements Cache.
func (m *MemCache) Success(_ context.Context, addr, machineID string, src Source, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[addr]
	if !ok {
		e = CacheEntry{Addr: addr, Source: src, AddedAt: at}
	}
	e.MachineID, e.LastSuccess = machineID, at
	m.entries[addr] = e
	return nil
}

// Prune implements Cache.
func (m *MemCache) Prune(_ context.Context, before time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for a, e := range m.entries {
		if e.activity().Before(before) {
			delete(m.entries, a)
		}
	}
	return nil
}
