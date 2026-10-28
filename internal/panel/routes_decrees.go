package panel

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/decrees"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/users"
)

func init() { RegisterArea("decrees", mountDecrees) }

const (
	// useTail is how many of the last uses of a decree the list shows.
	useTail = 5
	// maxOrder bounds the text of an order: the engine keeps it as it came.
	maxOrder = 4000
)

func mountDecrees(r *Router) {
	p := r.Panel()
	r.Require("GET /api/decrees", p.handleDecrees, users.Status)
	// A proposal is not a decree: the engine checks the level and the workspaces of the
	// author, and only the confirmation of the same author makes it one.
	r.Require("POST /api/decrees", p.handleProposeDecree, users.OneOffException)
	r.Require("POST /api/decrees/proposals/{id}/confirm", p.handleConfirmDecree, users.OneOffException)
	r.Require("POST /api/decrees/{number}/revoke", p.handleRevokeDecree, users.Status)
	r.Require("POST /api/decrees/{number}/renew", p.handleRenewDelegation, users.DelegateHigh)
}

// backend is the backend of the rules, or the answer that it is not available.
func (p *Panel) backend(w http.ResponseWriter) (RulesBackend, bool) {
	b := p.rulesBackend()
	if b == nil {
		WriteError(w, Err(CodeUnavailable, "api.rules_unavailable", nil))
		return nil, false
	}
	return b, true
}

// actingWorkspaces are the workspaces of the person: the Owner's are all of them.
func (p *Panel) actingWorkspaces(ctx context.Context, me users.User) ([]string, error) {
	all, err := p.workspaceSlugs(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(s string) bool { return !me.Acts(s) }), nil
}

// workspaceOf checks that the workspace exists and that the person acts in it. One
// that is not theirs is told like one that does not exist.
func (p *Panel) workspaceOf(w http.ResponseWriter, r *http.Request, slug string) bool {
	slugs, err := p.actingWorkspaces(r.Context(), UserOf(r))
	if err != nil {
		p.log.Error("panel: reading the workspaces", "err", err)
		WriteError(w, errInternal())
		return false
	}
	if !slices.Contains(slugs, slug) {
		WriteError(w, Err(CodeNotFound, "api.workspace_not_found", nil))
		return false
	}
	return true
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	t = t.UTC()
	return &t
}

// canRevoke mirrors the table of what a level revokes (users.RevokeDecrees); the
// service decides again on the click.
func canRevoke(me users.User, v decrees.View) bool {
	if v.State.Ended() {
		return false
	}
	switch users.RevokeDecrees(me.Level) {
	case users.RevokeAny:
		return true
	case users.RevokeAdminAndApprover:
		return v.Level == classes.LevelAdmin || v.Level == classes.LevelApprover
	case users.RevokeOwnUnused:
		return v.Author == me.ID && v.Kind == decrees.KindException && v.State == decrees.Active
	}
	return false
}

func (p *Panel) decreeRow(ctx context.Context, b RulesBackend, me users.User, v decrees.View) (DecreeRow, error) {
	level := v.Level
	if level == "" {
		level = v.AuthorLevel
	}
	row := DecreeRow{Number: v.Number, Kind: string(v.Kind), State: string(v.State), Workspace: v.Workspace, Mesa: v.Mesa, Scope: v.Scope,
		Effect: v.Effect.Kind, Summary: v.Summary(), Text: v.Text, Author: v.Author, AuthorName: v.AuthorName, Level: string(level),
		CreatedAt: v.CreatedAt.UTC(), Until: timePtr(v.Until), EndedAt: timePtr(v.EndedAt), Frozen: v.Frozen,
		Uses: []DecreeUse{}}
	v.Level = level
	row.CanRevoke = canRevoke(me, v)
	if row.CanRevoke {
		need, err := b.NeedsPINToRevoke(ctx, v.Number)
		if err != nil {
			return row, err
		}
		row.RevokeNeedsPIN = need
	}
	uses, err := b.Uses(ctx, v.Number)
	if err != nil {
		return row, err
	}
	row.UseCount = len(uses)
	for _, u := range uses[max(0, len(uses)-useTail):] {
		row.Uses = append(row.Uses, DecreeUse{At: u.At.UTC(), Character: u.Character, Action: u.Action})
	}
	return row, nil
}

