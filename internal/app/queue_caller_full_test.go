//go:build full

package app

import (
	"context"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/cli"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// Part of CA-CIDADELA-14, the half of the cli.call: the Citadel believes the character
// a Tower names as the caller only when the registry puts it on that Tower.
func TestATowerCannotCallAsACharacterThatRunsOnAnotherMachine(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	b := r.node("b", a)
	var done applied
	var mu sync.Mutex
	where := "somewhere-else"
	a.setup = func(e *Engine) {
		e.deps.AgentMachine = func(context.Context, string) (string, bool) { mu.Lock(); defer mu.Unlock(); return where, true }
		e.Commands().Handle("done", done.handler(false))
	}
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	r.join(b, a, clock.Real{})
	r.start(b, clock.Real{})
	r.waitFor("b to connect", func() bool { return b.connected(1) })

	gate := cli.NewGate(cli.GateConfig{Commands: queuedCommands(), Next: forwarder{b.e}})
	call := func() (*localchan.Call, string) {
		id, err := localchan.NewID(clock.Real{})
		if err != nil {
			t.Fatal(err)
		}
		return &localchan.Call{
			Request: localchan.Request{V: 1, ID: id, AgentID: "alice", Cmd: "done"},
			Caller:  localchan.Caller{Identified: true, AgentID: "alice", TerminalID: "t1"},
		}, id
	}
	c, id := call()
	resp := gate.Forward(t.Context(), c)
	if resp.OK {
		t.Fatalf("a call as a character of another machine: %+v", resp)
	}
	if done.count(id) != 0 {
		t.Fatal("the command ran for a caller the registry puts elsewhere")
	}

	mu.Lock()
	where = b.id
	mu.Unlock()
	c, id = call()
	if resp := gate.Forward(t.Context(), c); !resp.OK {
		t.Fatalf("a call as a character of the Tower: %+v", resp)
	}
	if done.count(id) != 1 {
		t.Fatalf("the command ran %d times for a character of the Tower", done.count(id))
	}
}
