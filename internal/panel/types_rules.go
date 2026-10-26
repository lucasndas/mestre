package panel

import (
	"net/http"
	"time"
)

// The payloads of the screens of the rules and of the chain: decrees, delegations
// and temporary table rules, the supervision of the chain, the ceiling, the
// approval policy and the extra instructions (docs/painel/administracao.md). tygo
// turns them into web/src/gen/panel.ts; the tags are the contract.

// The codes of the errors of those screens.
const (
	CodeDecreeRefused   Code = "decree_refused"
	CodeDecreeEnded     Code = "decree_ended"
	CodeDecreeLocked    Code = "mestre_locked"
	CodeNoProposal      Code = "no_proposal"
	CodeGrandMasterOnly Code = "grand_master_only"
	CodeNoTable         Code = "no_table"
	CodeTableOver       Code = "table_over"
	CodeReasonRequired  Code = "reason_required"
	CodeInvalidPolicy   Code = "invalid_policy"
	CodeFileChanged     Code = "file_changed"
	CodeFileRefused     Code = "file_refused"
	CodeTooLarge        Code = "too_large"
	CodeNoFolder        Code = "no_folder"
)

func init() {
	for c, st := range map[Code]int{
		CodeDecreeRefused:   http.StatusUnprocessableEntity,
		CodeDecreeEnded:     http.StatusConflict,
		CodeDecreeLocked:    http.StatusLocked,
		CodeNoProposal:      http.StatusNotFound,
		CodeGrandMasterOnly: http.StatusForbidden,
		CodeNoTable:         http.StatusNotFound,
		CodeTableOver:       http.StatusConflict,
		CodeReasonRequired:  http.StatusUnprocessableEntity,
		CodeInvalidPolicy:   http.StatusUnprocessableEntity,
		CodeFileChanged:     http.StatusConflict,
		CodeFileRefused:     http.StatusConflict,
		CodeTooLarge:        http.StatusRequestEntityTooLarge,
		CodeNoFolder:        http.StatusConflict,
	} {
		status[c] = st
	}
}

