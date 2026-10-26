package panel

import (
	"net/http"
	"time"
)

// The payloads of the administration screens of the users, the secrets and the
// audit (docs/painel/administracao.md). tygo turns them into web/src/gen/panel.ts;
// the tags are the contract.

// The codes of the errors of the administration screens.
const (
	CodeOwnerExists      Code = "owner_exists"
	CodeSelfAction       Code = "self_action"
	CodeBadLevel         Code = "bad_level"
	CodeInvalidWorkspace Code = "invalid_workspace"
	CodeTransferBad      Code = "transfer_bad"
	CodeNoTransfer       Code = "no_transfer"
	CodeSecretManaged    Code = "secret_managed"
	CodeSecretExists     Code = "secret_exists"
	CodeInvalidSecret    Code = "invalid_secret"
	CodeVaultClosed      Code = "vault_closed"
	CodeAckRequired      Code = "ack_required"
	CodeGitHubMismatch   Code = "github_login_mismatch"
	CodeGitHubDenied     Code = "github_denied"
	CodeGitHubExpired    Code = "github_expired"
	CodeGitHubNoLogin    Code = "github_no_login"
	CodeGitHubNoFlow     Code = "github_no_flow"
	CodeGitHubNotReady   Code = "github_not_ready"
	CodeGitHubFlowOff    Code = "github_flow_off"
	CodeGitHubNoRight    Code = "github_no_right"
)

func init() {
	for c, st := range map[Code]int{
		CodeOwnerExists:      http.StatusConflict,
		CodeSelfAction:       http.StatusConflict,
		CodeBadLevel:         http.StatusUnprocessableEntity,
		CodeInvalidWorkspace: http.StatusUnprocessableEntity,
		CodeTransferBad:      http.StatusConflict,
		CodeNoTransfer:       http.StatusNotFound,
		CodeSecretManaged:    http.StatusConflict,
		CodeSecretExists:     http.StatusConflict,
		CodeInvalidSecret:    http.StatusUnprocessableEntity,
		CodeVaultClosed:      http.StatusServiceUnavailable,
		CodeAckRequired:      http.StatusUnprocessableEntity,
		CodeGitHubMismatch:   http.StatusConflict,
		CodeGitHubDenied:     http.StatusForbidden,
		CodeGitHubExpired:    http.StatusConflict,
		CodeGitHubNoLogin:    http.StatusUnprocessableEntity,
		CodeGitHubNoFlow:     http.StatusConflict,
		CodeGitHubNotReady:   http.StatusServiceUnavailable,
		CodeGitHubFlowOff:    http.StatusServiceUnavailable,
		CodeGitHubNoRight:    http.StatusForbidden,
	} {
		status[c] = st
	}
}

