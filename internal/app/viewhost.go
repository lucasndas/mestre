package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/canvas"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/quests"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
	"github.com/mestre-ia/mestre/internal/orchestration/usage"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// viewHost is the panel.ViewSource over the orchestration of the Citadel that
// acts: the blocks of the workspace view are read from the same services the
// CLI and the canvas use (docs/painel/visao-do-workspace.md).
type viewHost struct {
	// get gives the orchestration of the period (nil: this machine hosts none).
	get func() *Orchestration
	clk clock.Clock
}

var _ panel.ViewSource = viewHost{}

func (h viewHost) orc() (*Orchestration, error) {
	if o := h.get(); o != nil {
		return o, nil
	}
	return nil, panel.ErrWorkspaceNotFound // this machine hosts no orchestration
}

// layout is where the view of a workspace keeps its elements: the canvas items of
// the workspace itself (table ""), with the keys "gm" and "table:<id>".
func (h viewHost) layout(o *Orchestration) *canvas.Store {
	return &canvas.Store{DB: o.Registry.DB, Clock: h.clk}
}

// View implements panel.ViewSource.
func (h viewHost) View(ctx context.Context, workspace string) (panel.ViewData, error) {
	out := panel.ViewData{Tables: []panel.TableBlock{}, Layout: []panel.CanvasItem{}}
	o, err := h.orc()
	if err != nil {
		return out, nil // no orchestration here: the workspace shows with no tables
	}
	names := canvasHost(h).machineNames(ctx, o)
	machineName := func(id string) string {
		if n := names[id]; n != "" {
			return n
		}
		return id
	}
	alive, err := o.Registry.AliveOf(ctx, workspace)
	if err != nil {
		return out, err
	}
	party := map[string]map[string]int{}
	size := map[string]int{}
	for _, c := range alive {
		if c.Role == characters.RoleGrandMaster {
			out.GrandMaster = &panel.ViewGrandMaster{ID: c.ID, Name: c.Name, State: string(c.State), Machine: c.Machine, MachineName: machineName(c.Machine), HasTerminal: c.TerminalID != ""}
			continue
		}
		if c.Table == "" {
			continue
		}
		if party[c.Table] == nil {
			party[c.Table] = map[string]int{}
		}
		party[c.Table][c.Machine]++
		size[c.Table]++
	}

	ts, err := o.Tables.List(ctx, workspace)
	if err != nil {
		return out, err
	}
	limits := map[string]usage.Limit{}
	if o.Usage != nil {
		all, err := o.Usage.Limits(ctx)
		if err != nil {
			return out, err
		}
		for _, l := range all {
			if l.Scope == usage.ScopeTable {
				limits[l.Key] = l
			}
		}
	}
	now := h.clk.Now()
	for _, t := range ts {
		b, err := h.block(ctx, o, t, now, limits)
		if err != nil {
			return out, err
		}
		for m, n := range party[t.ID] {
			b.Party = append(b.Party, panel.ViewMachine{Machine: m, MachineName: machineName(m), Characters: n})
		}
		sort.Slice(b.Party, func(i, j int) bool { return b.Party[i].MachineName < b.Party[j].MachineName })
		b.PartySize = size[t.ID]
		out.Tables = append(out.Tables, b)
	}
	items, err := h.layout(o).Items(ctx, workspace, "")
	if err != nil {
		return out, err
	}
	for _, it := range items {
		out.Layout = append(out.Layout, panel.CanvasItem{Key: it.Key, X: it.X, Y: it.Y})
	}
	return out, nil
}

