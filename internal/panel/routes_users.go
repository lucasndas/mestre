package panel

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/users"
)

func init() { RegisterArea("users", mountUsers) }

// PersonalGitHub is what the panel needs of the personal credentials of GitHub
// (*github.Personal): the device flow and the state. The token never passes
// through the panel: it goes from GitHub to the vault.
type PersonalGitHub interface {
	Begin(ctx context.Context, userID string) (github.Challenge, error)
	Poll(ctx context.Context, userID string) (github.PollResult, error)
	Status(ctx context.Context, userID string) (github.Status, error)
}

// The actions of PIN of this screen (the request of PIN is for one of them).
const (
	pinUserCreate   = "users.create"
	pinUserEdit     = "users.edit"
	pinUserSuspend  = "users.suspend"
	pinUserReturn   = "users.reactivate"
	pinUserRemove   = "users.remove"
	pinUserPairing  = "users.pairing_code"
	pinTransferStop = "users.transfer_cancel"
)

func mountUsers(r *Router) {
	p := r.Panel()
	r.Require("GET /api/users", p.handleUsers, users.ManageUsers)
	r.Sensitive("POST /api/users", pinUserCreate, users.ManageUsers, p.handleCreateUser)
	r.Sensitive("PATCH /api/users/{id}", pinUserEdit, users.ManageUsers, p.handleEditUser)
	r.Sensitive("POST /api/users/{id}/suspend", pinUserSuspend, users.ManageUsers, p.stateChange("suspend"))
	r.Sensitive("POST /api/users/{id}/reactivate", pinUserReturn, users.ManageUsers, p.stateChange("reactivate"))
	r.Sensitive("POST /api/users/{id}/remove", pinUserRemove, users.ManageUsers, p.stateChange("remove"))
	r.Sensitive("POST /api/users/{id}/pairing-code", pinUserPairing, users.ManageUsers, p.handlePairingCode)

	r.Auth("GET /api/transfer", p.handleTransferState)
	r.SensitiveRequest("POST /api/transfer", users.ActionStartTransfer, users.TransferOwner, p.handleStartTransfer)
	r.Sensitive("DELETE /api/transfer", pinTransferStop, users.TransferOwner, p.handleCancelTransfer)
	r.SensitiveRequest("POST /api/transfer/accept", users.ActionAcceptTransfer, "", p.handleAcceptTransfer)

	r.Auth("GET /api/me/github", p.handleGitHubMe)
	r.Require("POST /api/me/github/begin", p.handleGitHubBegin, users.ConnectOwnGitHub)
	r.Require("POST /api/me/github/poll", p.handleGitHubPoll, users.ConnectOwnGitHub)
}

func listOf[T any](l []T) []T {
	if l == nil {
		return []T{}
	}
	return l
}

func (p *Panel) githubState(ctx context.Context, userID string) GitHubState {
	if p.github == nil {
		return GitHubState{}
	}
	st, err := p.github.Status(ctx, userID)
	if err != nil {
		p.log.Warn("panel: reading the GitHub credential of a user", "user", userID, "err", err)
		return GitHubState{}
	}
	g := GitHubState{State: st.State, Login: st.Login}
	if !st.ExpiresAt.IsZero() {
		t := st.ExpiresAt.UTC()
		g.ExpiresAt = &t
	}
	return g
}

func (p *Panel) userRow(ctx context.Context, u users.User) UserRow {
	return UserRow{
		ID: u.ID, Name: u.Name, Login: u.Login, Level: string(u.Level), LevelName: u.Level.Name(), State: string(u.State),
		Workspaces: listOf(u.Workspaces), HasPIN: u.HasPIN, Telegram: u.TelegramID != 0, GitHubLogin: u.GitHubLogin,
		GitHub: p.githubState(ctx, u.ID), CreatedAt: u.CreatedAt,
	}
}