func (p *Panel) handleDecrees(w http.ResponseWriter, r *http.Request) {
	b, ok := p.backend(w)
	if !ok {
		return
	}
	ctx, me := r.Context(), UserOf(r)
	slugs, err := p.actingWorkspaces(ctx, me)
	if err != nil {
		WriteError(w, errInternal())
		return
	}
	all := r.URL.Query().Get("all") == "1"
	out := DecreeList{Workspaces: []DecreeWorkspace{}}
	for _, ws := range slugs {
		views, err := b.ListWorkspace(ctx, ws, all)
		if err != nil {
			p.log.Error("panel: reading the decrees", "workspace", ws, "err", err)
			WriteError(w, errInternal())
			return
		}
		group := DecreeWorkspace{Workspace: ws, Decrees: []DecreeRow{}}
		for _, v := range views {
			if v.Effect.Kind == decrees.EffectDelegation && !all {
				continue // delegations have their own screen
			}
			row, err := p.decreeRow(ctx, b, me, v)
			if err != nil {
				p.log.Error("panel: reading a decree", "number", v.Number, "err", err)
				WriteError(w, errInternal())
				return
			}
			group.Decrees = append(group.Decrees, row)
		}
		out.Workspaces = append(out.Workspaces, group)
	}
	WriteJSON(w, http.StatusOK, out)
}

// decreeError turns what the decrees service refuses into the answer of the API.
func (p *Panel) decreeError(w http.ResponseWriter, err error) {
	var ref *decrees.Refusal
	switch {
	case errors.As(err, &ref):
		WriteError(w, ErrMsg(ref.Msg).WithStatus(http.StatusUnprocessableEntity))
	case errors.Is(err, decrees.ErrPINRequired):
		WriteError(w, Err(CodePINRequired, "api.pin_required", nil))
	case errors.Is(err, decrees.ErrPINFailed):
		WriteError(w, Err(CodePINInvalid, "api.pin_invalid", nil))
	case errors.Is(err, decrees.ErrNotAllowed), errors.Is(err, decrees.ErrNotAuthor):
		WriteError(w, Err(CodeForbiddenLevel, "api.level_not_allowed", nil))
	case errors.Is(err, decrees.ErrEnded):
		WriteError(w, Err(CodeDecreeEnded, "api.decree_ended", nil))
	case errors.Is(err, decrees.ErrLocked):
		WriteError(w, Err(CodeDecreeLocked, "api.mestre_locked", nil))
	case errors.Is(err, decrees.ErrNoDecree):
		WriteError(w, Err(CodeNotFound, "api.decree_not_found", nil))
	case errors.Is(err, decrees.ErrNoProposal):
		WriteError(w, Err(CodeNoProposal, "api.proposal_gone", nil))
	case errors.Is(err, decrees.ErrTargetChanged):
		WriteError(w, ErrMsg(i18n.M("decree.target_changed")).WithStatus(http.StatusUnprocessableEntity))
	case errors.Is(err, decrees.ErrWrongRequest):
		WriteError(w, ErrMsg(i18n.M("decree.wrong_request")).WithStatus(http.StatusUnprocessableEntity))
	case errors.Is(err, decrees.ErrNoVerify):
		WriteError(w, ErrMsg(i18n.M("decree.no_verify")).WithStatus(http.StatusUnprocessableEntity))
	default:
		p.log.Error("panel: the decrees", "err", err)
		WriteError(w, errInternal())
	}
}

var (
	permissionFields = []string{"files", "commands", "tools", "domains"}
	targetKinds      = map[string]decrees.TargetKind{"class": decrees.TargetClass, "character": decrees.TargetCharacter,
		"table": decrees.TargetTable, "workspace": decrees.TargetWorkspace}
)