// DecreeRow is a decree as the screen shows it.
type DecreeRow struct {
	Number    int64  `json:"number"`
	Kind      string `json:"kind"`  // rule or exception
	State     string `json:"state"` // active, suspended, used, expired or revoked
	Workspace string `json:"workspace"`
	Mesa      string `json:"mesa"`
	Scope     string `json:"scope"`
	// Effect is the kind of effect (permission, behavior, policy, ceiling, delegation).
	Effect string `json:"effect"`
	// Summary is what the decree does, in the words of the engine.
	Summary string `json:"summary"`
	// Text is the order as the author gave it.
	Text       string `json:"text"`
	Author     string `json:"author"`
	AuthorName string `json:"author_name"`
	// Level is the level that counts now (the one the author has today).
	Level     string     `json:"level"`
	CreatedAt time.Time  `json:"created_at"`
	Until     *time.Time `json:"until,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	// Frozen says the Mestre is locked: every decree in force is shown as suspended.
	Frozen bool `json:"frozen"`
	// Uses is what the decree freed, in the order it happened (the last ones).
	Uses     []DecreeUse `json:"uses"`
	UseCount int         `json:"use_count"`
	// CanRevoke says the level of who asks may revoke it (the server decides again);
	// RevokeNeedsPIN says revoking it loosens something.
	CanRevoke      bool `json:"can_revoke"`
	RevokeNeedsPIN bool `json:"revoke_needs_pin"`
}

// DecreeUse is something done on the strength of a decree.
type DecreeUse struct {
	At        time.Time `json:"at"`
	Character string    `json:"character"`
	Action    string    `json:"action"`
}

// DecreeWorkspace is the decrees of one workspace.
type DecreeWorkspace struct {
	Workspace string      `json:"workspace"`
	Decrees   []DecreeRow `json:"decrees"`
}

// DecreeList is GET /api/decrees: the workspaces of who asks.
type DecreeList struct {
	Workspaces []DecreeWorkspace `json:"workspaces"`
}

// DecreeInput is POST /api/decrees: the proposal of a permanent rule or of a
// one-time exception. Nothing is a decree before the confirmation.
type DecreeInput struct {
	Workspace string `json:"workspace"`
	Mesa      string `json:"mesa,omitempty"`
	// Kind is "rule" or "exception".
	Kind string `json:"kind"`
	// Effect is "permission" or "behavior" (a rule), "permission" (an exception).
	Effect string `json:"effect"`
	// Target is what the rule is about: class, character, table or workspace.
	TargetKind string `json:"target_kind"`
	TargetName string `json:"target_name,omitempty"`
	// Field and Side say what a permission rule frees or forbids:
	// files, commands, tools or domains; allow or deny.
	Field string `json:"field,omitempty"`
	Side  string `json:"side,omitempty"`
	Value string `json:"value,omitempty"`
	// Command is the exact command an exception frees.
	Command string `json:"command,omitempty"`
	// Instruction is the short instruction of a behavior.
	Instruction string `json:"instruction,omitempty"`
	// Until is the end of the validity of a rule (empty: permanent).
	Until *time.Time `json:"until,omitempty"`
	// Text is the order as the person says it; it is kept as it is.
	Text string `json:"text"`
}

// DecreeProposal is what the engine understood, waiting for the click of the author.
type DecreeProposal struct {
	ID string `json:"id"`
	// Text is the confirmation: "Entendi: … Confirma?".
	Text  string `json:"text"`
	Order string `json:"order"`
	// PIN says confirming asks the PIN of the author; Why says why.
	PIN bool   `json:"pin"`
	Why string `json:"why,omitempty"`
}

// ConfirmedDecree is the decree the confirmation made.
type ConfirmedDecree struct {
	Number int64  `json:"number"`
	State  string `json:"state"`
}

// SubRow is a subdelegation.
type SubRow struct {
	ID        int64      `json:"id"`
	Workspace string     `json:"workspace"`
	Mesa      string     `json:"mesa"`
	Type      string     `json:"type"`
	Grantor   string     `json:"grantor"`
	Grantee   string     `json:"grantee"`
	Origin    int64      `json:"origin"`
	Reason    string     `json:"reason"`
	CreatedAt time.Time  `json:"created_at"`
	Until     time.Time  `json:"until"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Active    bool       `json:"active"`
	Decisions int        `json:"decisions"`
}

