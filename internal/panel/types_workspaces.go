package panel

import "time"

// The stage states of the session zero a workspace shows.
const (
	StageDone    = "done"
	StageCurrent = "current"
	StageWaiting = "waiting"
	StageFailed  = "failed"
	StagePending = "pending"
)

// StageFailureInfo is a stage that failed: the code and the reason the engine
// recorded, how many times and when it tries again.
type StageFailureInfo struct {
	Code     string    `json:"code"`
	Reason   string    `json:"reason"`
	Failures int       `json:"failures"`
	At       time.Time `json:"at"`
	RetryAt  time.Time `json:"retry_at"`
}

// WorkspaceStage is one step of the session zero, in the order they run.
type WorkspaceStage struct {
	// Key is apps, mirror, files, analysis or approval_test.
	Key string `json:"key"`
	// State is StageDone, StageCurrent, StageWaiting, StageFailed or StagePending.
	State string `json:"state"`
	// Waiting is what the current stage waits for, when it waits.
	Waiting string            `json:"waiting,omitempty"`
	Failure *StageFailureInfo `json:"failure,omitempty"`
}

// WorkspaceRow is a workspace in the list.
type WorkspaceRow struct {
	Slug          string `json:"slug"`
	Repo          string `json:"repo"`
	DefaultBranch string `json:"default_branch"`
	// State is preparing, active or paused.
	State  string           `json:"state"`
	Stages []WorkspaceStage `json:"stages"`
	// Versioning is "" while the question is pending, then local or versioned;
	// VersioningAsked says the question was already put to the people.
	Versioning       string `json:"versioning"`
	VersioningAsked  bool   `json:"versioning_asked"`
	SessionZeroPR    string `json:"session_zero_pr,omitempty"`
	GrandMasterSheet string `json:"grand_master_sheet,omitempty"`
	// GrandMaster is the state of the Grand Master (none, idle, waiting, busy, stopped).
	GrandMaster string `json:"grand_master"`
	// FetchError is why the mirror cannot be updated, while it cannot.
	FetchError string    `json:"fetch_error,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	// CanAct says the user acts in this workspace; the buttons follow it and the
	// level, and the server checks both again at every request.
	CanAct bool `json:"can_act"`
}

// SheetOption is a sheet that may be the Grand Master of a workspace.
type SheetOption struct {
	Name  string `json:"name"`
	Agent string `json:"agent"`
}

// WorkspaceList is GET /api/workspaces.
type WorkspaceList struct {
	Workspaces []WorkspaceRow `json:"workspaces"`
	// Sheets are the sheets the Grand Master may have; empty for who cannot create.
	Sheets []SheetOption `json:"sheets"`
	// CanCreate says the user may create a workspace.
	CanCreate bool `json:"can_create"`
}

// CreateWorkspaceRequest is POST /api/workspaces.
type CreateWorkspaceRequest struct {
	// Repo is "owner/name".
	Repo string `json:"repo"`
	// Slug is optional and immutable once the workspace exists.
	Slug             string `json:"slug,omitempty"`
	GrandMasterSheet string `json:"grand_master_sheet"`
}

// VersioningRequest is POST /api/workspaces/{slug}/versioning.
type VersioningRequest struct {
	// Answer is local or versioned.
	Answer string `json:"answer"`
}

// VerificationConfirmed is the version of verificacao.yaml that is in force.
type VerificationConfirmed struct {
	Doc  string    `json:"doc"`
	Hash string    `json:"hash"`
	By   string    `json:"by"`
	At   time.Time `json:"at"`
}

// VerificationView is GET /api/workspaces/{slug}/verification: the file of the
// main clone (the proposal of the analysis, or what was written there), the
// version confirmed, and whether the file differs from it.
type VerificationView struct {
	// File is the content of .mestre/regras/verificacao.yaml in the main clone
	// ("" when it does not exist yet).
	File      string                 `json:"file"`
	Confirmed *VerificationConfirmed `json:"confirmed,omitempty"`
	// Commands are the commands the file proposes, in order (empty when it does
	// not parse; FileError says why).
	Commands  []string `json:"commands"`
	FileError string   `json:"file_error,omitempty"`
	// Outside is true when a version is confirmed and the file differs from it:
	// an edit made outside the panel, which is not in force.
	Outside bool `json:"outside"`
}

// ConfirmVerificationRequest is PUT /api/workspaces/{slug}/verification: the
// whole document, which becomes the confirmed version.
type ConfirmVerificationRequest struct {
	Doc string `json:"doc"`
}
