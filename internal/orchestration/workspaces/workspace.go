package workspaces

import (
	"errors"
	"fmt"
	"time"
)

// State is the state of a workspace (docs/orquestracao/workspaces.md#estados).
type State string

// The states of a workspace.
const (
	// StatePreparing: the session zero is under way; there are no tables.
	StatePreparing State = "preparing"
	// StateActive: normal operation; the only state in which tables open.
	StateActive State = "active"
	// StatePaused: every table paused; the Grand Master hears users only.
	StatePaused State = "paused"
)

// Stage is a stage of the session zero of the workspace.
type Stage string

// The stages, in the order they run.
const (
	StageApps         Stage = "apps"          // the fixed Apps installed in the repository
	StageMirror       Stage = "mirror"        // the mirror and the main clone
	StageFiles        Stage = "files"         // .mestre/, skills, hooks and exclusions
	StageAnalysis     Stage = "analysis"      // analysis of the project, proposal of AGENTS.md
	StageApprovalTest Stage = "approval_test" // the approval test of an App
)

// Stages lists the stages in the order they run.
var Stages = []Stage{StageApps, StageMirror, StageFiles, StageAnalysis, StageApprovalTest}

// index is the position of s in Stages, or -1.
func (s Stage) index() int {
	for i, o := range Stages {
		if o == s {
			return i
		}
	}
	return -1
}

// StageFailure is a stage that failed: the code and the reason the panel
// shows, how many times it failed and when the engine tries it again.
type StageFailure struct {
	Code     string
	Reason   string
	Failures int
	At       time.Time
	RetryAt  time.Time
}

// FetchStatus is the health of the mirror.
type FetchStatus struct {
	// LastAt is the last successful fetch (zero: never).
	LastAt time.Time
	// FailingSince is the first failure of the current outage (zero: the
	// mirror is not failing); Error is the last one.
	FailingSince time.Time
	Error        string
	// Alerted says whether the operational alert of this outage was sent.
	Alerted bool
}

// AdvanceStatus is the fast-forward of the main clone being held back.
type AdvanceStatus struct {
	// BlockedSince is when the advance was first held back (zero: it is
	// not); Reason is why, as of the last attempt.
	BlockedSince time.Time
	Reason       string
	// Alerted says whether the Grand Master was told.
	Alerted bool
}

// Workspace is one workspace as the Guild database keeps it.
type Workspace struct {
	Slug          string
	Repo          Repo
	CloneURL      string
	DefaultBranch string
	State         State
	// Stage is the stage a preparing workspace is in; empty otherwise.
	Stage Stage
	// StageFailure is set while the stage fails.
	StageFailure *StageFailure
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Fetch        FetchStatus
	Advance      AdvanceStatus
}

// MirrorReady says whether the mirror and the main clone exist: the mirror
// stage is behind the workspace, or the workspace is past the session zero.
func (w Workspace) MirrorReady() bool {
	return w.State != StatePreparing || w.Stage.index() > StageMirror.index()
}

// Errors of the lifecycle.
var (
	// ErrNotFound is a workspace that does not exist.
	ErrNotFound = errors.New("workspaces: no such workspace")
	// ErrNotActive is a table asked of a workspace that is not active.
	ErrNotActive = errors.New("workspaces: the workspace is not active")
	// ErrPausePreparing is the pause of a workspace still preparing.
	ErrPausePreparing = errors.New("workspaces: a preparing workspace cannot be paused")
	// ErrBadTransition is a pause of a workspace that is not active or a
	// resume of one that is not paused.
	ErrBadTransition = errors.New("workspaces: the state does not allow this")
	// ErrNoRunner is a stage nobody runs yet: the workspace waits at it.
	ErrNoRunner = errors.New("workspaces: no runner for the stage")
)

// NotActiveError says why a table cannot open: the state and, while
// preparing, the stage.
type NotActiveError struct {
	Slug  string
	State State
	Stage Stage
}

func (e *NotActiveError) Error() string {
	if e.State == StatePreparing {
		return fmt.Sprintf("workspace %s não está ativo: está em preparação, etapa %s", e.Slug, e.Stage)
	}
	return fmt.Sprintf("workspace %s não está ativo: estado %s", e.Slug, e.State)
}

// Is makes errors.Is(err, ErrNotActive) true.
func (e *NotActiveError) Is(target error) bool { return target == ErrNotActive }
