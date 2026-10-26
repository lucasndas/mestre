package panel

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/users"
)

func init() { RegisterArea("chain", mountChain) }

func mountChain(r *Router) {
	p := r.Panel()
	r.Require("GET /api/chain", p.handleChain, users.Status)
	// Pausing and resuming only restrict, so they ask no PIN (docs/telegram/usuarios-e-niveis.md).
	r.Require("POST /api/chain/{workspace}/tables/{table}/pause", p.handlePause(true), users.PauseButtons)
	r.Require("POST /api/chain/{workspace}/tables/{table}/resume", p.handlePause(false), users.PauseButtons)
}

func queueRow(m chain.QueueMetric) QueueRow {
	return QueueRow{Level: string(m.Level), Table: m.Table, Open: m.Open, OldestSeconds: seconds(m.Oldest), AvgWaitSecs: seconds(m.AvgWait)}
}

// pauseInfo reads the pause of a table; who did it is named when it was a person.
func (p *Panel) pauseInfo(ctx context.Context, pause chain.Pause) PauseInfo {
	info := PauseInfo{By: pause.By, Kind: pause.Kind, Reason: pause.Reason, Since: pause.Since.UTC()}
	if id, ok := strings.CutPrefix(pause.By, "user:"); ok {
		if u, err := p.users.Get(ctx, id); err == nil {
			info.ByName = u.Name
		}
	}
	return info
}

func (p *Panel) handleChain(w http.ResponseWriter, r *http.Request) {
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
	canPause := users.Can(me.Level, users.PauseButtons)
	out := ChainStatus{Workspaces: []ChainWorkspace{}, CanPause: canPause}
	for _, ws := range slugs {
		cw, err := p.chainOf(ctx, b, ws, canPause)
		if err != nil {
			p.log.Error("panel: reading the supervision of the chain", "workspace", ws, "err", err)
			WriteError(w, errInternal())
			return
		}
		out.Workspaces = append(out.Workspaces, cw)
	}
	WriteJSON(w, http.StatusOK, out)
}

func (p *Panel) chainOf(ctx context.Context, b RulesBackend, ws string, canPause bool) (ChainWorkspace, error) {
	cw := ChainWorkspace{Workspace: ws, Queues: []QueueRow{}, Tables: []ChainTable{}}
	metrics, err := b.Metrics(ctx, ws)
	if err != nil {
		return cw, err
	}
	open, err := b.OpenTables(ctx, ws)
	if err != nil {
		return cw, err
	}
	byTable := map[string][]QueueRow{}
	for _, m := range metrics {
		if m.Table == "" {
			cw.Queues = append(cw.Queues, queueRow(m))
			continue
		}
		byTable[m.Table] = append(byTable[m.Table], queueRow(m))
		if !slices.Contains(open, m.Table) {
			open = append(open, m.Table)
		}
	}
	slices.Sort(open)
	for _, t := range open {
		row := ChainTable{Table: t, Queues: byTable[t]}
		if row.Queues == nil {
			row.Queues = []QueueRow{}
		}
		pause, paused, err := b.PauseOf(ctx, ws, t)
		if err != nil {
			return cw, err
		}
		if paused {
			info := p.pauseInfo(ctx, pause)
			row.Pause = &info
			// the pause of the Grand Master is resumed by the Grand Master alone
			row.CanResume = canPause && pause.Kind != "character"
		}
		cw.Tables = append(cw.Tables, row)
	}
	return cw, nil
}

func (p *Panel) handlePause(pause bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, ok := p.backend(w)
		if !ok {
			return
		}
		ws, table := r.PathValue("workspace"), r.PathValue("table")
		if !p.workspaceOf(w, r, ws) {
			return
		}
		var in PauseInput
		if !decode(w, r, &in) {
			return
		}
		ctx, me := r.Context(), UserOf(r)
		reason := strings.TrimSpace(in.Reason)
		if reason == "" {
			WriteError(w, Err(CodeReasonRequired, "diga o motivo", nil))
			return
		}
		req := chain.PauseRequest{Workspace: ws, Table: table, Reason: reason,
			By: chain.Actor{User: me.ID, UserLevel: characters.UserLevel(me.Level)},
			// the pause of the Grand Master is resumed by the Grand Master alone; the
			// supervisor checks it under its lock, so a pause that changes hands cannot slip by
			RefuseCharacterPause: true}
		before, _, err := b.PauseOf(ctx, ws, table)
		if err != nil {
			p.log.Error("panel: reading the pause of a table", "err", err)
			WriteError(w, errInternal())
			return
		}
		var changed bool
		if pause {
			changed, err = b.PauseAutonomy(ctx, req)
		} else {
			changed, err = b.ResumeAutonomy(ctx, req)
		}
		switch {
		case err == nil:
		case errors.Is(err, chain.ErrCharacterPause):
			WriteError(w, Err(CodeGrandMasterOnly, "a pausa do Grão-Mestre só ele retoma", nil))
			return
		case errors.Is(err, chain.ErrLevelDenied):
			WriteError(w, Err(CodeForbiddenLevel, "ação não permitida a este nível", nil))
			return
		case errors.Is(err, chain.ErrReasonRequired):
			WriteError(w, Err(CodeReasonRequired, "diga o motivo", nil))
			return
		case errors.Is(err, chain.ErrTableOver):
			WriteError(w, Err(CodeTableOver, "a mesa já acabou", nil))
			return
		case errors.Is(err, chain.ErrNoTable):
			WriteError(w, Err(CodeNoTable, "mesa não encontrada", nil))
			return
		default:
			p.log.Error("panel: the pause of the autonomy", "err", err)
			WriteError(w, errInternal())
			return
		}
		if changed { // pausing a paused table, or resuming one that is not, changes nothing
			action, from, to := "panel.chain.paused", map[string]any{"paused": false}, map[string]any{"paused": true, "reason": reason}
			if !pause {
				action, from, to = "panel.chain.resumed", map[string]any{"paused": true, "by": before.By, "reason": before.Reason}, map[string]any{"paused": false, "reason": reason}
			}
			p.audit(ctx, action, map[string]any{"user_id": me.ID, "workspace": ws, "table": table, "before": from, "after": to, "ip": clientIP(r)})
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
