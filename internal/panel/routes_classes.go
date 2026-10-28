package panel

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/users"
)

func init() { RegisterArea("classes", mountClasses) }

const pinClassApprove = "class.approve"

func mountClasses(r *Router) {
	p := r.Panel()
	// the Administrator approves a class of the workspace; the Grand Master never does,
	// and he is no user of the panel
	r.Require("GET /api/classes", p.handleClasses, users.ApproveWorkspaceCls)
	r.Sensitive("POST /api/classes/proposals/{id}/approve", pinClassApprove, users.ApproveWorkspaceCls, p.handleApproveProposal)
}

// appCount reads the count of the Apps of the account.
func (p *Panel) appCount(ctx context.Context) (AppCount, error) {
	reg := &github.Registry{DB: p.db, Clock: p.clk}
	others, err := reg.Others(ctx)
	if err != nil {
		return AppCount{}, err
	}
	total, err := reg.Count(ctx)
	if err != nil {
		return AppCount{}, err
	}
	return AppCount{Mine: total - others, Others: others, Total: total, Limit: github.AppLimit, Warn: github.AppWarning, Near: total >= github.AppWarning}, nil
}

// actingIn are the workspaces a user sees: all of them for the Owner.
func actingIn(u users.User) (list []string, all bool) {
	return u.Workspaces, u.Level == users.Owner
}

// handleClasses lists the global, workspace and table classes of the workspaces the
// user sees, with their Apps, and the proposals that wait for approval (the ones the
// Grand Master made and the changes made outside the Mestre).
// Part of CA-CLASSES-06 and CA-CLI-CLR-03 (the panel side) and of CA-ADM-02 (the band).
func (p *Panel) handleClasses(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	src := p.mg().Classes
	if src == nil {
		WriteError(w, Err(CodeUnavailable, "api.classes_unavailable", nil))
		return
	}
	apps, err := p.appCount(ctx)
	if err != nil {
		p.log.Error("panel: counting the Apps", "err", err)
		WriteError(w, errInternal())
		return
	}
	list, all := actingIn(UserOf(r))
	rows, props, err := src.Classes(ctx, list, all)
	if err != nil {
		p.log.Error("panel: listing the classes", "err", err)
		WriteError(w, errInternal())
		return
	}
	WriteJSON(w, http.StatusOK, ClassesPage{Apps: apps, Classes: orEmpty(rows), Proposals: orEmpty(props)})
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// handleApproveProposal is the "Aprovar" of a proposal. The library checks the level
// again, the hash the approver saw, the problems of the package and the ceiling; the
// workspace of a global class is "".
func (p *Panel) handleApproveProposal(w http.ResponseWriter, r *http.Request) {
	ctx, me := r.Context(), UserOf(r)
	src := p.mg().Classes
	if src == nil {
		WriteError(w, Err(CodeUnavailable, "api.classes_unavailable", nil))
		return
	}
	var in ApproveProposalRequest
	if !decode(w, r, &in) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 || in.Hash == "" {
		WriteError(w, errBadRequest())
		return
	}
	if list, all := actingIn(me); in.Workspace != "" && !all && !slices.Contains(list, in.Workspace) {
		WriteError(w, Err(CodeNotFound, "api.proposal_not_found", nil))
		return
	}
	err = src.Approve(ctx, in.Workspace, id, in.Hash, me)
	detail := map[string]any{"user_id": me.ID, "proposal": id, "workspace": in.Workspace, "hash": in.Hash, "ip": clientIP(r)}
	switch {
	case err == nil:
		p.audit(ctx, "panel.class.approved", detail)
		WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	case errors.Is(err, classes.ErrNeedsAdministrator), errors.Is(err, classes.ErrNotAuthorized):
		p.audit(ctx, "panel.class.approve_refused", detail)
		WriteError(w, errForbidden(err, map[string]any{"need": string(users.Administrator), "have": string(me.Level)}))
	case errors.Is(err, classes.ErrClassChanged):
		WriteError(w, Err(CodeProposalStale, "api.proposal_stale", nil))
	case errors.Is(err, classes.ErrProposalInvalid), errors.Is(err, classes.ErrNameTaken):
		WriteError(w, Err(CodeProposalBad, "api.proposal_bad", nil))
	default:
		p.log.Error("panel: approving a class", "proposal", id, "err", err)
		WriteError(w, errInternal())
	}
}
