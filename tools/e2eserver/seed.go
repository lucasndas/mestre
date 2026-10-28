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
func seedRequest(ctx context.Context, guildDB, workspacesDir string, rich bool) error {
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
	// the campaign of the workspaces double (workspaces.go) has its row too: the conversation of the
	// panel is about the current campaign, and the chat lists the rows of the database
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES (?, 'acme/e2e', 'acme/e2e', 'https://example.test/e2e.git', 'main', 'active', '', ?, ?)`, fakeWorkspace, now.UnixMilli(), now.UnixMilli()); err != nil {
		return fmt.Errorf("the campaign of the double: %w", err)
	}
	// a campaign with no Grão-Mestre (the double of the conversation has none for it): the conversation says so
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES (?, 'lucas/sem-grao-mestre', 'lucas/sem-grao-mestre', 'https://example.test/sem.git', 'main', 'active', '', ?, ?)`, "sem-grao-mestre", now.UnixMilli(), now.UnixMilli()); err != nil {
		return fmt.Errorf("the campaign without Grão-Mestre: %w", err)
	}
	if err := seedManage(ctx, db, workspacesDir, now); err != nil {
		return err
	}
	if err := insertCostRequest(ctx, db, now); err != nil {
		return err
	}
	// the request of the table that waits for a decision, and one on another type, both with the table
	if err := insertRequest(ctx, db, now, "loja-decisao", classes.RequestExtraCost, "mais US$ 20 de uso este mês", classes.RiskMedium); err != nil {
		return err
	}
	if err := insertRequest(ctx, db, now, "loja-revisao", classes.RequestMerge, "juntar o PR 21 (avaliações de clientes) ao projeto", classes.RiskHigh); err != nil {
		return err
	}
	if err := seedPanel(ctx, db, now); err != nil {
		return err
	}
	if rich {
		return seedAdmin(ctx, db, now)
	}
	return nil
}

// seedPanel gives the bell its notices (a table that is stuck, the Grand Master, the engine, the
// system) and the Guild a second machine that went quiet.
func seedPanel(ctx context.Context, db *store.DB, now time.Time) error {
	notice := func(kind, table, text, link, ref string, ago time.Duration) error {
		_, err := db.Writer().ExecContext(ctx, `INSERT INTO panel_notices (kind, workspace, mesa, audience, text, link_kind, link_ref, created_at)
			VALUES (?, 'loja', ?, 'workspace', ?, ?, ?, ?)`, kind, table, text, link, ref, now.Add(-ago).UnixMilli())
		return err
	}
	if err := notice("alert", "loja-travada", "A mesa Cálculo de frete está travada: o teste falhou 3 vezes igual.", "table", "loja-travada", 20*time.Minute); err != nil {
		return err
	}
	if err := notice("notify", "loja-revisao", "O Grão-Mestre abriu o PR 21 das avaliações e pede a sua palavra.", "table", "loja-revisao", 2*time.Hour); err != nil {
		return err
	}
	if err := notice("system", "", "A Torre notebook-ana ficou sem sinal.", "machine", "notebook-ana", 26*time.Hour); err != nil {
		return err
	}
	_, err := db.Writer().ExecContext(ctx, `INSERT INTO machines (machine_id, name, instance, priority, excluded, state, names, joined_at)
		VALUES ('e2e-torre', 'notebook-ana', 'torre', 1, 0, 'offline', '{"hostname":"notebook-ana.local","ips":["192.168.1.50"],"port":8443}', ?)`, now.Add(-72*time.Hour).UnixMilli())
	if err != nil {
		return fmt.Errorf("the second machine: %w", err)
	}
	return nil
}

// insertCostRequest leaves a request of extra cost waiting for the people the way the chain does
// (the request, and the question that asks the PIN). The conversation double also raises one when
// the user asks for it (chat.go), so the journey test has a request of its own to approve.
func insertCostRequest(ctx context.Context, db *store.DB, now time.Time) error {
	return insertRequest(ctx, db, now, "", classes.RequestExtraCost, "mais US$ 20 de uso este mês", classes.RiskMedium)
}

// insertRequest is the same for any type, on a table of `loja` (tables.go) or none.
func insertRequest(ctx context.Context, db *store.DB, now time.Time, table string, typ classes.RequestType, about string, risk classes.Risk) error {
	r := chain.Request{
		Workspace: "loja", Table: table, Type: typ, State: chain.Open, Level: chain.Level{Kind: chain.LevelPeople},
		About: about, Risk: classes.RiskResult{Risk: risk, Rule: "e2e", TableVersion: 1},
		CreatedAt: now, Deadline: now.Add(24 * time.Hour),
	}
	r, err := (&chain.Store{DB: db}).Insert(ctx, r)
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
