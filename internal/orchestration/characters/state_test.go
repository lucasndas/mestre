package characters_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/providers"
)

type pair struct{ from, to characters.State }

// diagram is the stateDiagram-v2 of docs/orquestracao/personagens.md#estados,
// edge by edge, written here from the document and not from the code.
var diagram = map[pair]bool{
	{characters.Starting, characters.Idle}:        true,
	{characters.Starting, characters.Interrupted}: true,
	{characters.Starting, characters.Blocked}:     true,
	{characters.Idle, characters.Busy}:            true,
	{characters.Waiting, characters.Busy}:         true,
	{characters.Busy, characters.Idle}:            true,
	{characters.Busy, characters.Waiting}:         true,
	{characters.Waiting, characters.Idle}:         true,
	{characters.Busy, characters.Blocked}:         true,
	{characters.Blocked, characters.Busy}:         true,
	{characters.Blocked, characters.Idle}:         true,
	{characters.Busy, characters.Stuck}:           true,
	{characters.Stuck, characters.Busy}:           true,
	{characters.Idle, characters.Stopping}:        true,
	{characters.Stuck, characters.Stopping}:       true,
	{characters.Interrupted, characters.Starting}: true,
	{characters.Interrupted, characters.Stopping}: true,
	{characters.Stopping, characters.Stopped}:     true,
	{characters.Idle, characters.Interrupted}:     true, // any live state, if the process dies unasked
	{characters.Busy, characters.Interrupted}:     true,
	{characters.Waiting, characters.Interrupted}:  true,
	{characters.Blocked, characters.Interrupted}:  true,
	{characters.Stuck, characters.Interrupted}:    true,
}

func TestTheEdgesOfTheCodeAreTheEdgesOfTheDiagram(t *testing.T) {
	t.Parallel()
	got := map[pair]bool{}
	for _, e := range characters.Edges() {
		got[pair{e.From, e.To}] = true
		if next, ok := characters.Next(e.From, e.Trigger); !ok || next != e.To {
			t.Errorf("Next(%s, %s) = %s, %v; want %s", e.From, e.Trigger, next, ok, e.To)
		}
	}
	for p := range diagram {
		if !got[p] {
			t.Errorf("the diagram has %s -> %s and the code does not", p.from, p.to)
		}
	}
	for p := range got {
		if !diagram[p] {
			t.Errorf("the code has %s -> %s and the diagram does not", p.from, p.to)
		}
	}
}

// reach drives a new character to a state and returns it.
func reach(t *testing.T, r *rig, state characters.State, name string) characters.Character {
	t.Helper()
	c := r.make("Dev Claude", name, "3-login")
	switch state {
	case characters.Starting:
	case characters.Idle:
		r.ready(c)
	case characters.Busy:
		r.ready(c)
		r.busy(c)
	case characters.Waiting:
		r.ready(c)
		r.busy(c)
		r.waits.set(c.ID, "Revisor")
		r.hook(c, providers.EventTurnEnd)
		r.waits.set(c.ID, "")
	case characters.Blocked:
		r.ready(c)
		r.busy(c)
		r.hook(c, providers.EventPermissionRequest)
	case characters.Stuck:
		r.ready(c)
		r.busy(c)
		r.clk.Advance(15 * time.Minute)
	case characters.Interrupted:
		r.ready(c)
		r.machineOf(c).host.emit(characters.HostEvent{Terminal: c.ID, Kind: characters.HostExit, ExitCode: 1})
	default:
		t.Fatalf("cannot reach %s", state)
	}
	if got := r.state(c); got != state {
		t.Fatalf("%s is %s, want %s", name, got, state)
	}
	return c
}

