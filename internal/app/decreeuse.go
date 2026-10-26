package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/decrees"
	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// DecreeApplier registers the use of a decree (decrees.Service.Applied).
type DecreeApplier interface {
	Applied(ctx context.Context, c characters.Character, number int64, use decrees.AppliedUse) error
}

// decreeUseTimeout bounds the write of a use.
const decreeUseTimeout = 5 * time.Second

// decreeQueueSize bounds the uses waiting to be written: the hook answers
// before the write, and a use that finds the queue full is dropped with a
// warning (the list of what a decree executed is a record, never a gate).
const decreeQueueSize = 256

// maxEventAction bounds the action put in the list of a decree.
const maxEventAction = 500

// decreeUses records, in the decree, that the hook let a call through on its
// strength (docs/telegram/decretos.md#registro). It looks the character up in
// the registry of the Citadel and gives the decree to Applied, which refuses a
// decree that does not reach the character now or does not free this call, so
// the number an event carries is never trusted alone.
type decreeUses struct {
	reg     func(ctx context.Context, id string) (characters.Character, error)
	applier DecreeApplier
	log     func(msg string, args ...any)
	clk     clock.Clock // nil: the real one
}

// onEvent registers the use of the decree an allow carries, once per event.
func (u decreeUses) onEvent(ev providers.Event, ans hook.Answer) {
	if !usesDecree(u.applier != nil, ev, ans) {
		return
	}
	clk := u.clk
	if clk == nil {
		clk = clock.Real{}
	}
	ctx, cancel := clock.WithTimeout(context.Background(), clk, decreeUseTimeout)
	defer cancel()
	c, err := u.reg(ctx, ev.AgentID)
	if err == nil {
		// the instant of the event, not of its delivery: an event waits in the
		// outbox of a Tower while the decree may end
		err = u.applier.Applied(ctx, c, ans.Decree, decrees.AppliedUse{Tool: ev.Tool, Action: boundAction(actionOf(ev)), At: ev.ReceivedAt})
	}
	if err != nil && u.log != nil {
		u.log("app: the use of a decree was not registered", "decree", ans.Decree, "agent", ev.AgentID, "ended", errors.Is(err, decrees.ErrEnded), "err", err)
	}
}

func usesDecree(have bool, ev providers.Event, ans hook.Answer) bool {
	return have && ans.Decree > 0 && ans.Verdict == hook.VerdictAllow && ev.AgentID != "" && (ev.Event == providers.EventPreTool || ev.Event == providers.EventPermissionRequest)
}

func boundAction(s string) string {
	if r := []rune(s); len(r) > maxEventAction {
		return string(r[:maxEventAction]) + "…"
	}
	return s
}

type decreeJob struct {
	ev  providers.Event
	ans hook.Answer
}

// decreeQueue is the registration of the uses off the path of the hook: a
// bounded queue and a worker tied to the period (the Loop of the orchestration),
// so the durable write is not in the answer of the hook.
type decreeQueue struct {
	uses    decreeUses
	jobs    chan decreeJob
	pending sync.WaitGroup
}

func newDecreeQueue(u decreeUses) *decreeQueue {
	return &decreeQueue{uses: u, jobs: make(chan decreeJob, decreeQueueSize)}
}

// Enqueue is what the hooks tell of an answered event; it never waits.
func (q *decreeQueue) Enqueue(ev providers.Event, ans hook.Answer) {
	if !usesDecree(q.uses.applier != nil, ev, ans) {
		return
	}
	q.pending.Add(1)
	select {
	case q.jobs <- decreeJob{ev, ans}:
	default:
		q.pending.Done()
		if q.uses.log != nil {
			q.uses.log("app: the queue of the uses of the decrees is full; a use was not registered", "decree", ans.Decree, "agent", ev.AgentID)
		}
	}
}

// Loop is the worker: it runs while the period does.
func (q *decreeQueue) Loop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case j := <-q.jobs:
			q.uses.onEvent(j.ev, j.ans)
			q.pending.Done()
		}
	}
}

// Wait returns when what was enqueued has been written (the tests).
func (q *decreeQueue) Wait() { q.pending.Wait() }

// actionOf says what the call did, for the list of what a decree executed.
func actionOf(ev providers.Event) string {
	if ev.Tool == nil {
		return string(ev.Event)
	}
	switch {
	case ev.Tool.Command != "":
		return ev.Tool.Command
	case len(ev.Tool.Paths) > 0:
		return fmt.Sprintf("%s %s", ev.Tool.NativeName, strings.Join(ev.Tool.Paths, " "))
	case ev.Tool.URL != "":
		return fmt.Sprintf("%s %s", ev.Tool.NativeName, ev.Tool.URL)
	}
	return ev.Tool.NativeName
}

// hostDecreeUses is the use of the decrees in the hook of a period: the queue of
// what is told of each event the hooks answered, when the people of the chain
// keep decrees (nil otherwise: no decree has been made).
func hostDecreeUses(integ IntegrationRun, orc *Orchestration, clk clock.Clock, log func(msg string, args ...any)) *decreeQueue {
	people, ok := integ.(chainPeople)
	if !ok {
		return nil
	}
	ap, ok := people.CharacterDecrees().(DecreeApplier)
	if !ok {
		return nil
	}
	return newDecreeQueue(decreeUses{reg: orc.Registry.Get, applier: ap, clk: clk, log: log})
}

// periodLedger is the ledger of the exceptions of the hook on the Citadel: the
// ledger of the machine decides whether this character spent the exception
// (it is read by the hook in its own time), and the decree itself is the single
// authority of "once": a second character the same exception reaches, in the
// window before the rules are published again, is told it is already used.
type periodLedger struct {
	e     *Engine
	local classes.ExceptionLedger
}

func (l periodLedger) Use(ctx context.Context, agentID string, id int64) (bool, error) {
	ok, err := l.local.Use(ctx, agentID, id)
	if err != nil || !ok {
		return ok, err
	}
	if p := l.e.period.Load(); p != nil && p.exceptions != nil {
		return p.exceptions.Use(ctx, agentID, id)
	}
	return true, nil
}

// decreeTargets is the decrees.Targets: the live target of the request an
// exception bound to a PR was made for. The repository of the target is the one
// of the workspace of the decree, never another (an exception made in a
// workspace spends nothing in the repository of another), and the live target is
// given in the spelling of the repository of the decree, which GitHub folds.
type decreeTargets struct {
	live  lateTargets
	repos func(ctx context.Context, workspace string) (string, error)
}

func (t decreeTargets) Current(ctx context.Context, r chain.Request) (json.RawMessage, error) {
	if r.Type != classes.RequestPR && r.Type != classes.RequestMerge {
		return nil, fmt.Errorf("app: no exception is bound to the target of a request of type %q", r.Type)
	}
	want, err := t.repos(ctx, r.Workspace)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", chain.ErrWrongRepo, err)
	}
	if !chain.SameRepo(want, chain.TargetRepo(r)) {
		return nil, fmt.Errorf("%w: %q is not %q", chain.ErrWrongRepo, chain.TargetRepo(r), want)
	}
	cur, err := t.live.Current(ctx, r)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if json.Unmarshal(cur, &m) != nil {
		return cur, nil
	}
	m["repo"] = chain.TargetRepo(r)
	return json.Marshal(m)
}