func grantOf(field, value string) *classes.Grant {
	g := &classes.Grant{}
	switch field {
	case "files":
		g.Files = []string{value}
	case "commands":
		g.Commands = []string{value}
	case "tools":
		g.Tools = []string{value}
	case "domains":
		g.Domains = []string{value}
	}
	return g
}

// proposalOf makes the proposal of the engine from what the form said. It checks only
// the shape; the level, the workspaces and the invariants are the engine's.
func proposalOf(me users.User, in DecreeInput) (decrees.Proposal, *APIError) {
	bad := func(field string) (decrees.Proposal, *APIError) {
		return decrees.Proposal{}, Err(CodeBadRequest, "api.invalid_field", map[string]any{"field": field})
	}
	text := strings.TrimSpace(in.Text)
	if text == "" || len(text) > maxOrder {
		return bad("text")
	}
	tk, ok := targetKinds[in.TargetKind]
	if !ok {
		return bad("target_kind")
	}
	target := decrees.Target{Kind: tk, Name: strings.TrimSpace(in.TargetName)}
	if tk == decrees.TargetWorkspace {
		target.Name = in.Workspace
	}
	if target.Name == "" {
		return bad("target_name")
	}
	p := decrees.Proposal{Author: me.ID, Workspace: in.Workspace, Mesa: strings.TrimSpace(in.Mesa), Target: target, Text: text,
		Origin: decrees.Origin{Channel: "panel"}}
	if tk == decrees.TargetTable && p.Mesa == "" {
		p.Mesa = target.Name
	}
	switch in.Kind {
	case "rule":
		p.Kind = decrees.KindRule
		if in.Until != nil {
			p.Until = *in.Until
		}
		switch in.Effect {
		case decrees.EffectPermission:
			if !slices.Contains(permissionFields, in.Field) || strings.TrimSpace(in.Value) == "" {
				return bad("value")
			}
			g := grantOf(in.Field, strings.TrimSpace(in.Value))
			switch in.Side {
			case "allow":
				p.Effect = decrees.Effect{Kind: decrees.EffectPermission, Allow: g}
			case "deny":
				p.Effect = decrees.Effect{Kind: decrees.EffectPermission, Deny: g}
			default:
				return bad("side")
			}
		case decrees.EffectBehavior:
			p.Effect = decrees.Effect{Kind: decrees.EffectBehavior, Instruction: strings.TrimSpace(in.Instruction)}
		default:
			return bad("effect")
		}
	case "exception":
		cmd := strings.TrimSpace(in.Command)
		if cmd == "" {
			return bad("command")
		}
		p.Kind = decrees.KindException
		p.Effect = decrees.Effect{Kind: decrees.EffectPermission, Allow: &classes.Grant{Commands: []string{cmd}}}
		p.Action = &classes.Action{Kind: providers.KindShell, Command: cmd}
	default:
		return bad("kind")
	}
	return p, nil
}

func (p *Panel) handleProposeDecree(w http.ResponseWriter, r *http.Request) {
	b, ok := p.backend(w)
	if !ok {
		return
	}
	ctx, me := r.Context(), UserOf(r)
	var in DecreeInput
	if !decode(w, r, &in) {
		return
	}
	if !p.workspaceOf(w, r, in.Workspace) {
		return
	}
	prop, apiErr := proposalOf(me, in)
	if apiErr != nil {
		WriteError(w, apiErr)
		return
	}
	pending, err := b.Propose(ctx, prop)
	if err != nil {
		p.decreeError(w, err)
		return
	}
	p.audit(ctx, "panel.decree.proposed", map[string]any{"user_id": me.ID, "workspace": in.Workspace, "proposal": pending.ID, "kind": in.Kind,
		"after": map[string]any{"summary": pending.Text, "pin": pending.PIN}, "ip": clientIP(r)})
	WriteJSON(w, http.StatusCreated, DecreeProposal{ID: pending.ID, Text: pending.Text, Order: pending.Order, PIN: pending.PIN, Why: pending.Why})
}

