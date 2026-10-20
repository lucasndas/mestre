//go:build full

package conformance_test

import (
	"context"
	"sync"

	"github.com/lucasndas/mestre/internal/conformance"
	"github.com/lucasndas/mestre/internal/gitx"
)

// decisionGit is the git of the runner of the rig, with the state the engine
// decides a `git commit` on served without a git process.
//
// The `mestre hook` of the character waits 800 ms for the engine and then
// decides alone, denying the commit ("motor indisponível, tente depois"). With
// the real git, the decision of a commit costs about six processes (two
// `rev-parse` to open the worktree by its record, twice, then `worktree list`
// and `status`): ~150 ms idle on Linux, but over the 800 ms on Windows with
// the whole suite running, and the step 5 failed with "o commit não foi
// criado no worktree". The hook is a process of its own with the real clock,
// so the deadline cannot be injected: what the test controls is the work of
// the decision, which here is a lookup. The real state of the engine (the
// branch of the worktree, the staged paths, the pinned opening) is checked
// by TestTheGitStateOfARunKnowsTheMainBranch and by the tests of
// classes.GitxState; the commit itself is still made by the real git of the
// character and read back by the real git of the runner.
type decisionGit struct {
	*gitx.Real
	mu    sync.Mutex
	repos map[string]gitx.Repo
}

func newDecisionGit(g *gitx.Real) *decisionGit {
	return &decisionGit{Real: g, repos: map[string]gitx.Repo{}}
}

// Record records the worktree and opens it by the record once, here and not
// in the decision of the engine.
func (g *decisionGit) Record(ctx context.Context, dir string) (gitx.WorktreeRecord, error) {
	rec, err := g.Real.Record(ctx, dir)
	if err != nil {
		return rec, err
	}
	real, err := g.Real.OpenWorktree(ctx, rec)
	if err != nil {
		return rec, err
	}
	wts, err := real.ListWorktrees(ctx)
	if err != nil {
		return rec, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.repos[rec.Dir] = decisionRepo{Repo: real, worktrees: wts}
	return rec, nil
}

// OpenWorktree is the worktree recorded, opened once by Record.
func (g *decisionGit) OpenWorktree(ctx context.Context, rec gitx.WorktreeRecord) (gitx.Repo, error) {
	g.mu.Lock()
	r, ok := g.repos[rec.Dir]
	g.mu.Unlock()
	if ok {
		return r, nil
	}
	return g.Real.OpenWorktree(ctx, rec)
}

// decisionRepo answers the two reads of the decision of a commit from memory:
// the branch of the worktree, which no step changes, and the one path the
// steps stage. Everything else is the real repository.
type decisionRepo struct {
	gitx.Repo
	worktrees []gitx.Worktree
}

func (r decisionRepo) ListWorktrees(context.Context) ([]gitx.Worktree, error) {
	return r.worktrees, nil
}

func (decisionRepo) Status(context.Context) ([]gitx.StatusEntry, error) {
	return []gitx.StatusEntry{{Index: 'A', Worktree: ' ', Path: conformance.AllowedDir + "/y.txt"}}, nil
}
