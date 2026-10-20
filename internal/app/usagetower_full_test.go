//go:build full

package app

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/usage"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Part of CA-USO-14, over the real connection: the Tower reads the transcript of its
// Codex character and the reading reaches the limits of use of the Citadel.
func TestAReadingOfATowerReachesTheCitadelOverTheConnection(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	b := r.node("b", a)
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	r.join(b, a, clock.Real{})
	r.start(b, clock.Real{})
	r.waitFor("b to connect", func() bool { return b.connected(1) })

	spy := &lockedUsage{}
	reader := newUsageReader(&usageReader{svc: spy, dir: subjects{"cx": {ID: "cx", Provider: providers.Codex}}, clk: clock.Real{}})
	a.e.period.Store(&periodHooks{usage: newHookUsage(spy, reader, nil),
		agentMachine: func(_ context.Context, id string) (string, bool) { return b.id, id == "cx" }})

	profile := t.TempDir()
	path := filepath.Join(profile, "sessions", "2026", "10", "14", "rollout-2026-10-14T12-00-00-019a0000-0000-7000-8000-000000000001.jsonl")
	writeCodexRollout(t, path, 0, 3)
	tower := newUsageReader(&usageReader{svc: towerUsageSink{tell: b.e.tellCitadel}, machine: b.id, clk: clock.Real{}, log: slog.New(slog.DiscardHandler), noSheet: true,
		dir: subjects{"cx": {ID: "cx", Provider: providers.Codex}},
		alive: func(context.Context) ([]characters.Character, error) {
			return []characters.Character{{ID: "cx", Machine: b.id, State: characters.Busy}}, nil
		},
		adapters: providers.NewRegistry(providers.Options{}),
		src: UsageSources{Profile: func(context.Context, characters.Character) (UsageProfile, bool) {
			return UsageProfile{Dir: profile}, true
		}}})
	tower.Note(providers.Event{AgentID: "cx", Event: providers.EventTurnEnd, SessionID: "019a0000-0000-7000-8000-000000000001", TranscriptPath: path})
	if err := tower.Pass(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.waitFor("the reading to reach the Citadel", func() bool { return spy.count() == 1 })
	if got := spy.first(); got.InputTokens != 12000 {
		t.Errorf("the Citadel took %+v, want the tokens of the first turn", got)
	}
}

// lockedUsage is a usageSpy that the connection of the rig may write from its own goroutine.
type lockedUsage struct {
	mu sync.Mutex
	usageSpy
}

func (l *lockedUsage) Reading(ctx context.Context, sub usage.Subject, u providers.Usage, rd usage.Reading) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.usageSpy.Reading(ctx, sub, u, rd)
}

func (l *lockedUsage) count() int { l.mu.Lock(); defer l.mu.Unlock(); return len(l.readings) }
func (l *lockedUsage) first() providers.Usage {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.readings[0]
}
