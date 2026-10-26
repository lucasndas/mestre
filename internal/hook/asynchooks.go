package hook

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// AsyncSource gives the asynchronous hooks of the class for an event
// (ClassHookSource).
type AsyncSource interface {
	Async(ctx context.Context, ev providers.Event) (run []AsyncHook, refused []string, err error)
}

// HookExec runs an asynchronous hook of a class as the character: the same
// user and permissions, in its folder, with its environment plus
// MESTRE_HOOK_EVENT and the event on stdin. The engine is not that user, so
// the process is started by the terminal layer of the machine (the launcher
// that starts the characters, M4); the context carries the time limit.
type HookExec interface {
	Exec(ctx context.Context, agentID string, h AsyncHook, event providers.EventKind, stdin []byte) (HookResult, error)
}

// HookResult is how an asynchronous hook ended: the exit code and what it
// wrote (output and error together).
type HookResult struct {
	Exit   int
	Output string
}

// AsyncResult is the outcome of one asynchronous hook, as it goes to the
// output the hook asked for.
type AsyncResult struct {
	AgentID, TableID, TurnID string
	Event                    providers.EventKind
	Hook                     string
	Output                   string
	Exit                     int
	// Err is a hook that could not run or ran out of time.
	Err error
}

// HookOutputs receives what the asynchronous hooks wrote
// (docs/classes/classes.md#hooks-extras-da-classe): the log of the
// character, the scroll of the quest (M4) and the inbox of the character
// (M4).
type HookOutputs interface {
	Log(ctx context.Context, r AsyncResult)
	Scroll(ctx context.Context, r AsyncResult) error
	Agent(ctx context.Context, r AsyncResult) error
}

// HookGrace is how much longer than the time limit of a hook the engine waits
// for the terminal to answer.
const HookGrace = 5 * time.Second

// DefaultMaxAsync is how many events wait in the queue of a character before
// the oldest is dropped.
const DefaultMaxAsync = 1000

// AsyncRunner runs the asynchronous hooks of the classes in the engine, after
// the answer to the agent and without holding it: at most one at a time for
// each character, the others wait in a queue. Its OnEvent is the OnEvent of
// the Engine.
type AsyncRunner struct {
	Source AsyncSource
	Exec   HookExec
	Out    HookOutputs
	Clock  clock.Clock
	Logger *slog.Logger
	// MaxQueued bounds the queue of a character; zero is DefaultMaxAsync.
	MaxQueued int

	cmu sync.Mutex
	ctx context.Context
	wg  sync.WaitGroup
	mu  sync.Mutex
	q   map[string]*asyncQueue
}

type asyncQueue struct {
	events  []providers.Event
	running bool
}

// Start gives the runner the context its hooks run in; they stop with it.
func (a *AsyncRunner) Start(ctx context.Context) {
	a.cmu.Lock()
	a.ctx = ctx
	a.cmu.Unlock()
}

// context is the context given to Start, nil before it.
func (a *AsyncRunner) context() context.Context {
	a.cmu.Lock()
	defer a.cmu.Unlock()
	return a.ctx
}

// OnEvent queues the event for the hooks of the class of its character. It
// does nothing else, so it never holds the answer to the agent.
func (a *AsyncRunner) OnEvent(ev providers.Event, _ Answer) {
	if ctx := a.context(); ctx == nil || ctx.Err() != nil || ev.AgentID == "" || a.Source == nil || a.Exec == nil || !hookable(ev.Event) {
		return
	}
	limit := a.MaxQueued
	if limit <= 0 {
		limit = DefaultMaxAsync
	}
	a.mu.Lock()
	if a.q == nil {
		a.q = map[string]*asyncQueue{}
	}
	q := a.q[ev.AgentID]
	if q == nil {
		q = &asyncQueue{}
		a.q[ev.AgentID] = q
	}
	if len(q.events) >= limit {
		q.events = q.events[1:]
		a.log().Warn("hook: the queue of the asynchronous hooks is full, the oldest event was dropped", "agent", ev.AgentID)
	}
	q.events = append(q.events, ev)
	start := !q.running
	q.running = true
	a.mu.Unlock()
	if start {
		a.wg.Add(1)
		go a.work(ev.AgentID, q)
	}
}

func hookable(k providers.EventKind) bool {
	for _, e := range classes.HookEvents {
		if e == k {
			return k != providers.EventPreTool
		}
	}
	return false
}

// Wait returns when every queue is empty and no hook is running.
func (a *AsyncRunner) Wait() { a.wg.Wait() }

func (a *AsyncRunner) log() *slog.Logger {
	if a.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return a.Logger
}

func (a *AsyncRunner) work(agent string, q *asyncQueue) {
	defer a.wg.Done()
	for {
		a.mu.Lock()
		if len(q.events) == 0 || a.context().Err() != nil {
			q.running = false
			q.events = nil
			a.mu.Unlock()
			return
		}
		ev := q.events[0]
		q.events = q.events[1:]
		a.mu.Unlock()
		a.handle(ev)
	}
}

func (a *AsyncRunner) handle(ev providers.Event) {
	hooks, refused, err := a.Source.Async(a.context(), ev)
	if err != nil {
		a.log().Warn("hook: the asynchronous hooks of the class could not be read", "agent", ev.AgentID, "err", err)
		return
	}
	for _, why := range refused {
		a.log().Warn("hook: an asynchronous hook of the class was refused", "agent", ev.AgentID, "event", ev.Event, "why", why)
	}
	if len(hooks) == 0 {
		return
	}
	stdin, err := json.Marshal(ev)
	if err != nil {
		a.log().Warn("hook: the event does not encode", "err", err)
		return
	}
	for _, h := range hooks {
		// the terminal ends a hook at its time limit and says so; the engine waits a
		// little longer, so the next hook of the queue does not meet the one that is
		// still being ended
		hctx, cancel := clock.WithTimeout(a.context(), a.Clock, time.Duration(h.Timeout)*time.Second+HookGrace)
		res, err := a.Exec.Exec(hctx, ev.AgentID, h, ev.Event, stdin)
		if err == nil && hctx.Err() != nil {
			err = hctx.Err()
		}
		cancel()
		r := AsyncResult{AgentID: ev.AgentID, TableID: ev.TableID, TurnID: ev.TurnID, Event: ev.Event, Hook: h.Name, Output: res.Output, Exit: res.Exit, Err: err}
		a.deliver(h, r)
	}
}

// deliver sends the result where the hook asked: a failure or a time-out is
// always logged and the hook after it still runs.
func (a *AsyncRunner) deliver(h AsyncHook, r AsyncResult) {
	if a.Out == nil {
		return
	}
	if r.Err != nil || r.Exit != 0 || h.Output == classes.OutputLog {
		a.Out.Log(a.context(), r)
		if r.Err != nil || h.Output == classes.OutputLog {
			return
		}
	}
	var err error
	switch h.Output {
	case classes.OutputScroll:
		err = a.Out.Scroll(a.context(), r)
	case classes.OutputAgent:
		err = a.Out.Agent(a.context(), r)
	}
	if err != nil {
		a.log().Warn("hook: the output of an asynchronous hook was not delivered", "agent", r.AgentID, "hook", r.Hook, "output", h.Output, "err", err)
	}
}
