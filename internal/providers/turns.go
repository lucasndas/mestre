package providers

import "strconv"

// Turns follows the session and the turn of one character's terminal, on
// the engine side, and completes the normalized events with what their
// payload does not carry (docs/provedores/adaptador.md#formato-interno-normalizado-de-evento):
//
//   - turn_id: the turn in progress, for the events without one (Claude's
//     statusLine, Notification, PreCompact, SessionEnd; every Antigravity
//     event but Stop). An Antigravity turn_start opens the turn after the
//     last Stop's executionNum.
//   - session_start, for the providers without the event (Antigravity):
//     the first turn_start of a session this terminal sees comes after a
//     derived session_start.
//
// The engine keeps one Turns per running terminal; it is not safe for
// concurrent use.
type Turns struct {
	derive   bool
	session  string
	turn     string
	open     bool
	lastExec int
}

// NewTurns returns the tracker of a terminal of the provider.
func NewTurns(info Info) *Turns { return &Turns{derive: info.DerivedSessionStart} }

// ResumeAfter sets the last turn of a resumed session, so the numbering of
// an Antigravity conversation goes on from it.
func (t *Turns) ResumeAfter(lastTurn string) {
	t.turn = lastTurn
	if n, err := strconv.Atoi(lastTurn); err == nil {
		t.lastExec = n
	}
}

// Apply completes ev and returns the events it stands for, in order.
func (t *Turns) Apply(ev Event) []Event {
	var out []Event
	if ev.SessionID != "" && ev.SessionID != t.session {
		t.session, t.open = ev.SessionID, false
		if t.derive && ev.Event == EventTurnStart {
			start := ev
			start.Event, start.TurnID, start.Prompt = EventSessionStart, "", ""
			out = append(out, start)
		}
	}
	switch ev.Event {
	case EventNone:
	case EventSessionStart:
		t.turn, t.open = "", false
	case EventTurnStart:
		switch {
		case ev.TurnID != "":
			t.turn = ev.TurnID
		case !t.open: // a second invocation of an open turn keeps its id
			t.turn = strconv.Itoa(t.lastExec + 1)
		}
		ev.TurnID, t.open = t.turn, true
	case EventTurnEnd, EventTurnFailure, EventInterrupt:
		if ev.TurnID == "" {
			ev.TurnID = t.turn
		}
		t.turn, t.open = ev.TurnID, false
		if n, err := strconv.Atoi(ev.TurnID); err == nil && n > t.lastExec {
			t.lastExec = n
		}
	default:
		if ev.TurnID == "" {
			ev.TurnID = t.turn
		} else if ev.Event.ToolEvent() {
			t.turn = ev.TurnID
		}
	}
	return append(out, ev)
}
