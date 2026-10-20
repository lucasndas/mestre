package tables_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/tables"
)

// CA-CLI-GM-13: with two open tables, `table status --json` has for each the title
// and the criteria of the epic, its open issues, the state, the size and the CI of
// the pull request and the folders the branch changes; a table that is over does
// not appear, and one table by its name or by the number of its epic has the same.
func TestTableStatusOfTheOpenTablesHasTheEpicTheIssuesThePullRequestAndTheFolders(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(12, "Login", "epico")
	w.gh.add(15, "Relatórios", "epico")
	w.gh.add(20, "Velho", "epico")
	w.gh.setBody(12, "Texto.\n\n## Critérios\n\n- [ ] Dado A, quando B, então C\n")
	w.gh.issues[12] = []tables.IssueRef{{Number: 31, Title: "Tela de login"}}
	t12, t15, t20 := w.open(12).Table, w.open(15).Table, w.open(20).Table
	repo := w.repoOf(t12)
	commit(t, w.be, w.git, repo, "src/login/form.ts", "export const f = 1\n", "feat: form")
	commit(t, w.be, w.git, repo, "src/login/session.ts", "export const s = 1\n", "feat: session")
	commit(t, w.be, w.git, repo, "docs/login.md", "# login\n", "docs: login")
	if _, err := w.svc.PROpened(w.ctx, w.slug(), t12.ID, 7, "abc"); err != nil {
		t.Fatal(err)
	}
	w.gh.stats[7] = tables.PullStats{Files: 3, Lines: 41, CI: "success"}
	w.dest.allowed = true
	if _, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: t20.ID, Cancel: true, Reason: "desistiu"}); err != nil {
		t.Fatal(err)
	}

	r := w.run(w.gm.ID, "table status", nil)
	if !r.OK {
		t.Fatalf("table status = %+v", r.Error)
	}
	var out struct {
		Tables []map[string]any `json:"tables"`
	}
	if err := json.Unmarshal(r.Data, &out); err != nil {
		t.Fatal(err)
	}
	by := map[string]map[string]any{}
	for _, row := range out.Tables {
		by[row["name"].(string)] = row
	}
	if len(by) != 2 || by[t20.ID] != nil || by[t12.ID] == nil || by[t15.ID] == nil {
		t.Fatalf("tables = %v, want the two open ones and not the cancelled", slices.Collect(func(yield func(string) bool) {
			for k := range by {
				if !yield(k) {
					return
				}
			}
		}))
	}
	row := by[t12.ID]
	epic := row["epic"].(map[string]any)
	if epic["title"] != "Login" || epic["number"] != float64(12) || len(epic["criteria"].([]any)) != 1 {
		t.Errorf("epic = %v, want its title and its one criterion", epic)
	}
	if issues := row["issues_open"].([]any); len(issues) != 1 || issues[0].(map[string]any)["number"] != float64(31) {
		t.Errorf("issues_open = %v", issues)
	}
	pr := row["pr"].(map[string]any)
	if pr["number"] != float64(7) || pr["files"] != float64(3) || pr["lines"] != float64(41) || pr["ci"] != "success" {
		t.Errorf("pr = %v, want #7 with 3 files, 41 lines and the CI green", pr)
	}
	folders := row["folders"].([]any)
	if len(folders) != 2 || folders[0] != "src/login" || folders[1] != "docs" || row["state"] != "in_review" {
		t.Errorf("folders = %v, state %v; want src/login first (two files) and docs", folders, row["state"])
	}
	// the other table has no pull request and no folders
	other := by[t15.ID]
	if p := other["pr"].(map[string]any); p["number"] != float64(0) || p["ci"] != "none" || len(other["folders"].([]any)) != 0 {
		t.Errorf("a table with nothing: pr %v folders %v", p, other["folders"])
	}
	// one table by the number of its epic
	one := w.run(w.gm.ID, "table status", nil, "12")
	if !one.OK || dataOf(t, one)["name"] != t12.ID || dataOf(t, one)["pr"].(map[string]any)["files"] != float64(3) {
		t.Fatalf("one table: %s", one.Data)
	}
}
