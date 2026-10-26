package main

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/presets"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
	"github.com/mestre-ia/mestre/internal/store"
	guilddb "github.com/mestre-ia/mestre/internal/store/guild"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// seedRequest puts a request of extra cost in the Guild database, waiting for the
// people the way the chain leaves it (the request, and the question that asks the PIN),
// before the engine starts: the smoke test finds it in the central of approvals and
// answers it. The Guild has no workspace and no agent; the request is the engine's own.
func seedRequest(ctx context.Context, guildDB, workspacesDir string) error {
	clk := clock.Real{}
	db, err := guilddb.Open(ctx, guildDB, store.Options{Clock: clk, Synchronous: "OFF"})
	if err != nil {
		return err
	}
	defer db.Close()
	now := clk.Now()
	// the workspace of the request, where the conversation of the smoke test happens
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES ('loja', 'lucas/loja', 'lucas/loja', 'https://example.test/loja.git', 'main', 'active', '', ?, ?)`, now.UnixMilli(), now.UnixMilli()); err != nil {
		return fmt.Errorf("the workspace: %w", err)
	}
	if err := seedManage(ctx, db, workspacesDir, now); err != nil {
		return err
	}
	r := chain.Request{
		Workspace: "loja", Type: classes.RequestExtraCost, State: chain.Open, Level: chain.Level{Kind: chain.LevelPeople},
		About: "mais US$ 20 de uso este mês", Risk: classes.RiskResult{Risk: classes.RiskMedium, Rule: "custo extra", TableVersion: 1},
		CreatedAt: now, Deadline: now.Add(24 * time.Hour),
	}
	r, err = (&chain.Store{DB: db}).Insert(ctx, r)
	if err != nil {
		return err
	}
	text := chain.Text(r, r.Level, chain.AnswersFor(chain.LevelPeople, r))
	return db.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO approval_flow (request, approvals, require, min_level, pin, text, asked_at) VALUES (?, 1, '[]', ?, 1, ?, ?)`,
			r.ID, string(classes.LevelApprover), text, now.UnixMilli())
		if err != nil {
			return fmt.Errorf("the question of the request: %w", err)
		}
		return nil
	})
}

// seedManage gives the screens of the classes and of the bases and sheets what they list:
// an approved class `revisor` of the workspace (written in its folder, as the Mestre
// keeps it) and the base `claude-code` with the sheet "Dev Claude".
func seedManage(ctx context.Context, db *store.DB, workspacesDir string, now time.Time) error {
	files, err := classes.NewFiles([]classes.File{
		{Path: "CLASS.md", Data: []byte("---\nname: revisor\ndescription: Revisa o que a mesa entrega\napp: novo\n---\nRevise o código da mesa.\n")},
		{Path: "permissions.yaml", Data: []byte("version: 1\nfiles:\n  read: [\"**\"]\n")},
	})
	if err != nil {
		return err
	}
	roots := classes.DefaultRoots(filepath.Join(workspacesDir, "classes-globais"), workspaces.Layout{Root: workspacesDir}.Principal("loja"))
	dir, err := roots.Dir(classes.ClassKey{Scope: classes.ClassWorkspace, Name: "revisor"})
	if err != nil {
		return err
	}
	if err := classes.WriteDir(dir, files); err != nil {
		return err
	}
	rec := classes.ClassRecord{Key: classes.ClassKey{Scope: classes.ClassWorkspace, Name: "revisor"}, Hash: files.Hash(), Files: files, ApprovedAt: now, By: classes.Author{Level: classes.LevelAdmin, ID: "e2e"}}
	if err := (classes.ClassDB{DB: db, Clock: clock.Real{}}).Approve(ctx, "loja", rec, 0); err != nil {
		return fmt.Errorf("the class: %w", err)
	}
	cat := &presets.Catalog{DB: db, Clock: clock.Real{}}
	if err := cat.SaveBase(ctx, presets.Base{Name: "claude-code", Command: "claude", Platforms: []presets.Platform{{OS: presets.Linux}, {OS: presets.Windows, Command: "claude.exe"}}, Ready: presets.Ready{Mode: presets.ReadyHook}}); err != nil {
		return fmt.Errorf("the base: %w", err)
	}
	if _, err := cat.SaveSheet(ctx, presets.Sheet{Name: "Dev Claude", Base: "claude-code", Agent: presets.AgentClaudeCode, Lifecycle: presets.Persistent, MaxIdle: "240m"}); err != nil {
		return fmt.Errorf("the sheet: %w", err)
	}
	return nil
}