// workspaceSlugs are the slugs of every workspace.
func (p *Panel) workspaceSlugs(ctx context.Context) ([]string, error) {
	rows, err := p.db.Reader().QueryContext(ctx, `SELECT slug FROM workspaces ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// knownWorkspaces says whether every slug is a workspace; the first that is not.
func (p *Panel) knownWorkspaces(ctx context.Context, ws []string) (bad string, ok bool, err error) {
	all, err := p.workspaceSlugs(ctx)
	if err != nil {
		return "", false, err
	}
	for _, w := range ws {
		if !slices.Contains(all, w) {
			return w, false, nil
		}
	}
	return "", true, nil
}

func (p *Panel) handleUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list, err := p.users.List(ctx)
	if err != nil {
		p.log.Error("panel: listing the users", "err", err)
		WriteError(w, errInternal())
		return
	}
	ws, err := p.workspaceSlugs(ctx)
	if err != nil {
		WriteError(w, errInternal())
		return
	}
	out := UserList{Users: make([]UserRow, 0, len(list)), Workspaces: listOf(ws)}
	for _, u := range list {
		out.Users = append(out.Users, p.userRow(ctx, u))
	}
	out.Transfer = p.transferOf(ctx, UserOf(r).ID)
	WriteJSON(w, http.StatusOK, out)
}

// userError answers the refusals of the service of the users; false when err is not one.
func (p *Panel) userError(w http.ResponseWriter, err error) bool {
	var le *users.LevelError
	switch {
	case err == nil:
		return false
	case errors.As(err, &le):
		WriteError(w, errForbidden(err, map[string]any{"capability": string(le.Capability), "need": string(le.Need), "have": string(le.Have)}))
	case errors.Is(err, users.ErrNoUser):
		WriteError(w, Err(CodeNotFound, "api.user_not_found", nil))
	case errors.Is(err, users.ErrOwnerExists):
		WriteError(w, Err(CodeOwnerExists, "api.owner_exists", nil))
	case errors.Is(err, users.ErrSelf):
		WriteError(w, Err(CodeSelfAction, "api.self_action", nil))
	case errors.Is(err, users.ErrBadLevel):
		WriteError(w, Err(CodeBadLevel, "api.bad_level", nil))
	case errors.Is(err, users.ErrTransferBad):
		WriteError(w, Err(CodeTransferBad, "api.transfer_bad", nil))
	case errors.Is(err, users.ErrNoTransfer):
		WriteError(w, Err(CodeNoTransfer, "api.no_transfer", nil))
	case p.inputError(w, err):
	default:
		return false
	}
	return true
}

func (p *Panel) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	ctx, me := r.Context(), UserOf(r)
	var in CreateUserRequest
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		WriteError(w, errBadRequest())
		return
	}
	if bad, ok, err := p.knownWorkspaces(ctx, in.Workspaces); err != nil {
		WriteError(w, errInternal())
		return
	} else if !ok {
		WriteError(w, Err(CodeInvalidWorkspace, "api.workspace_unknown", map[string]any{"workspace": bad}))
		return
	}
	u, err := p.users.Create(ctx, me, users.NewUser{Name: in.Name, Level: users.Level(in.Level), Workspaces: in.Workspaces, GitHubLogin: in.GitHubLogin})
	if p.userError(w, err) {
		return
	}
	if err != nil {
		p.log.Error("panel: creating a user", "err", err)
		WriteError(w, errInternal())
		return
	}
	p.audit(ctx, "panel.user.created", map[string]any{"user_id": me.ID, "target": u.ID, "after": userSnapshot(u), "ip": clientIP(r)})
	c, err := p.users.NewAccessCode(ctx, me, u.ID, users.AccessFirst)
	if err != nil {
		p.log.Error("panel: the code of first access of a new user", "err", err)
		WriteError(w, errInternal()) // the user exists: the Owner gives him a code from the list
		return
	}
	p.audit(ctx, "panel.access_code.created", map[string]any{"user_id": me.ID, "target": u.ID, "kind": c.Kind, "expires_at": c.ExpiresAt, "ip": clientIP(r)})
	WriteJSON(w, http.StatusCreated, CreateUserResponse{User: p.userRow(ctx, u), Code: AccessCodeResponse{Code: c.Code, Kind: c.Kind, ExpiresAt: c.ExpiresAt}})
}

// userSnapshot is what the audit keeps of a user: nothing that is a secret.
func userSnapshot(u users.User) map[string]any {
	return map[string]any{"name": u.Name, "level": string(u.Level), "state": string(u.State), "workspaces": listOf(u.Workspaces), "github_login": u.GitHubLogin}
}

func (p *Panel) handleEditUser(w http.ResponseWriter, r *http.Request) {
	ctx, me, id := r.Context(), UserOf(r), r.PathValue("id")
	var in EditUserRequest
	if !decode(w, r, &in) {
		return
	}
	before, err := p.users.Get(ctx, id)
	if errors.Is(err, users.ErrNoUser) || err == nil && before.State == users.Removed {
		WriteError(w, Err(CodeNotFound, "api.user_not_found", nil))
		return
	}
	if err != nil {
		WriteError(w, errInternal())
		return
	}
	e := users.Edit{Name: in.Name, Workspaces: in.Workspaces, GitHubLogin: in.GitHubLogin}
	if in.Level != nil {
		l := users.Level(*in.Level)
		e.Level = &l
	}
	if in.Workspaces != nil {
		if bad, ok, err := p.knownWorkspaces(ctx, *in.Workspaces); err != nil {
			WriteError(w, errInternal())
			return
		} else if !ok {
			WriteError(w, Err(CodeInvalidWorkspace, "api.workspace_unknown", map[string]any{"workspace": bad}))
			return
		}
	}
	after, err := p.users.Edit(ctx, me, id, e)
	if p.userError(w, err) {
		return
	}
	if err != nil {
		p.log.Error("panel: editing a user", "err", err)
		WriteError(w, errInternal())
		return
	}
	p.audit(ctx, "panel.user.edited", map[string]any{"user_id": me.ID, "target": id, "before": userSnapshot(before), "after": userSnapshot(after), "ip": clientIP(r)})
	WriteJSON(w, http.StatusOK, p.userRow(ctx, after))
}

// stateChange is suspend, reactivate or remove.
func (p *Panel) stateChange(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, me, id := r.Context(), UserOf(r), r.PathValue("id")
		before, err := p.users.Get(ctx, id)
		if errors.Is(err, users.ErrNoUser) || err == nil && before.State == users.Removed {
			WriteError(w, Err(CodeNotFound, "api.user_not_found", nil))
			return
		}
		if err != nil {
			WriteError(w, errInternal())
			return
		}
		switch kind {
		case "suspend":
			err = p.users.Suspend(ctx, me, id)
		case "reactivate":
			err = p.users.Reactivate(ctx, me, id)
		default:
			err = p.users.Remove(ctx, me, id)
		}
		if p.userError(w, err) {
			return
		}
		if err != nil {
			p.log.Error("panel: changing the state of a user", "kind", kind, "err", err)
			WriteError(w, errInternal())
			return
		}
		after, _ := p.users.Get(ctx, id)
		p.audit(ctx, "panel.user."+kind, map[string]any{"user_id": me.ID, "target": id, "before": userSnapshot(before), "after": userSnapshot(after), "ip": clientIP(r)})
		if kind == "remove" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		WriteJSON(w, http.StatusOK, p.userRow(ctx, after))
	}
}

func (p *Panel) handlePairingCode(w http.ResponseWriter, r *http.Request) {
	ctx, me, id := r.Context(), UserOf(r), r.PathValue("id")
	c, err := p.users.NewCode(ctx, me, id)
	if p.userError(w, err) {
		return
	}
	if err != nil {
		WriteError(w, errInternal())
		return
	}
	p.audit(ctx, "panel.pairing_code.created", map[string]any{"user_id": me.ID, "target": id, "expires_at": c.ExpiresAt, "ip": clientIP(r)})
	WriteJSON(w, http.StatusOK, PairingCodeResponse{Code: c.Code, ExpiresAt: c.ExpiresAt})
}

// transferOf is the transfer that waits and concerns the user (he asked it or is
// asked to accept it); nil when none.
func (p *Panel) transferOf(ctx context.Context, userID string) *TransferInfo {
	var t TransferInfo
	var expires int64
	err := p.db.Reader().QueryRowContext(ctx, `SELECT t.from_user, f.name, t.to_user, o.name, t.expires_at FROM owner_transfers t
		JOIN users f ON f.id = t.from_user JOIN users o ON o.id = t.to_user
		WHERE t.state = 'pending' AND t.expires_at > ? AND (t.from_user = ? OR t.to_user = ?)`,
		p.clk.Now().UnixMilli(), userID, userID).Scan(&t.FromID, &t.FromName, &t.ToID, &t.ToName, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		p.log.Warn("panel: reading the transfer of the post of Owner", "err", err)
		return nil
	}
	t.ExpiresAt = time.UnixMilli(expires).UTC()
	t.ToMe = t.ToID == userID
	return &t
}

func (p *Panel) handleTransferState(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, TransferState{Transfer: p.transferOf(r.Context(), UserOf(r).ID)})
}

func (p *Panel) handleStartTransfer(w http.ResponseWriter, r *http.Request, request string) {
	ctx, me := r.Context(), UserOf(r)
	var in TransferRequest
	if !decode(w, r, &in) {
		return
	}
	err := p.users.StartTransfer(ctx, me, in.To, request)
	if p.userError(w, err) {
		return
	}
	if err != nil {
		p.log.Error("panel: starting the transfer of the post of Owner", "err", err)
		WriteError(w, errInternal())
		return
	}
	p.audit(ctx, "panel.transfer.started", map[string]any{"user_id": me.ID, "target": in.To, "ip": clientIP(r)})
	WriteJSON(w, http.StatusOK, TransferState{Transfer: p.transferOf(ctx, me.ID)})
}

func (p *Panel) handleCancelTransfer(w http.ResponseWriter, r *http.Request) {
	ctx, me := r.Context(), UserOf(r)
	err := p.users.CancelTransfer(ctx, me)
	if p.userError(w, err) {
		return
	}
	if err != nil {
		WriteError(w, errInternal())
		return
	}
	p.audit(ctx, "panel.transfer.cancelled", map[string]any{"user_id": me.ID, "ip": clientIP(r)})
	w.WriteHeader(http.StatusNoContent)
}

// handleAcceptTransfer is the acceptance of the user who was asked: the service
// checks that he is the one, and the change ends the sessions of both.
func (p *Panel) handleAcceptTransfer(w http.ResponseWriter, r *http.Request, request string) {
	ctx, me := r.Context(), UserOf(r)
	err := p.users.AcceptTransfer(ctx, me, request)
	if p.userError(w, err) {
		return
	}
	if err != nil {
		p.log.Error("panel: accepting the transfer of the post of Owner", "err", err)
		WriteError(w, errInternal())
		return
	}
	p.audit(ctx, "panel.transfer.accepted", map[string]any{"user_id": me.ID, "ip": clientIP(r)})
	p.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (p *Panel) handleGitHubMe(w http.ResponseWriter, r *http.Request) {
	ctx, me := r.Context(), UserOf(r)
	WriteJSON(w, http.StatusOK, GitHubMe{GitHub: p.githubState(ctx, me.ID), Login: me.GitHubLogin, May: me.MayHoldGitHub()})
}

func (p *Panel) githubError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, github.ErrLoginMismatch):
		WriteError(w, Err(CodeGitHubMismatch, "api.github_mismatch", nil))
	case errors.Is(err, github.ErrDeviceDenied):
		WriteError(w, Err(CodeGitHubDenied, "api.github_denied", nil))
	case errors.Is(err, github.ErrDeviceExpired):
		WriteError(w, Err(CodeGitHubExpired, "api.github_expired", nil))
	case errors.Is(err, github.ErrNoGitHubLogin):
		WriteError(w, Err(CodeGitHubNoLogin, "api.github_no_login", nil))
	case errors.Is(err, github.ErrNoDeviceFlow):
		WriteError(w, Err(CodeGitHubNoFlow, "api.github_no_flow", nil))
	case errors.Is(err, github.ErrNoCredential):
		WriteError(w, Err(CodeGitHubNoRight, "api.github_no_right", nil))
	case errors.Is(err, github.ErrDeviceFlowOff):
		WriteError(w, Err(CodeGitHubFlowOff, "api.github_flow_off", nil))
	case errors.Is(err, github.ErrAppNotReady), errors.Is(err, github.ErrNoClientSecret):
		WriteError(w, Err(CodeGitHubNotReady, "api.github_not_ready", nil))
	case errors.Is(err, github.ErrUnavailable):
		WriteError(w, Err(CodeUnavailable, "api.github_no_answer", nil))
	default:
		p.log.Error("panel: the GitHub connection", "err", err)
		WriteError(w, errInternal())
	}
}

func (p *Panel) handleGitHubBegin(w http.ResponseWriter, r *http.Request) {
	ctx, me := r.Context(), UserOf(r)
	if p.github == nil {
		WriteError(w, Err(CodeUnavailable, "api.github_unavailable", nil))
		return
	}
	if !me.MayHoldGitHub() {
		p.githubError(w, github.ErrNoCredential)
		return
	}
	ch, err := p.github.Begin(ctx, me.ID)
	if err != nil {
		p.audit(ctx, "panel.github.refused", map[string]any{"user_id": me.ID, "ip": clientIP(r)})
		p.githubError(w, err)
		return
	}
	p.audit(ctx, "panel.github.begun", map[string]any{"user_id": me.ID, "ip": clientIP(r)})
	WriteJSON(w, http.StatusOK, GitHubChallenge{UserCode: ch.UserCode, VerificationURI: ch.VerificationURI, ExpiresAt: ch.ExpiresAt.UTC(), IntervalSeconds: max(1, int(ch.Interval/time.Second))})
}

func (p *Panel) handleGitHubPoll(w http.ResponseWriter, r *http.Request) {
	ctx, me := r.Context(), UserOf(r)
	if p.github == nil {
		WriteError(w, Err(CodeUnavailable, "api.github_unavailable", nil))
		return
	}
	res, err := p.github.Poll(ctx, me.ID)
	if err != nil {
		p.audit(ctx, "panel.github.failed", map[string]any{"user_id": me.ID, "reason": githubReason(err), "ip": clientIP(r)})
		p.githubError(w, err)
		return
	}
	if res == github.PollConnected {
		p.audit(ctx, "panel.github.connected", map[string]any{"user_id": me.ID, "after": map[string]any{"login": p.githubState(ctx, me.ID).Login}, "ip": clientIP(r)})
	}
	WriteJSON(w, http.StatusOK, GitHubPoll{State: string(res)})
}

// githubReason is the short name of a refusal of the GitHub connection, for the audit.
func githubReason(err error) string {
	switch {
	case errors.Is(err, github.ErrLoginMismatch):
		return "login_mismatch"
	case errors.Is(err, github.ErrDeviceDenied):
		return "denied"
	case errors.Is(err, github.ErrDeviceExpired):
		return "expired"
	}
	return "error"
}
