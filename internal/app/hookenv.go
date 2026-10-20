package app

import (
	"context"
	"errors"
	"os"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/store"
)

// The environment the hook decides in on a machine that runs characters
// (classes.Env): what the Runtime knows (the folders of the character) and what
// only the engine does, which NewEnvExtra adds: the way the adapters normalize a
// path, the edits of the turns in progress, the git state of the worktree of the
// table and the risk policy of the workspace (docs/classes/permissoes.md#pipeline-de-decisão).

// envExtra is the EnvExtra of the Runtime of this machine.
func (e *Engine) envExtra() func(l characters.Launch, env *classes.Env) {
	opts := EnvExtraOptions{Clock: e.clk, Providers: providers.Options{}, Edits: e.edits, Risk: e.riskOf}
	if git, err := gitx.New(gitx.Options{Clock: e.clk, Masker: e.deps.Masker, Suite: os.Getenv("MESTRE_TEST_SUITE")}); err == nil {
		opts.Git, opts.Worktrees = git, e.worktreeInfo
	} else {
		e.log.Warn("app: no git for the hook: a commit by a character will be denied", "err", err)
	}
	return NewEnvExtra(opts)
}

// riskOf is the risk policy of the workspace the period that acts reads from
// the approval policy file; none (the default table) outside such a period.
func (e *Engine) riskOf(workspace string) *classes.RiskPolicy {
	if p := e.period.Load(); p != nil && p.risk != nil {
		return p.risk(workspace)
	}
	return nil
}

// errNoRecord: the engine has no record of the worktree (this machine is not the
// Citadel of a period), so nothing opens it and a commit is denied.
var errNoRecord = errors.New("app: no record of the worktree of the table on this machine")

// worktreeInfo is what the Citadel recorded of the worktree of a table here.
func (e *Engine) worktreeInfo(ctx context.Context, workspace, table string) (WorktreeInfo, error) {
	var info WorktreeInfo
	err := e.withGuildDB(func(db *store.DB) error {
		st := tables.Store{DB: db}
		t, err := st.Get(ctx, workspace, table)
		if err != nil {
			return err
		}
		w, err := st.Worktree(ctx, workspace, table, e.id.MachineID)
		if err != nil {
			return err
		}
		info = WorktreeInfo{Record: gitx.WorktreeRecord{Dir: w.Dir, GitDir: w.GitDir, Common: w.Common}, BaseCommit: t.BaseCommit, Main: "refs/remotes/origin/" + t.BaseBranch}
		return nil
	})
	if err != nil {
		return WorktreeInfo{}, errors.Join(errNoRecord, err)
	}
	return info, nil
}
