//go:build full

package app

import (
	"context"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// CA-CIDADELA-10, the half of the events of the engine of the Tower: with the
// Citadel away, an event of a character of the Tower waits in the outbox; when
// the Citadel is back it is applied once, and the same message sent again (the
// Tower sends its queue after every reconnection) is applied no more. The `done`
// and the `mestre list` are in queue_done_full_test.go.
func TestTheEventsOfATowerWaitForTheCitadelAndAreAppliedOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	b := r.node("b", a)
	var mu sync.Mutex
	var got []providers.Event
	a.setup = func(e *Engine) {
		e.deps.AgentMachine = func(context.Context, string) (string, bool) { return b.id, true }
		e.deps.OnAgentEvent = func(machine string, ev providers.Event, _ hook.Answer) {
			mu.Lock()
			got = append(got, ev)
			mu.Unlock()
		}
	}
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	r.join(b, a, clock.Real{})
	r.start(b, clock.Real{})
	r.waitFor("b to connect", func() bool { return b.connected(1) })

	a.stop()
	r.waitFor("b to see the Citadel away", func() bool { return !b.e.Status().Connected })
	b.e.onHookEvent(providers.Event{AgentID: "dev-1", Event: providers.EventTurnEnd, LastMessage: "feito"}, hook.Answer{})
	mu.Lock()
	if len(got) != 0 {
		t.Fatalf("the Citadel is away and got %d events", len(got))
	}
	mu.Unlock()

	r.start(a, clock.Real{})
	r.waitFor("the event to arrive", func() bool { mu.Lock(); defer mu.Unlock(); return len(got) == 1 })

	// the same message sent again, as the queue is after a reconnection
	payload, err := agentEventPayload(providers.Event{AgentID: "dev-2", Event: providers.EventTurnEnd}, hook.Answer{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.waitFor("b to connect to the Citadel", func() bool { return b.connected(1) })
	link := b.e.tow.client.Link()
	id, err := link.Send(t.Context(), protocol.Outgoing{Type: protocol.TypeAgentEvent, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := link.Send(t.Context(), protocol.Outgoing{Type: protocol.TypeAgentEvent, Payload: payload, ID: id}); err != nil {
		t.Fatal(err)
	}
	r.waitFor("the second event", func() bool { mu.Lock(); defer mu.Unlock(); return len(got) >= 2 })
	r.waitFor("the outbox of b to be empty", func() bool { // the Citadel acknowledged both sends
		var n int
		err := b.e.mdb.Reader().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM outbox WHERE type = 'agent.event'`).Scan(&n)
		return err == nil && n == 0
	})
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0].LastMessage != "feito" || got[1].AgentID != "dev-2" {
		t.Errorf("events applied by the Citadel: %+v; want each one once", got)
	}
}
