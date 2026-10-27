package panel

import "time"

// The terminal in the browser (docs/painel/canvas.md#terminais-no-canvas and
// #digitar-no-terminal). The topic is "terminal:<terminal id>"; the output goes
// to the browser in binary frames of type 0x01 (ID of the terminal, Pos the byte
// offset), the rest in ui.event and the two messages below. tygo turns these
// types into web/src/gen/panel.ts.

// The messages of the browser about a terminal.
const (
	// TypeTerminalInput (browser → Citadel) types into the terminal; the Citadel
	// checks the level of the user at each one. A refusal comes as an error
	// message with reply_to; success has no answer.
	TypeTerminalInput = "terminal.input"
	// TypeTerminalTypingEnd (browser → Citadel) ends the typing session (the
	// keyboard was released); the session also ends with the subscription.
	TypeTerminalTypingEnd = "terminal.typing_end"
)

// The events of a terminal topic (the first one is EventSnapshot, a TerminalInfo).
const (
	// EventReplayed says the replay reached the live output (TerminalReplayed).
	EventReplayed = "replayed"
	// EventSignal says the machine of the terminal lost, or got back, its
	// signal (TerminalSignal).
	EventSignal = "signal"
	// EventScreen is the picture of the screen a slow browser gets instead of the
	// output it lost (TerminalScreenEvent); the frames go on from its Offset.
	EventScreen = "screen"
)

// TerminalInfo is GET /api/characters/{id}/terminal and the snapshot of the
// topic "terminal:<id>".
type TerminalInfo struct {
	TerminalID string `json:"terminal_id"`
	// FrameID is the ULID that the binary frames of the terminal carry in the
	// browser channel (the terminal ids are not ULIDs): the panel derives it from
	// the terminal id, and the front end learns it here, before the first frame.
	FrameID   string `json:"frame_id"`
	Character string `json:"character"`
	Name      string `json:"name"`
	Machine   string `json:"machine"`
	Workspace string `json:"workspace"`
	// Table is the table the character sits at (empty for the Grand Master): the
	// terminal screen asks for the canvas of that table to show the mission.
	Table string `json:"table,omitempty"`
	// State is the state of the character; Busy says it is working (typing
	// into it asks for a confirmation).
	State string `json:"state"`
	Busy  bool   `json:"busy"`
	// Cols and Rows are the fixed size of the PTY; the browser never changes it.
	Cols int `json:"cols"`
	Rows int `json:"rows"`
	// Signal is false while the machine of the terminal is out of reach, since
	// NoSignalSince.
	Signal        bool       `json:"signal"`
	NoSignalSince *time.Time `json:"no_signal_since,omitempty"`
	// CanType says the user may type (decided by the server at each input).
	CanType bool `json:"can_type"`
}

// TerminalInputMessage is the payload of TypeTerminalInput.
type TerminalInputMessage struct {
	TerminalID string `json:"terminal_id"`
	Data       string `json:"data"`
}

// TerminalTypingEndMessage is the payload of TypeTerminalTypingEnd.
type TerminalTypingEndMessage struct {
	TerminalID string `json:"terminal_id"`
}

// TerminalReplayed is the payload of EventReplayed: the replay covered the
// bytes from From to To, and Lost says that before From some bytes already left
// the buffer of the machine ("trecho perdido").
type TerminalReplayed struct {
	From uint64 `json:"from"`
	To   uint64 `json:"to"`
	Lost bool   `json:"lost"`
}

// TerminalSignal is the payload of EventSignal.
type TerminalSignal struct {
	Signal bool       `json:"signal"`
	Since  *time.Time `json:"since,omitempty"`
}

// TerminalScreenEvent is the payload of EventScreen: the text of the screen at
// byte Offset of the output.
type TerminalScreenEvent struct {
	Text   string `json:"text"`
	Offset uint64 `json:"offset"`
}

// TerminalList is GET /api/terminals?name=<character>[&workspace=<slug>]: the
// terminals of the characters with that name that the user sees, which is how
// `mestre attach` finds a character by the name the canvas shows.
type TerminalList struct {
	Terminals []TerminalInfo `json:"terminals"`
}
