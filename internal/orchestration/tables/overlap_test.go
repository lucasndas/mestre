package tables_test

import (
	"slices"
	"strings"
	"testing"
)

// CA-CLI-GM-14: the base has a recent commit in src/login/ and the table of the epic
// 15 changed src/login/form.ts: the reading lists the commit of the base and the
// file under the table, and changes nothing.
func TestTableOverlapListsTheCommitsOfTheBaseAndTheFilesOfTheTablesInThePaths(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(15, "Formulário", "epico")
	tbl := w.open(15).Table

	commit(t, w.be, w.git, w.remote, "src/login/session.ts", "export const a = 1\n", "feat: login session")
	commit(t, w.be, w.git, w.remote, "src/billing/pay.ts", "export const b = 1\n", "feat: billing")
	if err := w.wss.Fetch(w.ctx, w.slug()); err != nil {
		t.Fatal(err)
	}
	repo := w.repoOf(tbl)
	commit(t, w.be, w.git, repo, "src/login/form.ts", "export const f = 1\n", "feat: form")
	commit(t, w.be, w.git, repo, "src/ui/button.ts", "export const g = 1\n", "feat: button")
	before, _ := w.svc.Get(w.ctx, w.slug(), tbl.ID)

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
	if after, _ := w.svc.Get(w.ctx, w.slug(), tbl.ID); after.UpdatedAt != before.UpdatedAt || after.State != before.State {
		t.Fatal("the reading changed the table")
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
	if !r.OK || !strings.Contains(dataOf(t, r)["text"].(string), "src/login/form.ts") {
		t.Fatalf("table overlap = %s", r.Data)
	}
	master, _, _ := w.reg.TableMaster(w.ctx, w.slug(), tbl.ID)
	if r := w.run(master.ID, "table overlap", nil); r.OK || errCode(r) != "level_denied" {
		t.Fatalf("a table master = %+v, want level_denied", r.Error)
	}
}
