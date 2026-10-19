//go:build full

package app

import (
	"context"
	"sync"
	"testing"

	"github.com/lucasndas/mestre/internal/cli"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Part of CA-CLI-CONV-05: a `done` that went to the Citadel, ran there and
// lost its answer on the way back (the connection dropped) is told "queued"
// and goes to the outbox with the same id; when it arrives again the
// Citadel, which already ran it, runs nothing and hands the result of the
// first run to OnQueuedResult (the inbox of the caller), once.
func TestADoneThatRanAndLostItsAnswerStillDeliversItsResult(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	b := r.node("b", a)
	var done applied
	entered, release := make(chan struct{}, 1), make(chan struct{})
	var mu sync.Mutex
	var delivered []QueuedResult
	a.setup = func(e *Engine) {
		e.deps.AgentMachine = func(context.Context, string) (string, bool) { return b.id, true }
		h := done.handler(false)
		e.Commands().Handle("done", localchan.HandlerFunc(func(ctx context.Context, c *localchan.Call) localchan.Response {
			resp := h.Handle(ctx, c)
			entered <- struct{}{}
			<-release
			return resp
		}))
	}
	a.onQueued = func(q QueuedResult) {
		mu.Lock()
		delivered = append(delivered, q)
		mu.Unlock()
	}
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	r.join(b, a, clock.Real{})
	r.start(b, clock.Real{})
	r.waitFor("b to connect", func() bool { return b.connected(1) })

	gate := cli.NewGate(cli.GateConfig{Commands: queuedCommands(), Next: forwarder{b.e}})
	id, err := localchan.NewID(clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	answered := make(chan localchan.Response, 1)
	go func() {
		answered <- gate.Forward(t.Context(), &localchan.Call{
			Request: localchan.Request{V: 1, ID: id, AgentID: "alice", Cmd: "done"},
			Caller:  localchan.Caller{Identified: true, AgentID: "alice", TerminalID: "t1"},
		})
	}()
	<-entered // it runs on the Citadel now
	r.net.Cut("a", "b")
	resp := <-answered
	if !resp.OK || !queuedOf(resp) || opIDOf(resp) != id {
		t.Fatalf("the answer lost on the way: %+v; want queued with the id", resp)
	}
	close(release)
	r.waitFor("the result of the first run delivered", func() bool { mu.Lock(); defer mu.Unlock(); return len(delivered) == 1 })
	r.waitFor("the outbox of b to be empty", func() bool {
		rows, err := protocolPending(t, b)
		return err == nil && rows == 0
	})
	mu.Lock()
	defer mu.Unlock()
	if q := delivered[0]; q.OpID != id || !q.Response.OK || q.Cmd != "done" || q.AgentID != "alice" || len(delivered) != 1 {
		t.Fatalf("delivered %+v", delivered)
	}
	if n := done.count(id); n != 1 {
		t.Fatalf("the done ran %d times", n)
	}
}