// block is one table as its block shows it, but for the party.
func (h viewHost) block(ctx context.Context, o *Orchestration, t tables.Table, now time.Time, limits map[string]usage.Limit) (panel.TableBlock, error) {
	b := panel.TableBlock{ID: t.ID, Epic: t.Epic, EpicTitle: t.EpicTitle, Branch: t.Branch, State: string(t.State), Paused: t.Paused, PR: t.PRNumber,
		Party: []panel.ViewMachine{}, Waits: []panel.ViewWait{}}
	if t.Epic <= 0 {
		b.IssueWarning = "a issue do épico não foi criada no GitHub"
	}
	over := tables.Over(t.State)

	// the mission in progress, its step, its criteria and the failure that repeats
	var cur *quests.Quest
	qs, err := o.Quests.List(ctx, t.Workspace, t.ID)
	if err != nil {
		return b, err
	}
	cur = currentQuest(qs)
	if cur != nil {
		vq := &panel.ViewQuest{ID: cur.ID, Name: cur.Name, Issue: cur.Issue, State: string(cur.State)}
		v, err := o.Quests.Status(ctx, *cur)
		if err != nil {
			return b, err
		}
		for _, s := range v.Steps {
			if s.State == quests.StepPreparing || s.State.Active() {
				if vq.Step == "" {
					vq.Step = s.Def.Title()
				}
				if s.Waiting != "" && !over {
					kind := panel.WaitOther
					if strings.HasPrefix(s.Waiting, "aprovação") {
						kind = panel.WaitApproval
					}
					b.Waits = append(b.Waits, panel.ViewWait{Kind: kind, Reason: s.Waiting})
				}
			}
		}
		b.Quest = vq
		p, err := o.Quests.Progress(ctx, *cur)
		if err != nil {
			return b, err
		}
		if p.Criteria > 0 {
			b.Criteria = &panel.ViewCriteria{Total: p.Criteria, Proven: p.Proven, Verified: p.Verified}
		}
		if cur.State == quests.Paused {
			w := panel.ViewWait{Kind: panel.WaitOther, Reason: cur.PausedReason}
			if cur.PausedReason == quests.ReasonSameFailure {
				w.Kind, w.Fingerprint = panel.WaitSameFailure, p.Failure.Fingerprint
				if len(p.Failure.Places) > 0 {
					w.Where = p.Failure.Places[0]
				}
			}
			b.Waits = append(b.Waits, w)
		}
	}

	// the waits of the table itself
	if !over {
		if t.Paused {
			kind := panel.WaitPaused
			if t.PausedBy == "usage_limit" {
				kind = panel.WaitQuota
			}
			b.Waits = append([]panel.ViewWait{{Kind: kind, Reason: t.PausedReason}}, b.Waits...)
		}
		held, err := o.Tables.Held(ctx, t.Workspace, t.ID)
		if err != nil {
			return b, err
		}
		if held {
			b.Waits = append(b.Waits, panel.ViewWait{Kind: panel.WaitTable, Reason: "aguarda o merge de outra mesa"})
		}
		if t.State == tables.StateWorking && !t.Paused && !held {
			d, err := o.Tables.LastDelivery(ctx, t.Workspace, t.ID)
			if err != nil {
				return b, err
			}
			since := d
			if t.UpdatedAt.After(since) {
				since = t.UpdatedAt
			}
			if quiet := now.Sub(since); quiet >= chain.DefaultStallAfter {
				b.Stalled, b.QuietSeconds = true, int64(quiet.Seconds())
			}
		}
	}
	if d, err := o.Tables.LastDelivery(ctx, t.Workspace, t.ID); err == nil && !d.IsZero() {
		b.LastDelivery = &d
	}

	if l, ok := limits[usage.TableKey(t.Workspace, t.ID)]; ok {
		v, err := o.Usage.View(ctx, l, t.Workspace)
		if err != nil {
			return b, err
		}
		b.Usage = &panel.ViewUsage{Used: v.Used, Limit: v.Effective, Unit: string(l.Unit), Percent: v.Percent, HasData: v.HasData}
	}
	return b, nil
}

// currentQuest is the mission the block talks about: the one running, else the
// one paused, else the last one that ended or began.
func currentQuest(qs []quests.Quest) *quests.Quest {
	rank := func(s quests.State) int {
		switch s {
		case quests.Running:
			return 3
		case quests.Paused:
			return 2
		case quests.Completed:
			return 1
		}
		return 0
	}
	var best *quests.Quest
	for i := range qs {
		q := &qs[i]
		if rank(q.State) == 0 {
			continue
		}
		if best == nil || rank(q.State) > rank(best.State) || (rank(q.State) == rank(best.State) && q.UpdatedAt.After(best.UpdatedAt)) {
			best = q
		}
	}
	return best
}

// SaveLayout implements panel.ViewSource.
func (h viewHost) SaveLayout(ctx context.Context, workspace string, items []panel.CanvasItem) error {
	o, err := h.orc()
	if err != nil {
		return err
	}
	list := make([]canvas.Item, len(items))
	for i, it := range items {
		list[i] = canvas.Item{Key: it.Key, X: it.X, Y: it.Y}
	}
	return h.layout(o).Put(ctx, workspace, "", list)
}

// PauseTable implements panel.ViewSource: the supervision pauses the table
// itself (nothing is interrupted, no PIN: it only restricts) and checks the
// level of the user again.
func (h viewHost) PauseTable(ctx context.Context, by users.User, workspace, table string) (bool, error) {
	o, err := h.orc()
	if err != nil {
		return false, err
	}
	if o.Supervisor == nil {
		return false, &panel.Refusal{Reason: "unavailable", Message: "a pausa de mesa não está disponível agora"}
	}
	if _, err := o.Tables.Get(ctx, workspace, table); err != nil {
		return false, panel.ErrCanvasNotFound
	}
	changed, err := o.Supervisor.PauseStalled(ctx, chain.PauseRequest{Workspace: workspace, Table: table,
		Reason: "pausada pelo painel por " + by.Name,
		By:     chain.Actor{User: by.ID, UserLevel: characters.UserLevel(by.Level)}})
	switch {
	case errors.Is(err, chain.ErrLevelDenied):
		return false, &panel.Refusal{Reason: "level_denied", Message: err.Error()}
	case errors.Is(err, chain.ErrTableOver):
		return false, &panel.Refusal{Reason: "table_over", Message: fmt.Sprintf("a mesa %s já terminou", table)}
	}
	return changed, err
}
