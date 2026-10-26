package conformance

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// Call is one hook call the engine side received.
type Call struct {
	Seq int
	// Event is the event as the dispatcher sent it (with the raw payload
	// when the character records its hooks).
	Event providers.Event
	// Caller is who the local channel identified.
	Caller localchan.Caller
	// Answered is false for a call the engine did not answer (paused).
	Answered bool
	Answer   hook.Answer
	// Degraded is what the dispatcher decides for a call the engine did
	// not answer (paused): the fixed rules and the snapshot of the
	// character, as `mestre hook` computes them without the engine.
	Degraded classes.Verdict
	// Arrived is when the call came in; Gone when the dispatcher hung up
	// (only for a call that was not answered).
	Arrived, Gone time.Time
}

// Denied reports whether the engine denied the call.
func (c Call) Denied() bool { return c.Answered && c.Answer.Verdict == hook.VerdictDeny }

// Log is the list of the calls, in order, with a wait for the next ones.
type Log struct {
	mu      sync.Mutex
	calls   []Call
	audits  []localchan.Audit
	changed chan struct{}
}

func newLog() *Log { return &Log{changed: make(chan struct{})} }

func (l *Log) add(c Call) {
	l.mu.Lock()
	c.Seq = len(l.calls) + 1
	l.calls = append(l.calls, c)
	close(l.changed)
	l.changed = make(chan struct{})
	l.mu.Unlock()
}

// Len is the number of calls so far: the start of the calls of what comes
// next.
func (l *Log) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.calls)
}

// Since are the calls after the first n of a character ("" is any).
func (l *Log) Since(n int, agentID string) []Call {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Call
	for _, c := range l.calls[min(n, len(l.calls)):] {
		if agentID == "" || c.Event.AgentID == agentID {
			out = append(out, c)
		}
	}
	return out
}

// Audits are the requests of the local channel other than hook (the
// `mestre debug` a character runs).
func (l *Log) Audits() []localchan.Audit {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]localchan.Audit(nil), l.audits...)
}

// Wait waits for a call after the first n of the character that ok accepts.
func (l *Log) Wait(ctx context.Context, n int, agentID string, ok func(Call) bool) (Call, bool) {
	for {
		l.mu.Lock()
		calls, ch := l.calls[min(n, len(l.calls)):], l.changed
		for _, c := range calls {
			if (agentID == "" || c.Event.AgentID == agentID) && ok(c) {
				l.mu.Unlock()
				return c, true
			}
		}
		n = len(l.calls)
		l.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return Call{}, false
		}
	}
}

// changes signals the next call.
func (l *Log) changes() <-chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.changed
}

// tap is the hook handler of the local channel: it hands each call to the
// hook engine and records it, or, for a paused character, holds it without
// an answer until the dispatcher hangs up.
type tap struct {
	clk    clock.Clock
	engine *hook.Engine
	log    *Log
	// degraded decides a paused call as the dispatcher does; nil: unknown.
	degraded func(context.Context, providers.Event) classes.Decision

	mu     sync.Mutex
	paused map[string]bool
}

// pauseHold bounds how long a paused call is held after the dispatcher gave
// up (its deadline is under 1 s).
const pauseHold = 5 * time.Second

func newTap(clk clock.Clock, e *hook.Engine) *tap {
	return &tap{clk: clk, engine: e, log: newLog(), paused: map[string]bool{}}
}

func (t *tap) pause(agentID string, on bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if on {
		t.paused[agentID] = true
	} else {
		delete(t.paused, agentID)
	}
}

func (t *tap) isPaused(agentID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.paused[agentID]
}

// Handle implements localchan.Handler.
func (t *tap) Handle(ctx context.Context, c *localchan.Call) localchan.Response {
	arrived := t.clk.Now()
	var ev providers.Event
	if len(c.Request.Args) == 1 {
		_ = json.Unmarshal(c.Request.Args[0], &ev)
	}
	call := Call{Event: ev, Caller: c.Caller, Arrived: arrived}
	if t.isPaused(ev.AgentID) {
		hold := t.clk.NewTimer(pauseHold)
		defer hold.Stop()
		select {
		case <-c.Gone:
		case <-hold.C():
		case <-ctx.Done():
		}
		call.Gone = t.clk.Now()
		if t.degraded != nil {
			call.Degraded = t.degraded(ctx, ev).Verdict
		}
		t.log.add(call)
		return localchan.Fail(localchan.CodeTimeout, "the engine of the conformance test stopped answering this character", "")
	}
	resp := t.engine.Handle(ctx, c)
	call.Answered = true
	if resp.OK {
		_ = resp.DecodeData(&call.Answer)
	} else {
		call.Answer = hook.Answer{Verdict: hook.VerdictDeny}
		if resp.Error != nil {
			call.Answer.Reason = resp.Error.Code + ": " + resp.Error.Message
		}
	}
	t.log.add(call)
	return resp
}

func (t *tap) audit(a localchan.Audit) {
	if a.Cmd == localchan.CmdHook {
		return
	}
	t.log.mu.Lock()
	t.log.audits = append(t.log.audits, a)
	t.log.mu.Unlock()
}