// RuleRow is a temporary table rule.
type RuleRow struct {
	ID        int64  `json:"id"`
	Workspace string `json:"workspace"`
	Table     string `json:"table"`
	Class     string `json:"class"`
	State     string `json:"state"`
	Risk      string `json:"risk"`
	// Level is the level of the author (agent, mesa, grand master or a person).
	Level  string `json:"level"`
	Author string `json:"author"`
	// Covers is what the rule frees: a pattern or the exact action.
	Covers    string     `json:"covers"`
	Mission   string     `json:"mission,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	Until     *time.Time `json:"until,omitempty"`
	Decisions int        `json:"decisions"`
}

// DelegationRow is a delegation of decision: a decree of effect delegation.
type DelegationRow struct {
	DecreeRow `json:",inline" tstype:",extends"`
	// Type is the type of request the Grand Master may decide.
	Type string `json:"type"`
	// Decisions is how many were taken under it.
	Decisions int `json:"decisions"`
}

// DelegationWorkspace is the delegations, subdelegations and temporary rules of
// one workspace.
type DelegationWorkspace struct {
	Workspace      string          `json:"workspace"`
	Delegations    []DelegationRow `json:"delegations"`
	Subdelegations []SubRow        `json:"subdelegations"`
	Rules          []RuleRow       `json:"rules"`
}

// DelegationList is GET /api/delegations.
type DelegationList struct {
	Workspaces []DelegationWorkspace `json:"workspaces"`
	// CanRevokeAll and CanRenew are the Owner's buttons; the server decides again.
	CanRevokeAll bool `json:"can_revoke_all"`
	CanRevoke    bool `json:"can_revoke"`
}

// RevokeAllResult is what the emergency button took away.
type RevokeAllResult struct {
	Delegations    int            `json:"delegations"`
	Subdelegations int            `json:"subdelegations"`
	Rules          int            `json:"rules"`
	RulesBy        map[string]int `json:"rules_by_workspace"`
}

// QueueRow is the queue of one level of approvers.
type QueueRow struct {
	// Level is agent, mesa, grand_master or people, as the engine names it.
	Level string `json:"level"`
	Table string `json:"table,omitempty"`
	Open  int    `json:"open"`
	// OldestSeconds is how long the oldest open request has waited.
	OldestSeconds int64 `json:"oldest_seconds"`
	AvgWaitSecs   int64 `json:"avg_wait_seconds"`
}

// PauseInfo is the pause of the autonomy of a table.
type PauseInfo struct {
	By string `json:"by"`
	// ByName is the name of the person, when it was one.
	ByName string `json:"by_name,omitempty"`
	// Kind is character (the Grand Master) or person.
	Kind   string    `json:"kind"`
	Reason string    `json:"reason"`
	Since  time.Time `json:"since"`
}

// ChainTable is a table in the supervision.
type ChainTable struct {
	Table  string     `json:"table"`
	Pause  *PauseInfo `json:"pause,omitempty"`
	Queues []QueueRow `json:"queues"`
	// CanResume says who asks may resume this pause now.
	CanResume bool `json:"can_resume"`
}

// ChainWorkspace is the supervision of one workspace.
type ChainWorkspace struct {
	Workspace string `json:"workspace"`
	// Queues are the levels that are not of a table (Grand Master, people).
	Queues []QueueRow   `json:"queues"`
	Tables []ChainTable `json:"tables"`
}

// ChainStatus is GET /api/chain.
type ChainStatus struct {
	Workspaces []ChainWorkspace `json:"workspaces"`
	// CanPause says the level of who asks may pause and resume.
	CanPause bool `json:"can_pause"`
}

// PauseInput is the body of the pause and of the resume: the reason is required.
type PauseInput struct {
	Reason string `json:"reason"`
}

// CeilingView is the ceiling in force of a workspace.
type CeilingView struct {
	Workspace string `json:"workspace"`
	// Present says there is a file; Valid says it is read; Error says why not.
	Present bool   `json:"present"`
	Valid   bool   `json:"valid"`
	Error   string `json:"error,omitempty"`
	// Text is the file as it is (read only here).
	Text string `json:"text"`
	// Widenings are the decrees of effect ceiling in force.
	Widenings []DecreeRow `json:"widenings"`
	// CanWiden says who asks may open a request to widen it.
	CanWiden bool `json:"can_widen"`
}

// WidenInput is POST /api/workspaces/{workspace}/ceiling/widen: what the ceiling
// is asked to allow. It opens a proposal of decree; it changes nothing by itself.
type WidenInput struct {
	Field string `json:"field"` // files, commands, tools or domains
	Value string `json:"value"`
	Text  string `json:"text"`
}

// PolicyFile is the approval policy file of a workspace.
type PolicyFile struct {
	Workspace string `json:"workspace"`
	Text      string `json:"text"`
	// Version is the hash of Text, the base of the next save.
	Version string `json:"version"`
	// Default says the workspace has no file and Text is the default content.
	Default bool `json:"default"`
}

// PolicyInput is PUT /api/workspaces/{workspace}/policy.
type PolicyInput struct {
	Text string `json:"text"`
	// Version is the version the edit began from; a file changed since is refused.
	Version string `json:"version"`
}

// Instructions are the extra instructions of the users for a workspace.
type Instructions struct {
	Workspace    string `json:"workspace"`
	GrandMaster  string `json:"grand_master"`
	TableMasters string `json:"table_masters"`
	MaxBytes     int    `json:"max_bytes"`
}

// InstructionsInput is PUT /api/workspaces/{workspace}/instructions: the text of
// one of the two files.
type InstructionsInput struct {
	// Role is "grand_master" or "table_masters".
	Role string `json:"role"`
	Text string `json:"text"`
}

// RuleWorkspaces is GET /api/rules/workspaces: the workspaces of who asks.
type RuleWorkspaces struct {
	Workspaces []string `json:"workspaces"`
}
