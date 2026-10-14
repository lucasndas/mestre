package providers

import (
	"regexp"
	"strings"
)

// State is the readiness the screen shows.
type State string

// States of Screens. Starting is neither ready nor busy.
const (
	StateStarting State = "starting"
	StateReady    State = "ready"
	StateBusy     State = "busy"
)

// Action is what the engine does about a known screen
// (docs/provedores/adaptador.md#prontidão-e-telas-interativas).
type Action string

// Actions.
const (
	// ActionAvoid is a screen the engine pre-configures away; seeing it
	// means the pre-configuration failed, and it escalates.
	ActionAvoid Action = "avoid"
	// ActionAnswer types the answer of a decision the engine already
	// took; without a decision the screen falls back to Default.
	ActionAnswer Action = "answer"
	// ActionEscalate blocks the character (reason input) and tells the
	// Mestre da mesa.
	ActionEscalate Action = "escalate"
	// ActionAlert is a login: the character is blocked and the checklist
	// gets an alert.
	ActionAlert Action = "alert"
)

// KnownScreen is one screen of an adapter's list.
type KnownScreen struct {
	ID      string
	Pattern *regexp.Regexp
	Action  Action
	// Answers are the bytes of each choice of an ActionAnswer screen.
	Answers map[string][]byte
	// Default is the choice without a recorded decision ("deny" on a
	// native permission request); "" escalates instead.
	Default string
	// Checklist is the item of an ActionAlert or ActionAvoid screen.
	Checklist string
}

// ScreenInput is the current screen, from the terminal's emulator.
type ScreenInput struct {
	Text string
	// Stable is true when the screen did not change for the stable time
	// (docs/arquitetura/terminal.md#emulador-de-terminal).
	Stable bool
	// SessionStarted is true once session_start arrived.
	SessionStarted bool
	// ReadyPattern is the base's `ready: pattern`, when it has one; it
	// replaces the adapter's input prompt.
	ReadyPattern *regexp.Regexp
}

// ScreenResult is what Screens recognized.
type ScreenResult struct {
	State State
	// Known is the known screen on display, or nil.
	Known *KnownScreen
}

// screenSet is an adapter's screens: the input prompt, the busy sign and
// the known screens.
type screenSet struct {
	prompt *regexp.Regexp
	busy   *regexp.Regexp
	known  []KnownScreen
}

// evaluate: a known screen first, then ready, then busy. "Ready" is the
// input prompt visible on the last line and the screen stable, or
// session_start received and the prompt visible. The patterns are the ones
// of the screens known today (the fake agent imitates each provider's
// texts); the conformance test checks them against the real CLIs.
func (s screenSet) evaluate(in ScreenInput) ScreenResult {
	for i := range s.known {
		if s.known[i].Pattern.MatchString(in.Text) {
			return ScreenResult{State: StateStarting, Known: &s.known[i]}
		}
	}
	prompt := s.prompt
	if in.ReadyPattern != nil {
		prompt = in.ReadyPattern
	}
	if prompt != nil && prompt.MatchString(lastLine(in.Text)) {
		if in.Stable || in.SessionStarted {
			return ScreenResult{State: StateReady}
		}
		return ScreenResult{State: StateStarting}
	}
	if s.busy == nil || s.busy.MatchString(in.Text) {
		return ScreenResult{State: StateBusy}
	}
	return ScreenResult{State: StateStarting}
}

// lastLine is the last non-empty line of the screen, where the input
// prompt of every adapter sits.
func lastLine(screen string) string {
	lines := strings.Split(strings.TrimRight(screen, "\n "), "\n")
	return lines[len(lines)-1]
}

// menuFirst picks the first, already selected option of a menu.
var menuFirst = []byte("\r")
