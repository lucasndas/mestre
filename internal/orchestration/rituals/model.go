package rituals

import (
	"errors"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
)

// Action is what a ritual does when it fires.
type Action string

// The actions.
const (
	// ActionPrompt delivers a text to an agent, as an item `ritual`.
	ActionPrompt Action = "prompt"
	// ActionCommand types a line, and Enter, in a simple terminal.
	ActionCommand Action = "command"
	// ActionQuest runs a quest of the table.
	ActionQuest Action = "quest"
)

// State is the state of a ritual (docs/orquestracao/rituais.md#estados).
type State string

// The states of a ritual.
const (
	Enabled  State = "enabled"
	Disabled State = "disabled"
	// Finished is a --once that already fired.
	Finished State = "finished"
)

// FiringState is the state of a firing.
type FiringState string

// The states of a firing.
const (
	Queued     FiringState = "queued"
	PreRunning FiringState = "pre_running"
	// Delivering is the intent recorded before the side effect (the command
	// typed, the prompt queued): a firing found in it after a restart is never
	// repeated.
	Delivering FiringState = "delivering"
	Delivered  FiringState = "delivered"
	Expired    FiringState = "expired"
	Failed     FiringState = "failed"
	Skipped    FiringState = "skipped"
)

// Open says whether the firing still waits to be delivered.
func (s FiringState) Open() bool { return s == Queued || s == PreRunning || s == Delivering }

// Defaults of the document.
const (
	// DefaultTimeout is how long a firing waits for a busy target.
	DefaultTimeout = 30 * time.Minute
	// DefaultPreRunTimeout and DefaultPreRunOutput bound the pre-run.
	DefaultPreRunTimeout = 60 * time.Second
	DefaultPreRunOutput  = 16 << 10
	// OnTimeSlack is how late a firing may be and still be on time: the engine
	// looks at the clock about once a minute.
	OnTimeSlack = 2 * time.Minute
	// Placeholder is where the output of the pre-run goes.
	Placeholder = "{{output}}"
)

// Ritual is a routine of a table or of the workspace.
type Ritual struct {
	ID, Workspace string
	// Table is the table of the ritual ("" for a ritual of the workspace).
	Table string
	Name  string
	// Target is the name of the character it aims at; TargetTable the table of
	// the table master a ritual of the workspace aims at ("" for the Grand
	// Master and for a ritual of a table).
	Target, TargetTable string
	// Quest is the name of the quest it runs (Action quest).
	Quest    string
	Action   Action
	Content  string
	Schedule Schedule
	PreRun   string
	// Timeout is how long a firing waits in the queue of a busy target.
	Timeout   time.Duration
	State     State
	NextRun   time.Time
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Firing is one shot of a ritual: the history is kept in the database.
type Firing struct {
	ID           int64
	Ritual       string
	ScheduledFor time.Time
	CreatedAt    time.Time
	State        FiringState
	Reason       string
	// Late is true for a --once delivered after the engine was down.
	Late bool
	// Output is the tail of the error output of a pre-run that failed.
	Output      string
	DeliveredAt time.Time
	UpdatedAt   time.Time
}

// ActorKind says who asks.
type ActorKind string

// The actors.
const (
	ByCharacter ActorKind = "character"
	ByUser      ActorKind = "user"
	ByEngine    ActorKind = "engine"
)

// Actor is who asks: the Grand Master or a table master through the CLI, a user
// of the panel, or the engine.
type Actor struct {
	Kind      ActorKind
	Character characters.Character
	// ID is the id of the user.
	ID    string
	Level characters.UserLevel
}

// Errors of the rituals. The CLI of stage 11 maps them to its codes.
var (
	// ErrNoRitual: no such ritual in the scope.
	ErrNoRitual = errors.New("no such ritual")
	// ErrNameTaken: the scope has a ritual with the name.
	ErrNameTaken = errors.New("a ritual of the scope already has this name")
	// ErrBadRequest: the ritual does not make sense (no pre-run for the
	// placeholder, a prompt for a terminal, two kinds of content...).
	ErrBadRequest = errors.New("the ritual is not valid")
	// ErrNotVisible: the target is not a character of the scope (or does not
	// exist, or is a sheet).
	ErrNotVisible = errors.New("the target is not visible to the ritual")
	// ErrLevelDenied: who asks may not do it in this scope.
	ErrLevelDenied = errors.New("the level of the caller does not allow it")
	// ErrApproval: deleting a ritual needs the explicit request of an
	// authorized user, which the flow of approval brings (milestone 5).
	ErrApproval = errors.New("deleting a ritual needs the explicit request of an authorized user")
)
