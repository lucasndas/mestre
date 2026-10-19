package quests

import (
	"errors"
	"time"
)

// State is the state of a quest (docs/orquestracao/missoes.md#estados).
type State string

// The states of a quest.
const (
	Draft     State = "draft"
	Running   State = "running"
	Paused    State = "paused"
	Completed State = "completed"
	Cancelled State = "cancelled"
)

// StepState is the state of a step.
type StepState string

// The states of a step.
const (
	StepPending   StepState = "pending"
	StepPreparing StepState = "preparing"
	StepRunning   StepState = "running"
	StepStuck     StepState = "stuck"
	StepApproving StepState = "approving"
	StepCompleted StepState = "completed"
	StepDiscarded StepState = "discarded"
	StepCancelled StepState = "cancelled"
)

// Finished says whether the step needs nothing more in this round.
func (s StepState) Finished() bool {
	return s == StepCompleted || s == StepDiscarded || s == StepCancelled
}

// Active says whether a character has the step in its hands.
func (s StepState) Active() bool {
	return s == StepRunning || s == StepStuck || s == StepApproving
}

// What holds a step in preparing (Step.Blocker).
const (
	blockApproval = "approval:" // the request of an approval before
	blockSlot     = "slot:"     // the wait for a vacancy of the character
	blockBusy     = "busy"      // the character is doing another step
	blockSync     = "sync"      // the Tower does not have the branch yet
	blockResume   = "resume"    // the character was interrupted and is not back
)

// Reasons a quest is paused with.
const (
	ReasonPaused      = "pausada"
	ReasonReturnLimit = "limite de voltas"
	ReasonSameFailure = "mesma falha repetida"
)

// Quest is a quest of a table.
type Quest struct {
	ID, Workspace, Table, Name string
	State                      State
	PausedReason               string
	Def                        Definition
	MaxReturns                 int
	// Input is what the quest was run with (`quest run --input`).
	Input string
	// Round starts at 1 when the quest first runs and grows with every run
	// of a completed quest.
	Round int
	// Designer is the character that made the quest ("" for a user on the
	// canvas): it never decides an approval point of the quest.
	Designer string
	// Scroll is the key of the scroll "Missão: <name>".
	Scroll string
	// Issue is the issue on GitHub (0 until it is made).
	Issue int
	// Dirty counts the changes the mirror on GitHub has not gone over yet: the
	// mirror clears it only if it did not change while it ran.
	Dirty          int
	RoundCommented int
	IssueClosed    bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// StepDef returns the definition of the step in position pos.
func (q Quest) StepDef(pos int) StepDef { return q.Def.Steps[pos] }

// Step is the state of one step of a quest.
type Step struct {
	UID, Quest, ID string
	Pos            int
	State          StepState
	Character      string
	Returns        int
	// Entry is the reason of the last return: the input of the next delivery.
	Entry      string
	Blocker    string
	Approved   bool
	Approval   int64
	ResumeNote string
	// DiscardResult is true when a return made what the step is doing useless.
	DiscardResult  bool
	StuckAt        time.Time
	Escalated      bool
	SubIssue       int
	SubClosed      bool
	CommentedRound int
	UpdatedAt      time.Time
}

// Run is one delivery of a step to a character.
type Run struct {
	ID             int64
	Step, Quest    string
	Round, Attempt int
	Character      string
	CharacterName  string
	Class          string
	StartCommit    string
	EndCommit      string
	StartedAt      time.Time
	EndedAt        time.Time
	// Outcome is "" while it runs, then completed, discarded or returned.
	Outcome     string
	Summary     string
	Files       []string
	Uncommitted []string
	Warnings    []string
}

// The outcomes of a run.
const (
	OutcomeCompleted = "completed"
	OutcomeDiscarded = "discarded"
	OutcomeReturned  = "returned"
)

// Errors of the quests.
var (
	// ErrNoQuest: no such quest.
	ErrNoQuest = errors.New("no such quest")
	// ErrNameTaken: the table has a quest with that name.
	ErrNameTaken = errors.New("a quest of the table already has this name")
	// ErrInvalidState: the quest is not in a state that allows this.
	ErrInvalidState = errors.New("the quest is not in a state that allows this")
	// ErrNoStep: the character has no step under way.
	ErrNoStep = errors.New("the character has no step under way")
	// ErrNoReturnStep: the step of the character does not hand work back.
	ErrNoReturnStep = errors.New("the step does not return to another")
	// ErrSelfApproval: the designer of a quest never decides its approval
	// points.
	ErrSelfApproval = errors.New("the designer of the quest does not decide its approval points")
	// ErrNoRequest: no step waits for that request of approval.
	ErrNoRequest = errors.New("no step waits for this request of approval")
	// ErrTooLong: the summary of a done is over the limit.
	ErrTooLong = errors.New("the summary is too long")
	// ErrUsage: a done that asks for something that does not go together.
	ErrUsage = errors.New("invalid use of done")
	// ErrNoCriterion: a proof for a criterion the quest does not have.
	ErrNoCriterion = errors.New("the quest has no such criterion")
	// ErrProofTooLong: a proof over the limit.
	ErrProofTooLong = errors.New("the proof is too long")
	// ErrProofMissing: the class asks for a proof of every criterion.
	ErrProofMissing = errors.New("a criterion has no proof")
	// ErrNotMaster: only the Master of the table, or a user, makes a quest.
	ErrNotMaster = errors.New("only the Master of the table makes and conducts quests")
)

// MaxSummary is the size of the summary of a done (docs/cli/comunicacao.md).
const MaxSummary = 4000

// MaxSummaryFile is the size, in bytes, of the summary of a done that came
// from a file (`--file`).
const MaxSummaryFile = 64 << 10

// MaxProof is the size of the proof of a criterion, in characters.
const MaxProof = 500

// Causes of a failure a return may name.
var Causes = []string{"test", "build", "lint", "conflict", "network", "auth", "quota", "other"}

// MaxWhere is how many places a return may name.
const MaxWhere = 3
