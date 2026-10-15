package characters

import (
	"slices"
	"strings"

	"github.com/lucasndas/mestre/internal/providers"
)

// State is the state of a character or of a simple terminal
// (docs/orquestracao/personagens.md#estados).
type State string

// The states.
const (
	Starting    State = "starting"
	Idle        State = "idle"
	Busy        State = "busy"
	Waiting     State = "waiting"
	Blocked     State = "blocked"
	Stuck       State = "stuck"
	Stopping    State = "stopping"
	Stopped     State = "stopped"
	Interrupted State = "interrupted"
)

// States lists them in the order of the document.
func States() []State {
	return []State{Starting, Idle, Busy, Waiting, Blocked, Stuck, Stopping, Stopped, Interrupted}
}

// Valid says whether s is a state of the document.
func (s State) Valid() bool { return slices.Contains(States(), s) }

// Alive says whether a character in s still counts: it has a place in the
// limits and a name in its table. Interrupted counts: it will be resumed.
func (s State) Alive() bool { return s.Valid() && s != Stopped }

// Receives says whether a delivery of the inbox may be typed in s.
func (s State) Receives() bool { return s == Idle || s == Waiting }

// CountsAsIdle says whether the time in s is idle time.
func (s State) CountsAsIdle() bool { return s == Idle }

// ReasonDeliveryFailed is the reason of the return to idle of a character
// whose delivery could not be typed.
const ReasonDeliveryFailed = "delivery failed"

// The reasons of blocked.
const (
	ReasonPermission = "permission"
	ReasonQuota      = "quota"
	ReasonInput      = "input"
)

// Trigger is the label of an edge of the diagram of states.
type Trigger string

// The edges of the diagram, named by what the document writes on them.
const (
	TrigReady          Trigger = "ready"            // starting -> idle
	TrigStartFailed    Trigger = "start_failed"     // starting -> interrupted (failed or 120 s)
	TrigStartInput     Trigger = "start_input"      // starting -> blocked (a screen that asks for input)
	TrigDeliver        Trigger = "deliver"          // idle|waiting -> busy (a delivery or the start of a turn)
	TrigTurnEnd        Trigger = "turn_end"         // busy -> idle
	TrigTurnEndWaiting Trigger = "turn_end_waiting" // busy -> waiting
	TrigWaitResolved   Trigger = "wait_resolved"    // waiting -> idle
	TrigBlock          Trigger = "block"            // busy -> blocked
	TrigUnblockBusy    Trigger = "unblock_busy"     // blocked -> busy
	TrigUnblockIdle    Trigger = "unblock_idle"     // blocked -> idle
	TrigNoSignal       Trigger = "no_signal"        // busy -> stuck
	TrigSignal         Trigger = "signal"           // stuck -> busy
	TrigStop           Trigger = "stop"             // idle -> stopping (the safe end)
	TrigForceStop      Trigger = "force_stop"       // stuck -> stopping
	TrigResume         Trigger = "resume"           // interrupted -> starting
	TrigDiscard        Trigger = "discard"          // interrupted -> stopping
	TrigStopped        Trigger = "stopped"          // stopping -> stopped
	TrigDied           Trigger = "died"             // any live state -> interrupted (the process died unasked)
)

// edges is the diagram of the document, edge by edge. Nothing else is a
// transition.
var edges = map[State]map[Trigger]State{
	Starting: {TrigReady: Idle, TrigStartFailed: Interrupted, TrigStartInput: Blocked, TrigDied: Interrupted},
	Idle:     {TrigDeliver: Busy, TrigStop: Stopping, TrigDied: Interrupted},
	Waiting:  {TrigDeliver: Busy, TrigWaitResolved: Idle, TrigDied: Interrupted},
	Busy: {
		TrigTurnEnd: Idle, TrigTurnEndWaiting: Waiting, TrigBlock: Blocked, TrigNoSignal: Stuck, TrigDied: Interrupted,
	},
	Blocked:     {TrigUnblockBusy: Busy, TrigUnblockIdle: Idle, TrigDied: Interrupted},
	Stuck:       {TrigSignal: Busy, TrigForceStop: Stopping, TrigDied: Interrupted},
	Stopping:    {TrigStopped: Stopped},
	Interrupted: {TrigResume: Starting, TrigDiscard: Stopping},
}

