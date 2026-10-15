package tables

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
)

// State is the state of a table (docs/orquestracao/mesas.md#estados). It is
// the type the characters already use.
type State = characters.TableState

// The states.
const (
	StateOpen       = characters.TableOpen
	StateWorking    = characters.TableWorking
	StateInReview   = characters.TableInReview
	StateClosing    = characters.TableClosing
	StateClosed     = characters.TableClosed
	StateCancelling = characters.TableCancelling
	StateCancelled  = characters.TableCancelled
)

// Event is what moves a table from a state to another.
type Event string

// The events of the diagram of the document.
const (
	// EventReady: the table master is ready and has the request.
	EventReady Event = "master_ready"
	// EventPROpened: `pr open` (or `pr update`) concluded.
	EventPROpened Event = "pr_opened"
	// EventMission: a mission of adjustment began while in review.
	EventMission Event = "mission_started"
	// EventClose: `table close` of the Grand Master, with the merge confirmed.
	EventClose Event = "close"
	// EventClosed: the closing is over.
	EventClosed Event = "closed"
	// EventCancel: a cancellation was accepted.
	EventCancel Event = "cancel"
	// EventCancelled: the steps of the cancellation are over.
	EventCancelled Event = "cancelled"
)

// Events lists the events, in the order of the document.
var Events = []Event{EventReady, EventPROpened, EventMission, EventClose, EventClosed, EventCancel, EventCancelled}

// States lists the states, in the order of the document.
var States = []State{StateOpen, StateWorking, StateInReview, StateClosing, StateClosed, StateCancelling, StateCancelled}

// ErrBadTransition: the event does not apply in the state.
var ErrBadTransition = errors.New("tables: the state does not allow this")

// Transition is the diagram of the document: the state an event takes a
// table to, or ErrBadTransition. Closed and cancelled tables never reopen.
func Transition(from State, ev Event) (State, error) {
	var to State
	switch ev {
	case EventReady:
		if from == StateOpen {
			to = StateWorking
		}
	case EventPROpened:
		if from == StateWorking {
			to = StateInReview
		}
	case EventMission:
		if from == StateInReview {
			to = StateWorking
		}
	case EventClose:
		if from == StateInReview {
			to = StateClosing
		}
	case EventClosed:
		if from == StateClosing {
			to = StateClosed
		}
	case EventCancel:
		if from == StateOpen || from == StateWorking || from == StateInReview {
			to = StateCancelling
		}
	case EventCancelled:
		if from == StateCancelling {
			to = StateCancelled
		}
	}
	if to == "" {
		return "", fmt.Errorf("%w: %s in %s", ErrBadTransition, ev, from)
	}
	return to, nil
}

// Over says whether a table reached the end for good.
func Over(s State) bool { return s == StateClosed || s == StateCancelled }

// Table is one table as the Guild database keeps it.
type Table struct {
	Workspace string
	// ID is "<epic>-<slug>" (MESTRE_MESA); the branch is the prefix and ID.
	ID            string
	Epic          int
	EpicTitle     string
	Branch        string
	BaseBranch    string
	BaseCommit    string
	State         State
	Paused        bool
	PausedReason  string
	PausedBy      string
	PausedAt      time.Time
	PRNumber      int
	PRHead        string
	Merged        bool
	MergedHead    string
	MergedAt      time.Time
	KeepWorktrees bool
	CancelReason  string
	// CancelRemoteDone: the pull request and the epic were closed.
	CancelRemoteDone  bool
	CancelBranchUntil time.Time
	BranchDeleted     bool
	OpenedBy          string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	ClosedAt          time.Time
}

