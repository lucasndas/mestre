package panel

import (
	"context"
	"database/sql"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/mestre-ia/mestre/internal/orchestration/usage"
	"github.com/mestre-ia/mestre/internal/users"
)

func init() { RegisterArea("usage", mountUsage) }

const (
	usageDefaultDays = 14
	usageMaxDays     = 366
	usageRows        = 50
)

func mountUsage(r *Router) {
	// read-only: whoever reads the panel sees the usage of the workspaces they act in
	r.Require("GET /api/usage", r.Panel().handleUsage, users.Status)
}

// usageScope narrows the rows to the workspaces of the user (the Owner: none), by the
// column of the workspace.
type usageScope struct {
	all  bool
	list []string
}

func (s usageScope) where(col string) (string, []any) {
	if s.all {
		return "1 = 1", nil
	}
	if len(s.list) == 0 {
		return "0 = 1", nil
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(s.list)), ",")
	args := make([]any, len(s.list))
	for i, w := range s.list {
		args[i] = w
	}
	return col + " IN (" + marks + ")", args
}

const usageSum = `COALESCE(SUM(cost_usd), 0), COUNT(cost_usd),
	COALESCE(SUM(CASE WHEN has_tokens = 1 THEN input_tokens + output_tokens + cache_read_tokens + cache_write_tokens + reasoning_tokens ELSE 0 END), 0),
	COALESCE(SUM(has_tokens), 0), COALESCE(SUM(active_ms), 0), COALESCE(SUM(turns), 0)`