// handleConfirmDecree is the click of the author. The engine says whether the PIN is
// needed (before it changes anything); then the PIN of this request is the one the
// engine spends, in the transaction that makes the decree.
func (p *Panel) handleConfirmDecree(w http.ResponseWriter, r *http.Request) {
	b, ok := p.backend(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	confirm := func(w http.ResponseWriter, r *http.Request, pinRequest string) {
		ctx, me := r.Context(), UserOf(r)
		d, err := b.Confirm(ctx, id, me.ID, pinRequest)
		if err != nil {
			p.decreeError(w, err)
			return
		}
		p.audit(ctx, "panel.decree.created", map[string]any{"user_id": me.ID, "workspace": d.Workspace, "decree": d.Number, "proposal": id,
			"after": map[string]any{"kind": string(d.Kind), "state": string(d.State), "summary": d.Summary(), "text": d.Text, "until": timePtr(d.Until)}, "ip": clientIP(r)})
		WriteJSON(w, http.StatusCreated, ConfirmedDecree{Number: d.Number, State: string(d.State)})
	}
	if r.Header.Get(PINHeader) != "" {
		p.withPINRequest("decree:"+id, confirm)(w, r)
		return
	}
	confirm(w, r, "") // the engine refuses with "this needs the PIN" when it does
}

func (p *Panel) handleRevokeDecree(w http.ResponseWriter, r *http.Request) {
	b, ok := p.backend(w)
	if !ok {
		return
	}
	n, err := strconv.ParseInt(r.PathValue("number"), 10, 64)
	if err != nil {
		WriteError(w, errBadRequest())
		return
	}
	ctx, me := r.Context(), UserOf(r)
	before, err := b.Get(ctx, n)
	if err != nil || !me.Acts(before.Workspace) {
		WriteError(w, Err(CodeNotFound, "api.decree_not_found", nil))
		return
	}
	revoke := func(w http.ResponseWriter, r *http.Request, pinRequest string) {
		if err := b.Revoke(ctx, n, me.ID, pinRequest); err != nil {
			p.decreeError(w, err)
			return
		}
		p.audit(ctx, "panel.decree.revoked", map[string]any{"user_id": me.ID, "workspace": before.Workspace, "decree": n,
			"before": map[string]any{"state": string(before.State), "summary": before.Summary()}, "after": map[string]any{"state": string(decrees.Revoked)}, "ip": clientIP(r)})
		w.WriteHeader(http.StatusNoContent)
	}
	if r.Header.Get(PINHeader) != "" {
		p.withPINRequest("decree_revoke:"+strconv.FormatInt(n, 10), revoke)(w, r)
		return
	}
	revoke(w, r, "") // the engine asks for the PIN when the decree restricted something
}

// handleRenewDelegation is the click on Renovar: a new proposal of the same delegation,
// which the Owner confirms with the PIN like any other.
func (p *Panel) handleRenewDelegation(w http.ResponseWriter, r *http.Request) {
	b, ok := p.backend(w)
	if !ok {
		return
	}
	n, err := strconv.ParseInt(r.PathValue("number"), 10, 64)
	if err != nil {
		WriteError(w, errBadRequest())
		return
	}
	ctx, me := r.Context(), UserOf(r)
	before, err := b.Get(ctx, n)
	if err != nil || !me.Acts(before.Workspace) {
		WriteError(w, Err(CodeNotFound, "api.decree_not_found", nil))
		return
	}
	pending, err := b.Renew(ctx, n, me.ID)
	if err != nil {
		p.decreeError(w, err)
		return
	}
	p.audit(ctx, "panel.decree.renewal_proposed", map[string]any{"user_id": me.ID, "workspace": before.Workspace, "decree": n, "proposal": pending.ID,
		"after": map[string]any{"summary": pending.Text}, "ip": clientIP(r)})
	WriteJSON(w, http.StatusCreated, DecreeProposal{ID: pending.ID, Text: pending.Text, Order: pending.Order, PIN: pending.PIN, Why: pending.Why})
}
