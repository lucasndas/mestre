package fakegit

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/mestre-ia/mestre/internal/gitx"
)

var _ gitx.WorktreeOpener = (*Git)(nil)

// commonDir is the common dir the real git would report for the repository
// of r: the repository itself when bare, its .git otherwise.
func (r *Repo) commonDir() string {
	if r.bare {
		return r.mainDir
	}
	return filepath.Join(r.mainDir, ".git")
}

// recordOf is the record of a linked worktree, laid out like git does:
// <common>/worktrees/<name of the folder>.
func (r *Repo) recordOf(dir string) gitx.WorktreeRecord {
	common := r.commonDir()
	return gitx.WorktreeRecord{Dir: dir, GitDir: filepath.Join(common, "worktrees", filepath.Base(dir)), Common: common}
}

// Record is gitx.Real.Record for the fake: the engine's record of the linked
// worktree at dir, taken right after AddWorktree. The main checkout and a
// bare repository have none (gitx.ErrInvalidArgument), as with the real git.
func (g *Git) Record(_ context.Context, dir string) (gitx.WorktreeRecord, error) {
	abs := absDir(dir)
	g.mu.Lock()
	defer g.mu.Unlock()
	h, ok := g.byDir[abs]
	if !ok {
		return gitx.WorktreeRecord{}, fmt.Errorf("record %s: %w", dir, gitx.ErrNotARepo)
	}
	if abs == h.r.mainDir {
		return gitx.WorktreeRecord{}, fmt.Errorf("%w: %s is not a linked worktree", gitx.ErrInvalidArgument, dir)
	}
	return h.r.recordOf(abs), nil
}

// OpenWorktree is gitx.Real.OpenWorktree for the fake: the worktree opens
// only when it is a worktree of the recorded repository, laid out as the
// record says; anything else (gone, or of another repository) is
// gitx.ErrWorktreeMoved.
func (g *Git) OpenWorktree(_ context.Context, rec gitx.WorktreeRecord) (gitx.Repo, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	h, ok := g.byDir[absDir(rec.Dir)]
	if !ok || h.dir == h.r.mainDir || h.r.recordOf(h.dir) != rec {
		return nil, fmt.Errorf("%w: %s does not point at its record", gitx.ErrWorktreeMoved, rec.Dir)
	}
	return h, nil
}
