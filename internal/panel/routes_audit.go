package panel

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/users"
)

func init() { RegisterArea("audit", mountAudit) }

func mountAudit(r *Router) {
	r.Require("GET /api/audit", r.Panel().handleAudit, users.ActivateClasses)
}

const (
	auditDefaultLimit = 50
	auditMaxLimit     = 200
)

// jsonField reads a field of the detail of a line; a detail that is not JSON has none.
func jsonField(field string) string {
	return `CASE WHEN json_valid(detail) THEN json_extract(detail, '$.` + field + `') END`
}

// auditDay reads a filter of time: a day (in the time zone of the Guild) or an instant.
// end says the end of the day is wanted.
func (p *Panel) auditDay(ctx *http.Request, s string, end bool) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	loc, err := time.LoadLocation(p.timezone(ctx.Context()))
	if err != nil {
		loc = time.UTC
	}
	d, err := time.ParseInLocation("2006-01-02", s, loc)
	if err != nil {
		return time.Time{}, false
	}
	if end {
		return d.AddDate(0, 0, 1), true // exclusive
	}
	return d, true
}

// handleAudit lists the audit trail with the filters who (user), what (action, by
// the beginning of its name), when (from, to) and workspace. The Owner sees every
// line; the others see only the lines of their own workspaces, and the actions of the
// Operator of the PC only as the fact that they happened.
func (p *Panel) handleAudit(w http.ResponseWriter, r *http.Request) {
	ctx, me := r.Context(), UserOf(r)
	q := r.URL.Query()
	var (
		where []string
		args  []any
	)
	if v := q.Get("user"); v != "" {
		where, args = append(where, jsonField("user_id")+" = ?"), append(args, v)
	}
	if v := q.Get("action"); v != "" {
		esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(v)
		where, args = append(where, `action LIKE ? ESCAPE '\'`), append(args, esc+"%")
	}
	if v := q.Get("from"); v != "" {
		t, ok := p.auditDay(r, v, false)
		if !ok {
			WriteError(w, errBadRequest())
			return
		}
		where, args = append(where, "at >= ?"), append(args, t.UnixMilli())
	}
	if v := q.Get("to"); v != "" {
		t, ok := p.auditDay(r, v, true)
		if !ok {
			WriteError(w, errBadRequest())
			return
		}
		where, args = append(where, "at < ?"), append(args, t.UnixMilli())
	}
	if v := q.Get("workspace"); v != "" {
		where, args = append(where, jsonField("workspace")+" = ?"), append(args, v)
	}
	if me.Level != users.Owner {
		if len(me.Workspaces) == 0 {
			WriteJSON(w, http.StatusOK, AuditPage{Entries: []AuditEntry{}})
			return
		}
		marks := strings.TrimSuffix(strings.Repeat("?,", len(me.Workspaces)), ",")
		where = append(where, jsonField("workspace")+" IN ("+marks+")")
		for _, ws := range me.Workspaces {
			args = append(args, ws)
		}
	}
	if c := q.Get("cursor"); c != "" {
		at, id, ok := strings.Cut(c, ":")
		n, err := strconv.ParseInt(at, 10, 64)
		if !ok || err != nil {
			WriteError(w, errBadRequest())
			return
		}
		where, args = append(where, "(at < ? OR (at = ? AND id < ?))"), append(args, n, n, id)
	}
	limit := auditDefaultLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			WriteError(w, errBadRequest())
			return
		}
		limit = min(n, auditMaxLimit)
	}
	sqlText := `SELECT id, at, action, detail FROM audit_log`
	if len(where) > 0 {
		sqlText += ` WHERE ` + strings.Join(where, " AND ")
	}
	sqlText += ` ORDER BY at DESC, id DESC LIMIT ?`
	names, people, err := p.userNames(r)
	if err != nil {
		WriteError(w, errInternal())
		return
	}
	slugs, err := p.workspaceSlugs(ctx)
	if err != nil {
		WriteError(w, errInternal())
		return
	}
	rows, err := p.db.Reader().QueryContext(ctx, sqlText, append(args, limit+1)...)
	if err != nil {
		p.log.Error("panel: reading the audit trail", "err", err)
		WriteError(w, errInternal())
		return
	}
	defer rows.Close()
	out := AuditPage{Entries: []AuditEntry{}, People: people, Workspaces: []string{}}
	for _, s := range slugs {
		if me.Level == users.Owner || slices.Contains(me.Workspaces, s) {
			out.Workspaces = append(out.Workspaces, s)
		}
	}
	var lastAt int64
	var lastID string
	for rows.Next() {
		var (
			e      AuditEntry
			at     int64
			detail string
		)
		if err := rows.Scan(&e.ID, &at, &e.Action, &detail); err != nil {
			WriteError(w, errInternal())
			return
		}
		if len(out.Entries) == limit { // the line past the page: there is more
			out.Next = strconv.FormatInt(lastAt, 10) + ":" + lastID
			break
		}
		lastAt, lastID = at, e.ID
		e.At = time.UnixMilli(at).UTC()
		e.Detail = map[string]any{}
		if json.Unmarshal([]byte(detail), &e.Detail) != nil || e.Detail == nil {
			e.Detail = map[string]any{}
		}
		e.UserID, _ = e.Detail["user_id"].(string)
		e.UserName = names[e.UserID]
		e.Workspace, _ = e.Detail["workspace"].(string)
		if strings.HasPrefix(e.Action, "operator.") && me.Level != users.Owner {
			e.Detail = map[string]any{} // what the Operator did is for the Owner; the others see that it happened
		}
		out.Entries = append(out.Entries, e)
	}
	if err := rows.Err(); err != nil {
		WriteError(w, errInternal())
		return
	}
	WriteJSON(w, http.StatusOK, out)
}

// userNames maps the id of every user (removed ones too: the history keeps the
// name) to the name.
func (p *Panel) userNames(r *http.Request) (map[string]string, []AuditPerson, error) {
	rows, err := p.db.Reader().QueryContext(r.Context(), `SELECT id, name FROM users ORDER BY name`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	m, people := map[string]string{}, []AuditPerson{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, nil, err
		}
		m[id] = name
		people = append(people, AuditPerson{ID: id, Name: name})
	}
	return m, people, rows.Err()
}
