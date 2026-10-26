//go:build full

package app

import (
	"context"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/cli"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The transport behind CA-PROVA-15 (the criterion itself is in the fast suite,
// in internal/orchestration/quests, which also asserts the step that keeps
// running): a done with a proof that waited in the outbox of a Tower is refused
// when the Citadel gets it, and the refusal comes to the character as an item
// `system` with the op_id of the request.
func TestARefusedDoneThatWaitedInTheOutboxComesBackAsASystemItemWithItsOpID(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	b := r.node("b", a)
	st := newDoneStack(t)
	a.setup = func(e *Engine) {
		e.deps.AgentMachine = func(context.Context, string) (string, bool) { return b.id, true }
		e.Commands().Handle("done", st.orc.Quests.Handlers()["done"])
	}
	var mu sync.Mutex
	var results []QueuedResult
	a.onQueued = func(q QueuedResult) {
		if err := st.orc.Inbox.QueuedResult(t.Context(), q.OpID, q.AgentID, q.Cmd, q.Response); err != nil {
			t.Errorf("the result was not put in the inbox: %v", err)
		}
		mu.Lock()
		results = append(results, q)
		mu.Unlock()
	}
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	r.join(b, a, clock.Real{})
	r.start(b, clock.Real{})
	r.waitFor("b to connect", func() bool { return b.connected(1) })

	gate := cli.NewGate(cli.GateConfig{Commands: cli.Default(), Next: forwarder{b.e}})
	id, err := localchan.NewID(clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	call := &localchan.Call{
		Request: localchan.Request{V: 1, ID: id, AgentID: st.dev.ID, Cmd: "done", Args: localchan.TextArgs("login implementado"), Flags: map[string]any{"proof": []any{"C9=x"}}},
		Caller:  localchan.Caller{Identified: true, AgentID: st.dev.ID, TerminalID: st.dev.TerminalID},
	}
	a.stop()
	r.waitFor("b to see the Citadel away", func() bool { return !b.e.Status().Connected })
	if resp := gate.Forward(t.Context(), call); !resp.OK || !queuedOf(resp) || opIDOf(resp) != id {
		t.Fatalf("with the Citadel away: %+v", resp)
	}
	r.start(a, clock.Real{})
	r.waitFor("the result of the done", func() bool { mu.Lock(); defer mu.Unlock(); return len(results) == 1 })
	mu.Lock()
	if q := results[0]; q.OpID != id || q.Response.OK || q.Response.ExitCode() != localchan.ExitUsage {
		t.Errorf("result: %+v", q)
	}
	mu.Unlock()
	if dones, _ := st.orc.Registry.Dones(t.Context(), st.dev.ID); len(dones) != 0 {
		t.Errorf("a refused done was registered: %+v", dones)
	}
	items, err := st.orc.Inbox.Store().All(t.Context(), st.dev.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, it := range items {
		found = found || it.Kind == inbox.KindSystem && it.Refs.OpID == id
	}
	if !found {
		t.Errorf("the character got no item with the refusal of op %s: %+v", id, items)
	}
}