// Worktree is the worktree of a table on a machine.
type Worktree struct {
	Workspace, Table, Machine string
	Dir, GitDir, Common       string
	// LastCommit is the last commit of the branch the engine itself
	// registered on this machine.
	LastCommit string
	// RefCommit is what the engine registered for the ref a Tower sent to the
	// Citadel (refs/mestre/torres/...); empty for the Citadel's own.
	RefCommit string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Errors of the lifecycle; the handlers turn them into the codes of the CLI.
var (
	// ErrNoTable: no such table.
	ErrNoTable = errors.New("tables: no such table")
	// ErrEpicNotFound: the epic does not exist or has no `epico` label.
	ErrEpicNotFound = errors.New("tables: the epic does not exist or has no `epico` label")
	// ErrEpicClosed: the epic is closed.
	ErrEpicClosed = errors.New("tables: the epic is closed")
	// ErrTableExists: the epic belongs to another table that is not over.
	ErrTableExists = errors.New("tables: the epic already has an open table")
	// ErrNotMerged: `table close` without the merge confirmed.
	ErrNotMerged = errors.New("tables: the merge of the pull request was not confirmed")
	// ErrUnmergedChanges: a worktree or a character keeps the table from
	// ending; the error carries the list.
	ErrUnmergedChanges = errors.New("tables: something keeps the table from ending")
	// ErrApprovalRequired: the cancellation waits for the approval of a
	// destructive action.
	ErrApprovalRequired = errors.New("tables: the cancellation needs the approval of a destructive action")
	// ErrNotGrandMaster: only the Grand Master of the workspace opens and
	// closes tables.
	ErrNotGrandMaster = errors.New("tables: only the Grand Master of the workspace does this")
	// ErrPaused: the table is paused.
	ErrPaused = errors.New("tables: the table is paused")
	// ErrRefMoved: a ref moved without passing through the engine.
	ErrRefMoved = errors.New("tables: a ref moved without passing through the engine")
	// ErrNoWorktree: the table has no worktree on the machine.
	ErrNoWorktree = errors.New("tables: the table has no worktree on the machine")
	// ErrMachineUnreachable: a Tower of the table cannot be reached.
	ErrMachineUnreachable = errors.New("tables: the machine cannot be reached")
)

// MissingError is what keeps a table from ending: the list the Grand Master
// takes to the users. Pending is the part that only waits (a character that
// reaches its safe point), Decide the part that needs a decision.
type MissingError struct {
	Table   string
	Pending []string
	Decide  []string
}

func (e *MissingError) Error() string {
	var parts []string
	if len(e.Decide) > 0 {
		parts = append(parts, "falta decidir: "+strings.Join(e.Decide, "; "))
	}
	if len(e.Pending) > 0 {
		parts = append(parts, "aguardando: "+strings.Join(e.Pending, "; "))
	}
	return "a mesa " + e.Table + " continua encerrando — " + strings.Join(parts, " — ")
}

// Is makes errors.Is(err, ErrUnmergedChanges) true.
func (e *MissingError) Is(target error) bool { return target == ErrUnmergedChanges }

// Settings are the settings of a workspace that matter here
// (docs/orquestracao/workspaces.md#configuração-do-workspace). Whoever keeps
// the configuration (the panel, milestone 6) implements it; the defaults of
// the documents are in DefaultSettings.
type Settings interface {
	// BranchPrefix is the prefix of the branches of the tables ("epico/").
	BranchPrefix(workspace string) string
	// TableMasterSheet is the default sheet of the table masters.
	TableMasterSheet(workspace string) string
	// CancelRetention is how long the branch of a cancelled table stays in
	// the mirror.
	CancelRetention(workspace string) time.Duration
	// ExcludeMestre says whether .mestre/ stays out of git in the workspace.
	ExcludeMestre(workspace string) bool
}

// The defaults of the documents.
const (
	DefaultBranchPrefix    = "epico/"
	DefaultCancelRetention = 30 * 24 * time.Hour
	// DefaultTableMasterSheet is the sheet used when the workspace names none.
	DefaultTableMasterSheet = "Mestre da mesa"
)

// DefaultSettings is Settings with the defaults.
type DefaultSettings struct{}

// BranchPrefix implements Settings.
func (DefaultSettings) BranchPrefix(string) string { return DefaultBranchPrefix }

// TableMasterSheet implements Settings.
func (DefaultSettings) TableMasterSheet(string) string { return DefaultTableMasterSheet }

// CancelRetention implements Settings.
func (DefaultSettings) CancelRetention(string) time.Duration { return DefaultCancelRetention }

// ExcludeMestre implements Settings.
func (DefaultSettings) ExcludeMestre(string) bool { return true }
