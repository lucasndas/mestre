package workspaces

import (
	"errors"
	"fmt"
	"time"

	"github.com/mestre-ia/mestre/internal/i18n"
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

// stageErrorMsg is the message of a failure code of the stages; any other code has the generic one.
func stageErrorMsg(code, detail string) i18n.Msg {
	switch code {
	case "analysis_failed":
		return i18n.M("workspace.stage_error_analysis_failed", "detail", detail)
	case "approval_request_failed":
		return i18n.M("workspace.stage_error_approval_request_failed", "detail", detail)
	case "approval_rules_failed":
		return i18n.M("workspace.stage_error_approval_rules_failed", "detail", detail)
	case "apps_check_failed":
		return i18n.M("workspace.stage_error_apps_check_failed", "detail", detail)
	case "apps_link_failed":
		return i18n.M("workspace.stage_error_apps_link_failed", "detail", detail)
	case "apps_notify_failed":
		return i18n.M("workspace.stage_error_apps_notify_failed", "detail", detail)
	case "clone_failed":
		return i18n.M("workspace.stage_error_clone_failed", "detail", detail)
	case "config_failed":
		return i18n.M("workspace.stage_error_config_failed", "detail", detail)
	case "credentials_failed":
		return i18n.M("workspace.stage_error_credentials_failed", "detail", detail)
	case "fetch_failed":
		return i18n.M("workspace.stage_error_fetch_failed", "detail", detail)
	case "files_failed":
		return i18n.M("workspace.stage_error_files_failed", "detail", detail)
	case "grand_master_failed":
		return i18n.M("workspace.stage_error_grand_master_failed", "detail", detail)
	case "layout_mismatch":
		return i18n.M("workspace.stage_error_layout_mismatch", "detail", detail)
	case "mkdir_failed":
		return i18n.M("workspace.stage_error_mkdir_failed", "detail", detail)
	case "notify_failed":
		return i18n.M("workspace.stage_error_notify_failed", "detail", detail)
	case "open_failed":
		return i18n.M("workspace.stage_error_open_failed", "detail", detail)
	case "verification_proposal_failed":
		return i18n.M("workspace.stage_error_verification_proposal_failed", "detail", detail)
	case "worktree_failed":
		return i18n.M("workspace.stage_error_worktree_failed", "detail", detail)
	}
	return i18n.M("workspace.stage_error_failed", "detail", detail)
}

// ReasonMsg is the failure as a message of the engine, for each reader to
// translate: the phrase of the engine comes from the code, and what the program
// that failed said (already masked in Reason) goes in as a text, never translated.
func (f StageFailure) ReasonMsg() i18n.Msg {
	return stageErrorMsg(f.Code, f.Reason)
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
		return fmt.Sprintf("campanha %s não está ativa: está em preparação, etapa %s", e.Slug, e.Stage)
	}
	return fmt.Sprintf("campanha %s não está ativa: estado %s", e.Slug, e.State)
}

// Is makes errors.Is(err, ErrNotActive) true.
func (e *NotActiveError) Is(target error) bool { return target == ErrNotActive }

// ErrWaiting is a stage that is not done and did not fail: it waits for
// something outside the engine (the fixed Apps installed in the repository,
// for one). A runner returns a *WaitError; the workspace stays at the stage,
// no failure is recorded and the engine looks again after the wait
// (docs/sessao-zero/workspace.md#início).
var ErrWaiting = errors.New("workspaces: the stage is waiting")

// DefaultWaitPoll is how long a waiting stage waits before it is run again
// when the runner does not say.
const DefaultWaitPoll = time.Minute

// WaitError is what a stage returns to wait: Reason is the text for the
// panel and Poll how long before the stage runs again (zero: DefaultWaitPoll).
type WaitError struct {
	Reason string
	// Msg is Reason as a message of the engine, for the panel to translate (Reason
	// is then its English text, for logs); empty when Reason is all there is.
	Msg  i18n.Msg
	Poll time.Duration
}

func (e *WaitError) Error() string { return "waiting: " + e.Reason }

// Is makes errors.Is(err, ErrWaiting) true.
func (e *WaitError) Is(target error) bool { return target == ErrWaiting }