// CA-PERS-06
func TestOnlyTheTransitionsOfTheDiagramHappenAndFifteenMinutesWithoutSignalMakeStuck(t *testing.T) {
	t.Parallel()
	kinds := []providers.EventKind{providers.EventSessionStart, providers.EventTurnStart, providers.EventPreTool, providers.EventPostTool,
		providers.EventPermissionRequest, providers.EventTurnEnd, providers.EventTurnFailure, providers.EventInterrupt,
		providers.EventPreCompact, providers.EventSessionEnd, providers.EventNotification, providers.EventUsage}
	// What each event does in each state; an event that is not listed does
	// nothing but signal.
	expect := map[characters.State]map[providers.EventKind]characters.State{
		characters.Starting: {providers.EventSessionStart: characters.Idle},
		characters.Idle:     {providers.EventTurnStart: characters.Busy},
		characters.Busy: {providers.EventPermissionRequest: characters.Blocked, providers.EventTurnEnd: characters.Idle,
			providers.EventTurnFailure: characters.Idle, providers.EventInterrupt: characters.Idle},
		characters.Waiting: {providers.EventTurnStart: characters.Busy},
		characters.Blocked: {providers.EventTurnStart: characters.Busy, providers.EventPreTool: characters.Busy, providers.EventPostTool: characters.Busy,
			providers.EventTurnEnd: characters.Idle, providers.EventTurnFailure: characters.Idle, providers.EventInterrupt: characters.Idle},
		// Any event is a signal: stuck first goes back to busy, and the event is read there.
		characters.Stuck: {providers.EventSessionStart: characters.Busy, providers.EventTurnStart: characters.Busy, providers.EventPreTool: characters.Busy,
			providers.EventPostTool: characters.Busy, providers.EventPermissionRequest: characters.Blocked, providers.EventTurnEnd: characters.Idle,
			providers.EventTurnFailure: characters.Idle, providers.EventInterrupt: characters.Idle, providers.EventPreCompact: characters.Busy,
			providers.EventSessionEnd: characters.Busy, providers.EventNotification: characters.Busy, providers.EventUsage: characters.Busy},
		characters.Interrupted: {},
	}
	for _, from := range []characters.State{characters.Starting, characters.Idle, characters.Busy, characters.Waiting, characters.Blocked, characters.Stuck, characters.Interrupted} {
		t.Run(string(from), func(t *testing.T) {
			t.Parallel()
			r := newRig(t, rigOptions{limits: characters.Limits{PerMachine: 1000, PerTable: 1000, PerWorkspace: 1000}})
			for i, kind := range kinds {
				c := reach(t, r, from, fmt.Sprintf("C%d", i))
				r.hook(c, kind)
				want, ok := expect[from][kind]
				if !ok {
					want = from
				}
				if got := r.state(c); got != want {
					t.Errorf("%s + %s = %s, want %s", from, kind, got, want)
				}
				for _, l := range mustLog(t, r, c) {
					if l.From != "" && !diagram[pair{l.From, l.To}] {
						t.Errorf("%s + %s logged %s -> %s, which the diagram does not have", from, kind, l.From, l.To)
					}
				}
			}
		})
	}
	r := newRig(t, rigOptions{})
	// A turn that fails on the rate limit blocks for the quota.
	c := reach(t, r, characters.Busy, "Quota")
	ev := event(c, providers.EventTurnFailure)
	ev.StopReason = providers.StopRateLimit
	r.machineOf(c).rt.OnHook(ev, hook.Answer{})
	if got := r.get(c.ID); got.State != characters.Blocked || got.Reason != characters.ReasonQuota {
		t.Errorf("rate limit: %s (%s), want blocked (quota)", got.State, got.Reason)
	}

	// Fifteen minutes without a signal in busy make stuck, a signal in the
	// meantime restarts the count, and the Mestre da mesa is told.
	c = reach(t, r, characters.Busy, "Silencioso")
	r.clk.Advance(14 * time.Minute)
	if got := r.state(c); got != characters.Busy {
		t.Fatalf("after 14 minutes: %s, want busy", got)
	}
	r.hook(c, providers.EventPostTool)
	r.clk.Advance(14 * time.Minute)
	if got := r.state(c); got != characters.Busy {
		t.Fatalf("14 minutes after a signal: %s, want busy", got)
	}
	r.clk.Advance(time.Minute)
	if got := r.state(c); got != characters.Stuck {
		t.Fatalf("15 minutes after the last signal: %s, want stuck", got)
	}
	if ns := r.notes.of(characters.NoticeStuck); len(ns) == 0 || ns[len(ns)-1].Character != c.ID || ns[len(ns)-1].To.Kind != characters.ToTableMaster {
		t.Errorf("the table master was not told that %s is stuck: %+v", c.Name, ns)
	}
	r.hook(c, providers.EventPostTool)
	if got := r.state(c); got != characters.Busy {
		t.Errorf("a signal of a stuck character: %s, want busy", got)
	}
}

func mustLog(t *testing.T, r *rig, c characters.Character) []characters.LogLine {
	t.Helper()
	l, err := r.reg.Log(r.ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// CA-PERS-10
func TestEveryTransitionIsInTheHistoryWithItsReasonAndItsTimeAndTheCleaningDeletesTheOldOnes(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	c := r.make("Dev Claude", "Dev", "3-login") // starting
	step := func(d time.Duration) { r.clk.Advance(d) }
	step(time.Second)
	r.hook(c, providers.EventSessionStart) // idle
	step(time.Minute)
	r.hook(c, providers.EventTurnStart) // busy
	step(2 * time.Minute)
	r.waits.set(c.ID, "character Revisor")
	r.hook(c, providers.EventTurnEnd) // waiting, for another character
	r.waits.set(c.ID, "")
	step(3 * time.Minute)
	if err := r.machineOf(c).rt.WaitResolved(r.ctx, c.ID); err != nil { // idle
		t.Fatal(err)
	}
	log := mustLog(t, r, c)
	type line struct {
		from, to characters.State
		reason   string
		at       time.Duration
	}
	want := []line{
		{"", characters.Starting, "created", 0},
		{characters.Starting, characters.Idle, "session_start", time.Second},
		{characters.Idle, characters.Busy, "turn_start", time.Second + time.Minute},
		{characters.Busy, characters.Waiting, "character Revisor", time.Second + 3*time.Minute},
		{characters.Waiting, characters.Idle, "wait resolved", time.Second + 6*time.Minute},
	}
	if len(log) != len(want) {
		t.Fatalf("the history has %d lines, want %d: %+v", len(log), len(want), log)
	}
	for i, w := range want {
		g := log[i]
		if g.From != w.from || g.To != w.to || g.Reason != w.reason || !g.At.Equal(day0.Add(w.at)) {
			t.Errorf("line %d = %s -> %s (%s) at %s; want %s -> %s (%s) at %s", i, g.From, g.To, g.Reason, g.At.Sub(day0), w.from, w.to, w.reason, w.at)
		}
	}
	// 91 days later the daily cleaning deletes what is older than 90 days.
	r.advance(89 * 24 * time.Hour)
	if got := mustLog(t, r, c); len(got) != len(want) {
		t.Fatalf("after 89 days the history has %d lines, want %d", len(got), len(want))
	}
	r.advance(2 * 24 * time.Hour)
	if got := mustLog(t, r, c); len(got) != 0 {
		t.Errorf("after 91 days the history still has %d lines: %+v", len(got), got)
	}
}
