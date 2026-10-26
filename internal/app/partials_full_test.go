//go:build full

package app

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// CA-LOCAL-17: the signs of life of a long command (`ask`: queued, delivered,
// answering…) cross the cli.call of a Tower: the CLI of the character on the
// Tower receives them in order, and then the final response.
func TestThePartialResponsesOfACommandCrossTheCLICallOfATower(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	b := r.node("b", a)
	a.setup = func(e *Engine) {
		e.deps.AgentMachine = func(context.Context, string) (string, bool) { return b.id, true }
		e.Commands().Handle("ask", localchan.HandlerFunc(func(_ context.Context, c *localchan.Call) localchan.Response {
			c.Partial(map[string]string{"status": "queued"})
			c.Partial(map[string]string{"status": "delivered"})
			return localchan.Response{OK: true, Data: json.RawMessage(`{"status":"answered"}`)}
		}))
	}
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	r.join(b, a, clock.Real{})
	r.start(b, clock.Real{})
	r.waitFor("b to connect", func() bool { return b.connected(1) })

	var mu sync.Mutex
	var got []string
	call := &localchan.Call{
		Request: localchan.Request{V: 1, ID: "ask-1", AgentID: "alice", Cmd: "ask"},
		Caller:  localchan.Caller{Identified: true, AgentID: "alice", TerminalID: "t1"},
	}
	call.SetPartialSink(func(data json.RawMessage) { mu.Lock(); got = append(got, string(data)); mu.Unlock() })
	resp := forwarder{b.e}.Forward(t.Context(), call)
	if !resp.OK {
		t.Fatalf("final response: %+v", resp)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] != `{"status":"queued"}` || got[1] != `{"status":"delivered"}` {
		t.Fatalf("partials = %v; want queued then delivered", got)
	}
}
