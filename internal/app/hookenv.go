package app

import (
	"context"
	"errors"
	"os"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/gitx"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/store"
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

// errNoRecord: the engine has no record of the worktree (this machine did not make it and is not the
// Citadel of a period), so nothing opens it and a commit is denied.
var errNoRecord = errors.New("app: no record of the worktree of the table on this machine")

// localWorktree is the record the machine itself keeps of the worktree it made of
// a table (a Tower, local_worktrees): the one git opens it by, which the Citadel
// does not have for a worktree of another machine.
func (e *Engine) localWorktree(ctx context.Context, workspace, table string) (gitx.WorktreeRecord, bool) {
	if e.mdb == nil {
		return gitx.WorktreeRecord{}, false
	}
	var rec gitx.WorktreeRecord
	err := e.mdb.Reader().QueryRowContext(ctx, `SELECT dir, git_dir, common_dir FROM local_worktrees WHERE workspace = ? AND table_id = ?`, workspace, table).
		Scan(&rec.Dir, &rec.GitDir, &rec.Common)
	return rec, err == nil && rec.Dir != "" && rec.GitDir != "" && rec.Common != ""
}

// worktreeInfo is what the machine recorded of the worktree of a table here: its
// own record when it made the worktree (a Tower), else what the Citadel recorded.
func (e *Engine) worktreeInfo(ctx context.Context, workspace, table string) (WorktreeInfo, error) {
	var info WorktreeInfo
	err := e.withGuildDB(func(db *store.DB) error {
		st := tables.Store{DB: db}
		t, err := st.Get(ctx, workspace, table)
		if err != nil {
			return err
		}
		rec, ok := e.localWorktree(ctx, workspace, table)
		if !ok {
			w, err := st.Worktree(ctx, workspace, table, e.id.MachineID)
			if err != nil {
				return err
			}
			rec = gitx.WorktreeRecord{Dir: w.Dir, GitDir: w.GitDir, Common: w.Common}
		}
		info = WorktreeInfo{Record: rec, BaseCommit: t.BaseCommit, Main: "refs/remotes/origin/" + t.BaseBranch}
		return nil
	})
	if err != nil {
		return WorktreeInfo{}, errors.Join(errNoRecord, err)
	}
	return info, nil
}