// usageGroup runs one grouping: key and label are SQL expressions, from is the table
// (with its joins), where the condition. The rows come biggest cost first.
func (p *Panel) usageGroup(ctx context.Context, keyExpr, labelExpr, wsExpr, from, where string, args []any, orderDay bool) ([]UsageTotals, error) {
	order := "ORDER BY COALESCE(SUM(cost_usd), 0) DESC, SUM(active_ms) DESC"
	limit := " LIMIT " + strconv.Itoa(usageRows)
	if orderDay {
		order, limit = "ORDER BY k DESC", ""
	}
	rows, err := p.db.Reader().QueryContext(ctx, `SELECT `+keyExpr+` AS k, `+labelExpr+`, `+wsExpr+`, `+usageSum+` FROM `+from+` WHERE `+where+` GROUP BY k `+order+limit, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UsageTotals{}
	for rows.Next() {
		var (
			t              UsageTotals
			cost           float64
			costN, tokensN int64
			tokens, active int64
			turns          int64
			label, ws      sql.NullString
		)
		if err := rows.Scan(&t.Key, &label, &ws, &cost, &costN, &tokens, &tokensN, &active, &turns); err != nil {
			return nil, err
		}
		t.Label, t.Workspace = label.String, ws.String
		if costN > 0 {
			t.CostUSD = &cost
		}
		if tokensN > 0 {
			t.Tokens = &tokens
		}
		t.ActiveSeconds, t.Turns = active/1000, turns
		if t.Label == "" {
			t.Label = t.Key
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// handleUsage shows the use per day, workspace, table, quest, character and account,
// with the limits set and what is left of each. The rows of the steps are the work of
// the tables; the conformance and the test of classes count only in the account and in
// the day, which only the Owner sees.
// Part of CA-USO-08 (the panel side): the use of a table and of each character.
func (p *Panel) handleUsage(w http.ResponseWriter, r *http.Request) {
	ctx, me := r.Context(), UserOf(r)
	days := usageDefaultDays
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			WriteError(w, errBadRequest())
			return
		}
		days = min(n, usageMaxDays)
	}
	list, all := actingIn(me)
	sc := usageScope{all: all, list: list}
	since := p.clk.Now().AddDate(0, 0, -days).UTC().UnixMilli()
	page := UsagePage{Days: []UsageTotals{}, Workspaces: []UsageTotals{}, Tables: []UsageTotals{}, Quests: []UsageTotals{}, Characters: []UsageTotals{}, Accounts: []UsageTotals{}, Limits: []UsageLimit{}}
	if src := p.mg().Usage; src != nil {
		page.Today = src.Today(ctx)
	}
	ws, wsArgs := sc.where("u.workspace")
	base := "at >= ? AND " + ws
	args := append([]any{since}, wsArgs...)
	step := base + " AND origin = 'etapa'"
	var err error
	run := func(dst *[]UsageTotals, key, label, wsx, from, where string, order bool) {
		if err != nil {
			return
		}
		*dst, err = p.usageGroup(ctx, key, label, wsx, from, where, args, order)
	}
	run(&page.Days, "u.day", "u.day", "''", "usage_records u", base, true)
	run(&page.Workspaces, "u.workspace", "u.workspace", "u.workspace", "usage_records u", step, false)
	run(&page.Tables, "u.workspace || '/' || u.table_id", "u.workspace || '/' || u.table_id", "u.workspace", "usage_records u", step+" AND u.table_id <> ''", false)
	run(&page.Quests, "u.quest", "COALESCE(MAX(q.name), u.quest)", "u.workspace", "usage_records u LEFT JOIN quests q ON q.id = u.quest", step+" AND u.quest <> ''", false)
	run(&page.Characters, "u.character_id", "COALESCE(MAX(c.name), u.character_id)", "u.workspace", "usage_records u LEFT JOIN characters c ON c.id = u.character_id", step+" AND u.character_id <> ''", false)
	if err == nil && all {
		// the accounts and the conformance have no workspace: only the Owner's view holds them
		// mestre:allow pt-text: the label of the default profile in the grouping of the usage, a name of the data
		page.Accounts, err = p.usageGroup(ctx, "u.provider || '/' || CASE WHEN u.profile = '' THEN 'padrão' ELSE u.profile END",
			// mestre:allow pt-text: the label of the default profile in the grouping of the usage, a name of the data
			"u.provider || ' · ' || CASE WHEN u.profile = '' THEN 'padrão' ELSE u.profile END", "''", "usage_records u", "at >= ?", []any{since}, false)
	}
	if err == nil {
		page.Limits, err = p.usageLimits(ctx, sc)
	}
	if err != nil {
		p.log.Error("panel: the usage", "err", err)
		WriteError(w, errInternal())
		return
	}
	WriteJSON(w, http.StatusOK, page)
}

// usageLimits are the limits the user sees, each with what is used and what is left.
func (p *Panel) usageLimits(ctx context.Context, sc usageScope) ([]UsageLimit, error) {
	src := p.mg().Usage
	out := []UsageLimit{}
	if src == nil {
		return out, nil
	}
	all, err := src.Limits(ctx)
	if err != nil {
		return nil, err
	}
	for _, l := range all {
		ws := p.limitWorkspace(ctx, l)
		if l.Scope == usage.ScopeDayTotal || ws == "" {
			// the total of the day covers the installation: the Owner's
			if !sc.all {
				continue
			}
		} else if !sc.all && !slices.Contains(sc.list, ws) {
			continue
		}
		v, err := src.View(ctx, l, ws)
		if err != nil {
			return nil, err
		}
		left := max(v.Effective-v.Used, 0)
		out = append(out, UsageLimit{Scope: string(l.Scope), Key: l.Key, Unit: string(l.Unit), Value: l.Value, Effective: v.Effective, Used: v.Used,
			Left: left, Percent: v.Percent, Period: v.Period, HasData: v.HasData})
	}
	return out, nil
}

// limitWorkspace is the workspace a limit is about ("" for the total of the day).
func (p *Panel) limitWorkspace(ctx context.Context, l usage.Limit) string {
	switch l.Scope {
	case usage.ScopeWorkspace, usage.ScopeDay:
		return l.Key
	case usage.ScopeTable:
		ws, _, _ := strings.Cut(l.Key, "/")
		return ws
	case usage.ScopeQuest:
		var ws string
		if err := p.db.Reader().QueryRowContext(ctx, `SELECT workspace FROM quests WHERE id = ?`, l.Key).Scan(&ws); err != nil {
			return ""
		}
		return ws
	}
	return ""
}
