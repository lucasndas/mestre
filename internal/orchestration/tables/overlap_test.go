package tables_test

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/gitx"
)

// overlapSnapshot is everything the reading must leave as it was: the refs of
// the mirror, the HEAD and the status of the worktree of the table, the table and the
// number of rows of every table of the database.
type overlapSnapshot struct {
	mirrorRefs []gitx.Ref
	head       gitx.Hash
	dirty      []gitx.StatusEntry
	rows       map[string]int
}

func (w *world) overlapSnapshot(repo gitx.Repo) overlapSnapshot {
	w.t.Helper()
	var s overlapSnapshot
	var err error
	if s.mirrorRefs, err = w.mirror().ListRefs(w.ctx); err != nil {
		w.t.Fatal(err)
	}
	if s.head, err = repo.ResolveRef(w.ctx, "HEAD"); err != nil {
		w.t.Fatal(err)
	}
	if s.dirty, err = repo.Status(w.ctx); err != nil {
		w.t.Fatal(err)
	}
	rows, err := w.db.Reader().QueryContext(w.ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		w.t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			w.t.Fatal(err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		w.t.Fatal(err)
	}
	_ = rows.Close()
	if len(names) == 0 {
		w.t.Fatal("the database has no table to snapshot")
	}
	s.rows = map[string]int{}
	for _, n := range names {
		var c int
		if err := w.db.Reader().QueryRowContext(w.ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %q`, n)).Scan(&c); err != nil {
			w.t.Fatal(err)
		}
		s.rows[n] = c
	}
	return s
}

// CA-CLI-GM-14: the base has a recent commit in src/login/ and the table of the epic
// 15 changed src/login/form.ts: the reading lists the commit of the base and the
// file under the table, and changes nothing.
func TestTableOverlapListsTheCommitsOfTheBaseAndTheFilesOfTheTablesInThePaths(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(15, "Formulário", "epico")
	tbl := w.open(15).Table

	baseCommit := commit(t, w.be, w.git, w.remote, "src/login/session.ts", "export const a = 1\n", "feat: login session")
	commit(t, w.be, w.git, w.remote, "src/billing/pay.ts", "export const b = 1\n", "feat: billing")
	if err := w.wss.Fetch(w.ctx, w.slug()); err != nil {
		t.Fatal(err)
	}
	repo := w.repoOf(tbl)
	commit(t, w.be, w.git, repo, "src/login/form.ts", "export const f = 1\n", "feat: form")
	commit(t, w.be, w.git, repo, "src/ui/button.ts", "export const g = 1\n", "feat: button")
	before, _ := w.svc.Get(w.ctx, w.slug(), tbl.ID)
	snapBefore := w.overlapSnapshot(repo)

	res, err := w.svc.Overlap(w.ctx, w.slug(), []string{"src/login/**"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Commits) != 1 || res.Commits[0].Subject != "feat: login session" || !slices.Equal(res.Commits[0].Files, []string{"src/login/session.ts"}) {
		t.Fatalf("commits = %+v, want only the one that touched src/login/", res.Commits)
	}
	if len(res.Tables) != 1 || res.Tables[0].Table != tbl.ID || res.Tables[0].Epic != 15 || !slices.Equal(res.Tables[0].Files, []string{"src/login/form.ts"}) {
		t.Fatalf("tables = %+v, want the table of the epic 15 with src/login/form.ts only", res.Tables)
	}
	if res.Commits[0].Hash != string(baseCommit) {
		t.Errorf("the commit of the base = %s, want %s", res.Commits[0].Hash, baseCommit)
	}

	// no path: every file; a path nobody touches: nothing
	all, _ := w.svc.Overlap(w.ctx, w.slug(), nil)
	if len(all.Commits) < 2 || len(all.Tables) != 1 || len(all.Tables[0].Files) != 2 {
		t.Fatalf("with no path: %+v", all)
	}
	none, _ := w.svc.Overlap(w.ctx, w.slug(), []string{"docs/**"})
	if len(none.Commits) != 0 || len(none.Tables) != 0 {
		t.Fatalf("a path nobody touches: %+v", none)
	}

	r := w.run(w.gm.ID, "table overlap", map[string]any{"path": []any{"src/login/**"}})
	if !r.OK {
		t.Fatalf("table overlap = %s", r.Data)
	}
	data := dataOf(t, r)
	text, _ := data["text"].(string)
	commitLine := fmt.Sprintf("- %.8s feat: login session (src/login/session.ts)", baseCommit)
	tableLine := fmt.Sprintf("Mesa %s (épico #15): src/login/form.ts", tbl.ID)
	ci, ti := strings.Index(text, commitLine), strings.Index(text, tableLine)
	if ci < 0 || ti < 0 || ci > ti {
		t.Fatalf("the output = %q, want the commit line %q and then, under the table, %q", text, commitLine, tableLine)
	}
	if strings.Contains(text[:ti], "src/login/form.ts") || strings.Contains(text[ti:], "session.ts") || strings.Contains(text, "button") || strings.Contains(text, "billing") {
		t.Errorf("the output = %q mixes the commit of the base with the file of the table or lists a file outside the path", text)
	}
	commits, _ := data["commits"].([]any)
	tbls, _ := data["tables"].([]any)
	if data["base"] != "main" || len(commits) != 1 || commits[0].(map[string]any)["hash"] != string(baseCommit) ||
		len(tbls) != 1 || tbls[0].(map[string]any)["name"] != tbl.ID || tbls[0].(map[string]any)["epic"] != float64(15) {
		t.Errorf("the data = %+v, want the commit %s of main and the table %s of the epic 15", data, baseCommit, tbl.ID)
	}
	// nothing was changed in the repository (mirror refs, HEAD and worktree of the table) nor in the database
	if after, _ := w.svc.Get(w.ctx, w.slug(), tbl.ID); !reflect.DeepEqual(after, before) {
		t.Errorf("the reading changed the table: %+v -> %+v", before, after)
	}
	if snapAfter := w.overlapSnapshot(repo); !reflect.DeepEqual(snapAfter, snapBefore) {
		t.Errorf("the reading changed the repository or the database:\nbefore %+v\nafter  %+v", snapBefore, snapAfter)
	}
	master, _, _ := w.reg.TableMaster(w.ctx, w.slug(), tbl.ID)
	if r := w.run(master.ID, "table overlap", nil); r.OK || errCode(r) != "level_denied" {
		t.Fatalf("a table master = %+v, want level_denied", r.Error)
	}
}
