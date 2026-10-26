package panel_test

import (
	"net/url"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
)

func (r *rig) putAudit(at time.Time, action, detail string) {
	r.t.Helper()
	if _, err := r.db.Writer().ExecContext(r.ctx, `INSERT INTO audit_log (id, at, machine_id, action, detail) VALUES (?, ?, 'm1', ?, ?)`,
		at.Format(time.RFC3339Nano)+action+detail, at.UnixMilli(), action, detail); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) audit(tok string, q url.Values) panel.AuditPage {
	r.t.Helper()
	w := r.call("GET", "/api/audit?"+q.Encode(), nil, opts{cookie: tok})
	if w.Code != 200 {
		r.t.Fatalf("audit = %d %s", w.Code, w.Body)
	}
	return decodeInto[panel.AuditPage](r.t, w)
}

func actions(p panel.AuditPage) []string {
	var out []string
	for _, e := range p.Entries {
		out = append(out, e.Action)
	}
	return out
}

func TestTheAuditTrailFiltersByWhoWhatWhenAndWorkspace(t *testing.T) {
	t.Parallel()
	a := newAdmin(t)
	tok := a.ownerSession()
	bruno, _ := a.member("Bruno", "bruno", users.Approver)
	day1 := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	a.putAudit(day1, "decree.created", `{"user_id":"`+bruno.ID+`","workspace":"loja"}`)
	a.putAudit(day2, "decree.revoked", `{"user_id":"`+a.owner.ID+`","workspace":"outra"}`)
	a.putAudit(day2.Add(time.Hour), "citadel.promoted", `not json at all`)

	all := a.audit(tok, url.Values{"action": {"decree."}})
	if got := actions(all); len(got) != 2 || got[0] != "decree.revoked" {
		t.Fatalf("by what: %v (newest first)", got)
	}
	if e := all.Entries[1]; e.UserName != "Bruno" || e.UserID != bruno.ID || e.Workspace != "loja" || e.Detail["workspace"] != "loja" {
		t.Fatalf("a line = %+v", e)
	}
	if got := actions(a.audit(tok, url.Values{"user": {bruno.ID}, "action": {"decree."}})); len(got) != 1 || got[0] != "decree.created" {
		t.Fatalf("by who: %v", got)
	}
	if got := actions(a.audit(tok, url.Values{"workspace": {"outra"}})); len(got) != 1 || got[0] != "decree.revoked" {
		t.Fatalf("by workspace: %v", got)
	}
	// by day, in the time zone of the Guild: the 21st alone has nothing; the 22nd has two lines
	if got := actions(a.audit(tok, url.Values{"from": {"2026-10-21"}, "to": {"2026-10-21"}})); len(got) != 0 {
		t.Fatalf("by the 21st: %v", got)
	}
	if got := actions(a.audit(tok, url.Values{"from": {"2026-10-22"}, "to": {"2026-10-22"}})); len(got) != 2 {
		t.Fatalf("by the 22nd: %v", got)
	}
	// a pattern in the filter is only text
	if got := actions(a.audit(tok, url.Values{"action": {"%"}})); len(got) != 0 {
		t.Fatalf("a %% in the filter matched: %v", got)
	}
	a.wantErr(a.call("GET", "/api/audit?from=ontem", nil, opts{cookie: tok}), 400, panel.CodeBadRequest)
}

func TestTheAuditTrailPagesAndOnlyShowsWhatTheLevelMaySee(t *testing.T) {
	t.Parallel()
	a := newAdmin(t)
	tok := a.ownerSession()
	adm, admTok := a.member("Bruno", "bruno", users.Administrator)
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for i := range 5 {
		a.putAudit(base.Add(time.Duration(i)*time.Minute), "decree.created", `{"user_id":"`+adm.ID+`","workspace":"loja"}`)
	}
	a.putAudit(base, "decree.created", `{"workspace":"outra"}`)
	a.putAudit(base, "operator.action", `{"workspace":"loja","screenshot":"x"}`)
	p1 := a.audit(tok, url.Values{"action": {"decree."}, "limit": {"4"}})
	if len(p1.Entries) != 4 || p1.Next == "" {
		t.Fatalf("first page = %d entries, next %q", len(p1.Entries), p1.Next)
	}
	p2 := a.audit(tok, url.Values{"action": {"decree."}, "limit": {"4"}, "cursor": {p1.Next}})
	if len(p2.Entries) != 2 || p2.Next != "" || p2.Entries[0].ID == p1.Entries[3].ID {
		t.Fatalf("second page = %d entries, next %q", len(p2.Entries), p2.Next)
	}
	// an Administrator sees the lines of his workspaces, and of the Operator only the fact
	got := a.audit(admTok, url.Values{"limit": {"200"}})
	for _, e := range got.Entries {
		if e.Workspace != "loja" {
			t.Errorf("an Administrator of loja sees a line of %q: %+v", e.Workspace, e)
		}
		if e.Action == "operator.action" && len(e.Detail) != 0 {
			t.Errorf("an Administrator sees what the Operator did: %v", e.Detail)
		}
	}
	if len(got.Entries) != 6 {
		t.Errorf("the Administrator sees %d lines, want the 5 and the one of the Operator", len(got.Entries))
	}
	_, obsTok := a.member("Olga", "olga", users.Observer)
	a.wantErr(a.call("GET", "/api/audit", nil, opts{cookie: obsTok}), 403, panel.CodeForbiddenLevel)
}
