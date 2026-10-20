package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The issue of a mission is the table's only when the engine registered it
// for that table of that workspace; anything else, unknown or of nobody is not.
func TestAnIssueIsTheTablesOnlyWhenTheEngineRegisteredItForThatTable(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	db, err := guildtpl.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES ('meu-app', 'lucas/Meu_App', 'lucas/meu_app', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	objs := &github.Objects{DB: db, Clock: clk}
	for n, table := range map[int]string{51: "meu-app/42-login", 52: "meu-app/43-outra"} {
		if err := objs.Register(ctx, github.Object{Repo: "lucas/meu_app", Kind: github.KindIssue, Number: n, Table: table, Op: github.NewOp()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := (tables.Store{DB: db}).Insert(ctx, tables.Table{Workspace: "meu-app", ID: "42-login", Epic: 42, EpicTitle: "Login", Branch: "epico/42-login", BaseBranch: "main", State: tables.StateWorking,
		CreatedAt: clk.Now(), UpdatedAt: clk.Now()}); err != nil {
		t.Fatal(err)
	}
	owner := issueOwner{db: db, clk: clk}
	for _, c := range []struct {
		workspace, table string
		issue            int
		want             bool
	}{
		{"meu-app", "42-login", 51, true},
		{"meu-app", "42-login", 52, false}, // another table's
		{"meu-app", "42-login", 99, false}, // nobody's
		{"outro", "42-login", 51, false},   // a workspace that does not exist
		{"meu-app", "43-outra", 52, true},
		{"meu-app", "42-login", 42, true},  // the epic, which the engine does not register
		{"meu-app", "43-outra", 42, false}, // but only for its own table
	} {
		got, err := owner.Owns(ctx, c.workspace, c.table, c.issue)
		if err != nil || got != c.want {
			t.Errorf("Owns(%s, %s, #%d) = %v, %v; want %v", c.workspace, c.table, c.issue, got, err, c.want)
		}
	}
}
