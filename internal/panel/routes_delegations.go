package panel

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/mestre-ia/mestre/internal/decrees"
	"github.com/mestre-ia/mestre/internal/orchestration/tablerules"
	"github.com/mestre-ia/mestre/internal/users"
)

func init() { RegisterArea("delegations", mountDelegations) }

func mountDelegations(r *Router) {
	p := r.Panel()
	r.Require("GET /api/delegations", p.handleDelegations, users.Status)
	r.Require("POST /api/subdelegations/{id}/revoke", p.handleRevokeSub, users.RevokeTemporaryRules)
	r.Require("POST /api/table-rules/{id}/revoke", p.handleRevokeRule, users.RevokeTemporaryRules)
	// the emergency button only restricts, so it asks no PIN (docs/aprovacoes/delegacao.md#revogar-tudo)
	r.Require("POST /api/delegations/revoke-all", p.handleRevokeAll, users.DelegateHigh)
}

func subRow(s decrees.Sub, decisions int) SubRow {
	return SubRow{ID: s.ID, Workspace: s.Workspace, Mesa: s.Mesa, Type: s.Type, Grantor: s.Grantor, Grantee: s.Grantee, Origin: s.Origin, Reason: s.Reason,
		CreatedAt: s.CreatedAt.UTC(), Until: s.Until.UTC(), EndedAt: timePtr(s.EndedAt), Active: s.Active, Decisions: decisions}
}

func ruleRow(x tablerules.Rule, decisions int) RuleRow {
	covers := x.Pattern
	if x.Action != nil {
		covers = strings.TrimSpace(x.Action.Command + " " + strings.Join(x.Action.Paths, " ") + " " + x.Action.URL)
	}
	return RuleRow{ID: x.ID, Workspace: x.Workspace, Table: x.Table, Class: x.Class, State: string(x.State), Risk: string(x.Risk), Level: string(x.Level),
		Author: x.Author, Covers: covers, Mission: x.Mission, CreatedAt: x.CreatedAt.UTC(), Until: timePtr(x.Until), Decisions: decisions}
}

func (p *Panel) handleDelegations(w http.ResponseWriter, r *http.Request) {
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
	out := DelegationList{Workspaces: []DelegationWorkspace{}, CanRevokeAll: users.Can(me.Level, users.DelegateHigh), CanRevoke: users.Can(me.Level, users.RevokeTemporaryRules)}
	for _, ws := range slugs {
		g, err := p.delegationsOf(ctx, b, me, ws)
		if err != nil {
			p.log.Error("panel: reading the delegations", "workspace", ws, "err", err)
			WriteError(w, errInternal())
			return
		}
		out.Workspaces = append(out.Workspaces, g)
	}
	WriteJSON(w, http.StatusOK, out)
}

func (p *Panel) delegationsOf(ctx context.Context, b RulesBackend, me users.User, ws string) (DelegationWorkspace, error) {
	g := DelegationWorkspace{Workspace: ws, Delegations: []DelegationRow{}, Subdelegations: []SubRow{}, Rules: []RuleRow{}}
	views, err := b.ListWorkspace(ctx, ws, false)
	if err != nil {
		return g, err
	}
	for _, v := range views {
		if v.Effect.Kind != decrees.EffectDelegation {
			continue
		}
		row, err := p.decreeRow(ctx, b, me, v)
		if err != nil {
			return g, err
		}
		g.Delegations = append(g.Delegations, DelegationRow{DecreeRow: row, Type: v.Effect.Setting, Decisions: row.UseCount})
	}
	subs, err := b.Subs(ctx, ws, true)
	if err != nil {
		return g, err
	}
	for _, s := range subs {
		n, err := b.SubUses(ctx, s.ID)
		if err != nil {
			return g, err
		}
		g.Subdelegations = append(g.Subdelegations, subRow(s, n))
	}
	rules, err := b.LiveRules(ctx, ws)
	if err != nil {
		return g, err
	}
	for _, x := range rules {
		uses, err := b.RuleUses(ctx, x.ID)
		if err != nil {
			return g, err
		}
		g.Rules = append(g.Rules, ruleRow(x, len(uses)))
	}
	return g, nil
}