// Next is the state a trigger leads to from a state, and whether the diagram
// has that edge.
func Next(from State, t Trigger) (State, bool) {
	to, ok := edges[from][t]
	return to, ok
}

// Edge is one edge of the diagram.
type Edge struct {
	From    State
	Trigger Trigger
	To      State
}

// Edges lists the edges of the diagram, for the tests.
func Edges() []Edge {
	var out []Edge
	for _, from := range States() {
		for t, to := range edges[from] {
			out = append(out, Edge{from, t, to})
		}
	}
	slices.SortFunc(out, func(a, b Edge) int {
		if a.From != b.From {
			return slices.Index(States(), a.From) - slices.Index(States(), b.From)
		}
		return slices.Compare([]byte(a.Trigger), []byte(b.Trigger))
	})
	return out
}

// Status is the state of a character with its reason.
type Status struct {
	State State
	// Reason is the one of waiting and blocked ("permission", "quota",
	// "input"; "approval #3", "character Revisor"…), or the event that
	// caused the last change.
	Reason string
}

// Label is how the canvas and `mestre list` say the status: "aguardando
// <motivo>" for waiting and blocked
// (docs/orquestracao/esperas-e-caixa-de-entrada.md#status-aguardando). An
// approval wait is "aguardando aprovação"; only the native prompt of the
// provider, blocked by permission, is "aguardando permissão".
func (s Status) Label() string {
	switch {
	case s.State == Blocked && s.Reason == ReasonPermission:
		return "aguardando permissão"
	case s.State == Blocked && s.Reason == ReasonQuota:
		return "aguardando cota"
	case s.State == Blocked:
		return "aguardando entrada"
	case s.State == Waiting && strings.HasPrefix(s.Reason, "approval"):
		// "approval #3 · do Mestre": the chain says which level has the request.
		if _, level, ok := strings.Cut(s.Reason, " · "); ok && level != "" {
			return "aguardando aprovação " + level
		}
		return "aguardando aprovação"
	case s.State == Waiting && s.Reason != "":
		return "aguardando " + s.Reason
	}
	return string(s.State)
}

// Step is a transition to apply: the trigger and the reason to record.
type Step struct {
	Trigger Trigger
	Reason  string
}

// HookSteps is what an event of the hook engine does to the state of a
// character, as the steps to apply in order. waitReason, when not empty, is
// the active wait of the character: a turn that ends then goes to waiting
// with it. Any event is a signal, so a stuck character first goes back to
// busy; the event is then read in busy. An event the diagram has no edge for
// does nothing but signal.
func HookSteps(st Status, ev providers.Event, waitReason string) []Step {
	var steps []Step
	cur := st
	apply := func(t Trigger, reason string) {
		to, ok := Next(cur.State, t)
		if !ok {
			return
		}
		steps = append(steps, Step{t, reason})
		cur = Status{to, reason}
	}
	if cur.State == Stuck {
		apply(TrigSignal, "signal: "+string(ev.Event))
	}
	end := func(reason string) {
		switch {
		case cur.State == Busy && waitReason != "":
			apply(TrigTurnEndWaiting, waitReason)
		case cur.State == Busy:
			apply(TrigTurnEnd, reason)
		case cur.State == Blocked:
			apply(TrigUnblockIdle, reason)
		}
	}
	switch ev.Event {
	case providers.EventSessionStart:
		switch cur.State {
		case Starting:
			apply(TrigReady, "session_start")
		case Blocked:
			if cur.Reason == ReasonInput { // the screen that asked for input was answered
				apply(TrigUnblockIdle, "session_start")
			}
		}
	case providers.EventTurnStart:
		switch cur.State {
		case Idle, Waiting:
			apply(TrigDeliver, "turn_start")
		case Blocked:
			apply(TrigUnblockBusy, "turn_start")
		}
	case providers.EventPreTool, providers.EventPostTool:
		if cur.State == Blocked {
			apply(TrigUnblockBusy, string(ev.Event))
		}
	case providers.EventPermissionRequest:
		if cur.State == Busy {
			apply(TrigBlock, ReasonPermission)
		}
	case providers.EventTurnEnd, providers.EventInterrupt:
		end(string(ev.Event))
	case providers.EventTurnFailure:
		if ev.StopReason == providers.StopRateLimit && cur.State == Busy {
			apply(TrigBlock, ReasonQuota)
			break
		}
		end(string(ev.Event))
	}
	return steps
}
