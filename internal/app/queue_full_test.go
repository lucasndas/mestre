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

// queuedCommands are the commands of later milestones that need no answer now
// (`done`, and `pr open` while GitHub is away), as the conventions treat them.
func queuedCommands() *cli.Registry {
	mk := func(name string) cli.Spec {
		return cli.Spec{Name: name, Group: cli.GroupCommunication, Summary: "test " + name, Usage: "mestre " + name,
			Who: cli.WhoAnyone, Queueable: true, MaxArgs: -1}
	}
	return cli.MustRegistry(mk("done"), mk("pr open"))
}

// applied counts what the Citadel did of each request.
type applied struct {
	mu   sync.Mutex
	runs map[string]int
}

func (a *applied) handler(fail bool) localchan.Handler {
	return localchan.HandlerFunc(func(_ context.Context, c *localchan.Call) localchan.Response {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.runs == nil {
			a.runs = map[string]int{}
		}
		a.runs[c.Request.ID]++
		if fail {
			return localchan.Fail("conflict", "there is no active task", "")
		}
		return localchan.OK(map[string]any{"text": "done recorded"})
	})
}

func (a *applied) count(id string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.runs[id]
}

// Part of CA-CLI-CONV-05 and CA-CLI-CONV-11 (CONV-11 is closed with `pr open` in queued_lease_test.go; CONV-05 closes with `done` in
// milestone 4): with the Citadel away, the Tower
// answers a command that needs no answer now with queued and an op_id, the
// call waits in its outbox, and the Citadel applies it once when it is back;
// the result, with the same op_id, goes to OnQueuedResult (the inbox of the
// caller in milestone 4), the error of a command that fails included. A
// command that needs the answer now still gets citadel_unavailable.
func TestACommandThatNeedsNoAnswerWaitsInTheOutboxWhileTheCitadelIsAway(t *testing.T) {
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
		e.Commands().Handle("pr open", done.handler(true))
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
	call := func(cmd string) (*localchan.Call, string) {
		id, err := localchan.NewID(clock.Real{})
		if err != nil {
			t.Fatal(err)
		}
		return &localchan.Call{
			Request: localchan.Request{V: 1, ID: id, AgentID: "alice", Cmd: cmd},
			Caller:  localchan.Caller{Identified: true, AgentID: "alice", TerminalID: "t1"},
		}, id
	}

	// the Citadel up: the command runs now, with its own answer
	c1, id1 := call("done")
	if resp := gate.Forward(t.Context(), c1); !resp.OK || resp.ExitCode() != 0 || done.count(id1) != 1 || string(resp.Data) == "" || queuedOf(resp) {
		t.Fatalf("with the Citadel up: %+v, ran %d times", resp, done.count(id1))
	}
	if d := b.e.citadelDebug(); d["reachable"] != true {
		t.Fatalf("debug of a connected Tower: %v", d)
	}

	a.stop()
	r.waitFor("b to see the Citadel away", func() bool { return !b.e.Status().Connected })
	if d := b.e.citadelDebug(); d["reachable"] != false || d["code"] != localchan.CodeCitadelUnavailable {
		t.Fatalf("debug of a Tower with the Citadel away: %v", d)
	}
	c2, id2 := call("done")
	resp := gate.Forward(t.Context(), c2)
	if !resp.OK || resp.ExitCode() != 0 || !queuedOf(resp) || opIDOf(resp) != id2 {
		t.Fatalf("with the Citadel away: %+v", resp)
	}
	// the same request again (the CLI resends it) is the same message
	if again := gate.Forward(t.Context(), c2); opIDOf(again) != id2 {
		t.Fatalf("the same request queued again: %+v", again)
	}
	c3, id3 := call("pr open")
	if opIDOf(gate.Forward(t.Context(), c3)) != id3 {
		t.Fatal("pr open was not queued")
	}
	// a command that needs the answer now is not queued
	plain := cli.NewGate(cli.GateConfig{Commands: cli.MustRegistry(cli.Spec{Name: "list", Group: cli.GroupCommunication, Summary: "s", Usage: "u", Who: cli.WhoAnyone}), Next: forwarder{b.e}})
	cl, _ := call("list")
	if resp := plain.Forward(t.Context(), cl); resp.OK || resp.Error.Code != localchan.CodeCitadelUnavailable || resp.ExitCode() != 5 {
		t.Fatalf("a command that needs the answer: %+v", resp)
	}
	if done.count(id2) != 0 {
		t.Fatal("the Citadel was away and still ran the command")
	}

	r.start(a, clock.Real{})
	r.waitFor("the Citadel to run the queued done", func() bool { return done.count(id2) >= 1 && done.count(id3) >= 1 })
	r.waitFor("both results delivered", func() bool { mu.Lock(); defer mu.Unlock(); return len(delivered) == 2 })
	// the Tower connected again and nothing else was resent
	r.waitFor("b to connect to the Citadel", func() bool { return b.connected(1) })
	r.waitFor("the outbox of b to be empty", func() bool {
		rows, err := protocolPending(t, b)
		return err == nil && rows == 0
	})
	if done.count(id2) != 1 || done.count(id3) != 1 || done.count(id1) != 1 {
		t.Fatalf("runs: done %d, pr open %d, the first done %d; want one each", done.count(id2), done.count(id3), done.count(id1))
	}
	mu.Lock()
	defer mu.Unlock()
	for _, q := range delivered {
		switch q.OpID {
		case id2:
			if !q.Response.OK || q.AgentID != "alice" || q.Cmd != "done" {
				t.Errorf("result of done: %+v", q)
			}
		case id3:
			if q.Response.OK || q.Response.Error.Code != "conflict" || q.Cmd != "pr open" || q.Response.ExitCode() != 7 {
				t.Errorf("result of pr open: %+v", q)
			}
		default:
			t.Errorf("result for an unknown op_id %q", q.OpID)
		}
	}
}

func queuedOf(r localchan.Response) bool {
	var d struct {
		Queued bool `json:"queued"`
	}
	return r.DecodeData(&d) == nil && d.Queued
}

func opIDOf(r localchan.Response) string {
	var d struct {
		OpID string `json:"op_id"`
	}
	_ = r.DecodeData(&d)
	return d.OpID
}

// protocolPending counts the messages in the outbox of the Tower.
func protocolPending(t *testing.T, n *inproc) (int, error) {
	t.Helper()
	var count int
	err := n.e.mdb.Reader().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM outbox WHERE type = 'cli.call'`).Scan(&count)
	return count, err
}