func (p *Panel) handleRevokeSub(w http.ResponseWriter, r *http.Request) {
	b, ok := p.backend(w)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		WriteError(w, errBadRequest())
		return
	}
	ctx, me := r.Context(), UserOf(r)
	slugs, err := p.actingWorkspaces(ctx, me)
	if err != nil {
		WriteError(w, errInternal())
		return
	}
	var before *decrees.Sub
	for _, ws := range slugs {
		subs, err := b.Subs(ctx, ws, true)
		if err != nil {
			WriteError(w, errInternal())
			return
		}
		for i := range subs {
			if subs[i].ID == id {
				before = &subs[i]
			}
		}
	}
	if before == nil {
		WriteError(w, Err(CodeNotFound, "api.sub_not_found", nil))
		return
	}
	switch err := b.RevokeSub(ctx, id, me.ID, true); {
	case err == nil:
	case errors.Is(err, decrees.ErrEnded):
		WriteError(w, Err(CodeDecreeEnded, "api.sub_ended", nil))
		return
	case errors.Is(err, decrees.ErrNotAllowed):
		WriteError(w, Err(CodeForbiddenLevel, "api.level_not_allowed", nil))
		return
	default:
		p.log.Error("panel: revoking a subdelegation", "err", err)
		WriteError(w, errInternal())
		return
	}
	p.audit(ctx, "panel.subdelegation.revoked", map[string]any{"user_id": me.ID, "workspace": before.Workspace, "subdelegation": id,
		"before": map[string]any{"type": before.Type, "mesa": before.Mesa, "grantee": before.Grantee, "until": before.Until.UTC(), "active": true},
		"after":  map[string]any{"active": false}, "ip": clientIP(r)})
	w.WriteHeader(http.StatusNoContent)
}

func (p *Panel) handleRevokeRule(w http.ResponseWriter, r *http.Request) {
	b, ok := p.backend(w)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		WriteError(w, errBadRequest())
		return
	}
	ctx, me := r.Context(), UserOf(r)
	before, err := b.Rule(ctx, id)
	if err != nil || !me.Acts(before.Workspace) {
		WriteError(w, Err(CodeNotFound, "api.rule_not_found", nil))
		return
	}
	switch err := b.RevokeRule(ctx, id, me.ID); {
	case err == nil:
	case errors.Is(err, tablerules.ErrEnded):
		WriteError(w, Err(CodeDecreeEnded, "api.rule_ended", nil))
		return
	case errors.Is(err, tablerules.ErrNotAllowed):
		WriteError(w, Err(CodeForbiddenLevel, "api.level_not_allowed", nil))
		return
	case errors.Is(err, tablerules.ErrNoRule):
		WriteError(w, Err(CodeNotFound, "api.rule_not_found", nil))
		return
	default:
		p.log.Error("panel: revoking a temporary rule", "err", err)
		WriteError(w, errInternal())
		return
	}
	p.audit(ctx, "panel.table_rule.revoked", map[string]any{"user_id": me.ID, "workspace": before.Workspace, "table": before.Table, "rule": id,
		"before": map[string]any{"state": string(before.State), "class": before.Class, "risk": string(before.Risk), "author": before.Author},
		"after":  map[string]any{"state": string(tablerules.Revoked)}, "ip": clientIP(r)})
	w.WriteHeader(http.StatusNoContent)
}

func (p *Panel) handleRevokeAll(w http.ResponseWriter, r *http.Request) {
	b, ok := p.backend(w)
	if !ok {
		return
	}
	ctx, me := r.Context(), UserOf(r)
	rep, err := b.RevokeAllDelegations(ctx, me.ID)
	if err != nil {
		p.decreeError(w, err)
		return
	}
	rules := 0
	for _, n := range rep.Rules {
		rules += n
	}
	numbers := make([]int64, 0, len(rep.Decrees))
	for _, d := range rep.Decrees {
		numbers = append(numbers, d.Number)
	}
	p.audit(ctx, "panel.delegations.revoked_all", map[string]any{"user_id": me.ID,
		"before": map[string]any{"delegations": numbers, "subdelegations": rep.Subdelegations, "rules": rules, "rules_by_workspace": rep.Rules},
		"after":  map[string]any{"delegations": 0, "subdelegations": 0, "rules": 0}, "ip": clientIP(r)})
	WriteJSON(w, http.StatusOK, RevokeAllResult{Delegations: len(rep.Decrees), Subdelegations: rep.Subdelegations, Rules: rules, RulesBy: rep.Rules})
}
