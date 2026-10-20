package waits

import (
	"errors"
	"strings"
	"time"
)

// Kind is the type of a wait
// (docs/orquestracao/esperas-e-caixa-de-entrada.md#esperas).
type Kind string

// The types. The wait for a vacancy (waiting_slot) is kept by the characters
// (character_slot_waits) and only listed here.
const (
	KindCharacter Kind = "character"
	KindSteps     Kind = "steps"
	KindApproval  Kind = "approval"
	KindTime      Kind = "time"
	KindSync      Kind = "sync"
	KindEpic      Kind = "epic"
	KindSlot      Kind = "slot"
)

// Mode says how many of the targets satisfy the wait.
type Mode string

// The modes.
const (
	All   Mode = "all"
	First Mode = "first"
)

// State is the state of a wait.
type State string

// The states.
const (
	Active    State = "active"
	Satisfied State = "satisfied"
	Expired   State = "expired"
	Cancelled State = "cancelled"
)

// Target is something a wait waits for: a character, a step of a quest, a
// request of approval, a synchronization.
type Target struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Done bool   `json:"done,omitempty"`
	// Result is what the target answered (its done).
	Result string `json:"result,omitempty"`
}

// Wait is a registered wait.
type Wait struct {
	ID               string
	Workspace, Table string
	// Waiter is the character that waits.
	Waiter  string
	Kind    Kind
	Mode    Mode
	Targets []Target
	// Reason is what the canvas and `mestre list` show after "aguardando".
	Reason string
	// Quest is the quest the wait belongs to; it is cancelled with it.
	Quest     string
	State     State
	Result    string
	CreatedAt time.Time
	// TimeoutAt is when the wait expires (zero: never); UntilAt the moment a
	// wait for a time ends.
	TimeoutAt, UntilAt time.Time
	EndedAt            time.Time
}

// Pending lists the targets that are not done yet.
func (w Wait) Pending() []Target {
	var out []Target
	for _, t := range w.Targets {
		if !t.Done {
			out = append(out, t)
		}
	}
	return out
}

// satisfied says whether the targets that are done satisfy the wait.
func (w Wait) satisfied() bool {
	if len(w.Targets) == 0 {
		return false
	}
	done := 0
	for _, t := range w.Targets {
		if t.Done {
			done++
		}
	}
	if w.Mode == First {
		return done > 0
	}
	return done == len(w.Targets)
}

// reasonOf writes what the character waits for: "Revisor", "QA e Revisor
// (todas)", "QA e Revisor (primeira)".
func reasonOf(names []string, mode Mode) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	list := strings.Join(names[:len(names)-1], ", ") + " e " + names[len(names)-1]
	if mode == First {
		return list + " (primeira)"
	}
	return list + " (todas)"
}

// Ask is an ask that was made.
type Ask struct {
	ID               string
	Workspace, Table string
	Asker, Target    string
	// Item is the item of the inbox that carries the question.
	Item     string
	State    AskState
	Blocking bool
	Response string
	// TimedOutAt is when the time limit of a blocking call ran out (zero: it
	// did not).
	TimedOutAt time.Time
	CreatedAt  time.Time
	AnsweredAt time.Time
}

// AskState is the state of an ask.
type AskState string

// The states of an ask.
const (
	AskOpen      AskState = "open"
	AskAnswered  AskState = "answered"
	AskCancelled AskState = "cancelled"
)

// Cycle is a deadlock: characters that wait for each other in a ring.
type Cycle struct {
	Key       string
	Workspace string
	// Text is the cycle written as "A → B → A".
	Text      string
	CreatedAt time.Time
}

// Timeouts are the time limits of the waits
// (docs/orquestracao/esperas-e-caixa-de-entrada.md#tempos-limite).
type Timeouts struct {
	// Ask is the default time limit of a blocking ask and MaxAsk the largest
	// that may be asked for.
	Ask, MaxAsk time.Duration
	Character   time.Duration
	Steps       time.Duration
	Slot        time.Duration
}

// DefaultTimeouts are the defaults of the document.
func DefaultTimeouts() Timeouts {
	return Timeouts{Ask: 90 * time.Second, MaxAsk: 9 * time.Minute, Character: 60 * time.Minute, Steps: 120 * time.Minute, Slot: 30 * time.Minute}
}

func (t Timeouts) filled() Timeouts {
	d := DefaultTimeouts()
	if t.Ask <= 0 {
		t.Ask = d.Ask
	}
	if t.MaxAsk <= 0 {
		t.MaxAsk = d.MaxAsk
	}
	if t.Character <= 0 {
		t.Character = d.Character
	}
	if t.Steps <= 0 {
		t.Steps = d.Steps
	}
	if t.Slot <= 0 {
		t.Slot = d.Slot
	}
	return t
}

// Errors of the waits.
var (
	// ErrNoAttachments: the attachments of a message could not be passed on.
	ErrNoAttachments = errors.New("the attachments cannot be passed on")
	// ErrSelf: a character waits for, or asks, itself.
	ErrSelf = errors.New("a character cannot wait for itself or ask itself")
	// ErrPast: a wait for a time that already passed.
	ErrPast = errors.New("the time is in the past")
	// ErrNoQuest: no such quest.
	ErrNoQuest = errors.New("no such quest")
	// ErrNoWait: no such wait.
	ErrNoWait = errors.New("no such wait")
	// ErrBadWait: the request of a wait does not make sense.
	ErrBadWait = errors.New("the wait is not valid")
)
