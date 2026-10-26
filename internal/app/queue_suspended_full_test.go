//go:build full

package app

import (
	"context"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/cli"
	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// Part of CA-CLI-CONV-05 and CA-CLI-CONV-11, as the test above. A Citadel
// that is up but suspended (it has not confirmed the epoch yet) refuses the
// call it receives; the Tower then queues the commands that need no answer
// now, under another message id (the Citadel kept the first one), and the
// Citadel runs it once when it acts.
func TestACommandThatNeedsNoAnswerWaitsForASuspendedCitadelToAct(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	b := r.node("b", a)
	var done applied
	var mu sync.Mutex
	var delivered []QueuedResult
	a.setup = func(e *Engine) {
		e.deps.AgentMachine = func(context.Context, string) (string, bool) { return b.id, true }
		e.Commands().Handle("done", done.handler(false))
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
	a.stop()
	r.lock.Hold()
	r.start(a, clock.Real{})
	r.start(b, clock.Real{})
	r.waitFor("b to connect to the suspended Citadel", func() bool {
		return b.connected(1) && a.e.Status().Fence == guild.FenceSuspended
	})

	gate := cli.NewGate(cli.GateConfig{Commands: queuedCommands(), Next: forwarder{b.e}})
	id, err := localchan.NewID(clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	call := &localchan.Call{
		Request: localchan.Request{V: 1, ID: id, AgentID: "alice", Cmd: "done"},
		Caller:  localchan.Caller{Identified: true, AgentID: "alice", TerminalID: "t1"},
	}
	resp := gate.Forward(t.Context(), call)
	op := opIDOf(resp)
	if !resp.OK || resp.ExitCode() != 0 || !queuedOf(resp) || op == "" {
		t.Fatalf("with the Citadel suspended: %+v", resp)
	}
	if done.count(id) != 0 {
		t.Fatal("a suspended Citadel ran the command")
	}

	r.lock.Release()
	r.waitFor("the Citadel to act", a.acting)
	r.waitFor("the Citadel to run the queued done", func() bool { return done.count(id) >= 1 })
	r.waitFor("the result delivered", func() bool { mu.Lock(); defer mu.Unlock(); return len(delivered) == 1 })
	r.waitFor("the outbox of b to be empty", func() bool {
		rows, err := protocolPending(t, b)
		return err == nil && rows == 0
	})
	mu.Lock()
	defer mu.Unlock()
	if q := delivered[0]; q.OpID != op || !q.Response.OK || q.AgentID != "alice" {
		t.Errorf("result = %+v, want op_id %s", q, op)
	}
	if done.count(id) != 1 {
		t.Errorf("the command ran %d times", done.count(id))
	}
}
