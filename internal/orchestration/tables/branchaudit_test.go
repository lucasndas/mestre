package tables_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/tables"
)

// auditedTips are the records of the tip of a branch in the audit trail, as
// (table, branch, sha) lines.
func auditedTips(t *testing.T, w *world) [][3]string {
	t.Helper()
	rows, err := w.db.Reader().QueryContext(context.Background(), `SELECT detail FROM audit_log WHERE action = ? ORDER BY at, id`, tables.BranchDeleteAction)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][3]string
	for rows.Next() {
		var detail string
		if err := rows.Scan(&detail); err != nil {
			t.Fatal(err)
		}
		var d struct{ Table, Branch, SHA string }
		if err := json.Unmarshal([]byte(detail), &d); err != nil {
			t.Fatal(err)
		}
		out = append(out, [3]string{d.Table, d.Branch, d.SHA})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// refusesAudit is an audit trail that takes no line.
func refusesAudit(context.Context, string, time.Time, string, string, []byte) error {
	return errors.New("the audit trail is not writable")
}

// CA-MESAS-11
func TestTheTipOfTheBranchIsInTheAuditBeforeGitIsAskedToDeleteItAndWithoutTheRecordItIsNotAsked(t *testing.T) {
	t.Parallel()
	t.Run("closing", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, worldOpts{})
		w.activate()
		tbl, head := reviewed(t, w, 42, "Login")
		var atDelete [][3]string
		w.spy.onDelete = func(string) { atDelete = auditedTips(t, w) }
		if res, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID}); err != nil || res.Table.State != tables.StateClosed {
			t.Fatalf("Close = %+v, %v", res, err)
		}
		if want := [][3]string{{tbl.ID, tbl.Branch, string(head)}}; len(atDelete) != 1 || atDelete[0] != want[0] {
			t.Fatalf("the audit when git was asked to delete the branch = %v; want %v", atDelete, want)
		}
		if _, deleted := w.spy.asked(); len(deleted) != 1 || deleted[0] != tbl.Branch {
			t.Errorf("git was asked to delete %v; want %s", deleted, tbl.Branch)
		}
	})
	t.Run("closing without the record", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, worldOpts{audit: refusesAudit})
		w.activate()
		tbl, _ := reviewed(t, w, 42, "Login")
		if _, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID}); err == nil {
			t.Fatal("the table closed although the tip of the branch could not be recorded")
		}
		if _, deleted := w.spy.asked(); len(deleted) != 0 {
			t.Errorf("git was asked to delete %v with no record of the tip", deleted)
		}
		if _, err := w.mirror().ResolveRef(w.ctx, "refs/heads/"+tbl.Branch); err != nil {
			t.Errorf("the branch left the mirror with no record: %v", err)
		}
	})
	t.Run("cancelled for thirty days", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, worldOpts{})
		w.activate()
		w.gh.add(42, "Login", "epico")
		tbl := w.open(42).Table
		w.dest.allowed = true
		if _, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID, Cancel: true, Reason: "o usuário desistiu"}); err != nil {
			t.Fatal(err)
		}
		tip, err := w.mirror().ResolveRef(w.ctx, "refs/heads/"+tbl.Branch)
		if err != nil {
			t.Fatal(err)
		}
		var atDelete [][3]string
		w.spy.onDelete = func(string) { atDelete = auditedTips(t, w) }
		w.clk.Set(day0.Add(30 * 24 * time.Hour))
		if err := w.svc.Sweep(w.ctx); err != nil {
			t.Fatal(err)
		}
		if len(atDelete) != 1 || atDelete[0] != [3]string{tbl.ID, tbl.Branch, string(tip)} {
			t.Fatalf("the audit when git was asked to delete the branch = %v; want (%s, %s, %s)", atDelete, tbl.ID, tbl.Branch, tip)
		}
	})
	t.Run("cancelled without the record", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, worldOpts{audit: refusesAudit})
		w.activate()
		w.gh.add(42, "Login", "epico")
		tbl := w.open(42).Table
		w.dest.allowed = true
		if _, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID, Cancel: true, Reason: "o usuário desistiu"}); err != nil {
			t.Fatal(err)
		}
		w.clk.Set(day0.Add(30 * 24 * time.Hour))
		if err := w.svc.Sweep(w.ctx); err == nil {
			t.Fatal("the sweep went on although the tip of the branch could not be recorded")
		}
		if _, deleted := w.spy.asked(); len(deleted) != 0 {
			t.Errorf("git was asked to delete %v with no record of the tip", deleted)
		}
	})
}