// GitHubState is the personal GitHub credential of a user, never a token.
type GitHubState struct {
	// State is "" (none), "connected" or "expired".
	State string `json:"state"`
	// Login is the GitHub account that authorized.
	Login     string     `json:"login"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// UserRow is a user in the screen of the users.
type UserRow struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Login       string      `json:"login"`
	Level       string      `json:"level"`
	LevelName   string      `json:"level_name"`
	State       string      `json:"state"`
	Workspaces  []string    `json:"workspaces"`
	HasPIN      bool        `json:"has_pin"`
	Telegram    bool        `json:"telegram"`
	GitHubLogin string      `json:"github_login"`
	GitHub      GitHubState `json:"github"`
	CreatedAt   time.Time   `json:"created_at"`
}

// TransferInfo is the transfer of the post of Owner that waits for the answer of
// the chosen user.
type TransferInfo struct {
	FromID    string    `json:"from_id"`
	FromName  string    `json:"from_name"`
	ToID      string    `json:"to_id"`
	ToName    string    `json:"to_name"`
	ExpiresAt time.Time `json:"expires_at"`
	// ToMe is true for the user who is asked to accept it.
	ToMe bool `json:"to_me"`
}

// UserList is GET /api/users.
type UserList struct {
	Users []UserRow `json:"users"`
	// Workspaces are the slugs of every workspace, for the lists of workspaces.
	Workspaces []string      `json:"workspaces"`
	Transfer   *TransferInfo `json:"transfer,omitempty"`
}

// TransferState is GET /api/transfer: the transfer, for the user asked to accept
// it or the Owner who asked (nil: none).
type TransferState struct {
	Transfer *TransferInfo `json:"transfer,omitempty"`
}

// CreateUserRequest is POST /api/users (the PIN of the Owner in the header).
type CreateUserRequest struct {
	Name        string   `json:"name"`
	Level       string   `json:"level"`
	Workspaces  []string `json:"workspaces"`
	GitHubLogin string   `json:"github_login"`
}

// CreateUserResponse carries the user and the code of first access, which is
// shown once.
type CreateUserResponse struct {
	User UserRow            `json:"user"`
	Code AccessCodeResponse `json:"code"`
}

// EditUserRequest is PATCH /api/users/{id}; a field that is not sent stays.
type EditUserRequest struct {
	Name        *string   `json:"name,omitempty"`
	Level       *string   `json:"level,omitempty"`
	Workspaces  *[]string `json:"workspaces,omitempty"`
	GitHubLogin *string   `json:"github_login,omitempty"`
}

// PairingCodeResponse is the code of pairing of the Telegram, shown once.
type PairingCodeResponse struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

// TransferRequest is POST /api/transfer: the user who is to receive the post.
type TransferRequest struct {
	To string `json:"to"`
}

// GitHubChallenge is POST /api/me/github/begin: what the user types in GitHub.
type GitHubChallenge struct {
	UserCode        string    `json:"user_code"`
	VerificationURI string    `json:"verification_uri"`
	ExpiresAt       time.Time `json:"expires_at"`
	IntervalSeconds int       `json:"interval_seconds"`
}

// GitHubPoll is POST /api/me/github/poll: "pending" or "connected".
type GitHubPoll struct {
	State string `json:"state"`
}

// GitHubMe is GET /api/me/github: the credential of the user and whether his
// level and workspaces let him have one.
type GitHubMe struct {
	GitHub GitHubState `json:"github"`
	// Login is the account registered for him (the one that must authorize).
	Login string `json:"login"`
	// May is false for a level or a list of workspaces that do not connect.
	May bool `json:"may"`
}

// SiteMetaInfo is the metadata of a site credential, which is clear in the vault.
type SiteMetaInfo struct {
	OwnerUser string     `json:"owner_user"`
	Classes   []string   `json:"classes"`
	Domains   []string   `json:"domains"`
	Kind      string     `json:"kind"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// SecretNotice is a validity notice of a credential: "expiring", "expired" or
// "stale" (no validity and not replaced for a long time), with the days.
type SecretNotice struct {
	Kind string `json:"kind"`
	Days int    `json:"days"`
}

// SecretRow is a secret in the screen: never the value, not even masked.
type SecretRow struct {
	Name        string        `json:"name"`
	Type        string        `json:"type"`
	Fingerprint string        `json:"fingerprint"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	LastUsedAt  *time.Time    `json:"last_used_at,omitempty"`
	Site        *SiteMetaInfo `json:"site,omitempty"`
	// Managed is true for a secret the Mestre keeps itself (the keys of the
	// Apps, the tokens of the users, the CA): it is listed, not changed here.
	Managed bool          `json:"managed"`
	Notice  *SecretNotice `json:"notice,omitempty"`
}

// SecretList is GET /api/secrets.
type SecretList struct {
	Secrets []SecretRow `json:"secrets"`
}

// SiteCredentialInput is the credential of a site: the fields the vault keeps
// (docs/plataforma/cofre.md#credenciais-de-site).
type SiteCredentialInput struct {
	Name      string            `json:"name"`
	URL       string            `json:"url"`
	User      string            `json:"user"`
	Password  string            `json:"password"`
	Selectors map[string]string `json:"selectors,omitempty"`
	Success   string            `json:"success,omitempty"`
	OwnerUser string            `json:"owner_user"`
	Classes   []string          `json:"classes"`
	Domains   []string          `json:"domains"`
	Kind      string            `json:"kind"`
	ExpiresAt *time.Time        `json:"expires_at,omitempty"`
}

// ReplaceSecretRequest is PUT /api/secrets/{name}: the new value of a secret
// that is not a site credential, or the whole credential of a site (Site).
type ReplaceSecretRequest struct {
	Value string               `json:"value,omitempty"`
	Site  *SiteCredentialInput `json:"site,omitempty"`
}

// RecoveryKeyRequest is POST /api/recovery-key: Acknowledged says the Owner read
// the warning about the old backups.
type RecoveryKeyRequest struct {
	Acknowledged bool `json:"acknowledged"`
}

// RecoveryKeyResponse carries the new recovery key, which is shown once and kept
// nowhere.
type RecoveryKeyResponse struct {
	Key string `json:"key"`
}

// AuditEntry is a line of the audit trail.
type AuditEntry struct {
	ID        string         `json:"id"`
	At        time.Time      `json:"at"`
	Action    string         `json:"action"`
	UserID    string         `json:"user_id"`
	UserName  string         `json:"user_name"`
	Workspace string         `json:"workspace"`
	Detail    map[string]any `json:"detail"`
}

// AuditPage is GET /api/audit: the newest first; Next is the cursor of the next page
// ("" at the end).
type AuditPage struct {
	Entries []AuditEntry `json:"entries"`
	Next    string       `json:"next"`
	// People and Workspaces fill the filters of the screen: the users (their names
	// are in the lines anyway) and the workspaces the user may look at.
	People     []AuditPerson `json:"people"`
	Workspaces []string      `json:"workspaces"`
}

// AuditPerson is a user in the filter "who" of the audit.
type AuditPerson struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
