package fakegit_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/testkit/fakegit"
)

// Record and OpenWorktree stand for the real ones: only a linked worktree has
// a record, and a worktree opens only by it.
func TestRecordAndOpenWorktreeByRecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g := fakegit.NewGit(nil)
	mirror, err := g.Init(ctx, filepath.Join(t.TempDir(), "espelho.git"), gitx.InitOptions{Bare: true})
	if err != nil {
		t.Fatal(err)
	}
	g.Handle(mirror.Dir()).Data().Commit("main", "init", map[string]string{"a": "a"})
	wtDir := filepath.Join(t.TempDir(), "principal")
	if _, err := mirror.AddWorktree(ctx, wtDir, gitx.WorktreeOptions{Branch: "main"}); err != nil {
		t.Fatal(err)
	}
	rec, err := g.Record(ctx, wtDir)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Common != mirror.Dir() || rec.GitDir != filepath.Join(mirror.Dir(), "worktrees", "principal") {
		t.Fatalf("record = %+v", rec)
	}
	if _, err := g.Record(ctx, mirror.Dir()); !errors.Is(err, gitx.ErrInvalidArgument) {
		t.Fatalf("record of the repository itself = %v; want ErrInvalidArgument", err)
	}
	wt, err := g.OpenWorktree(ctx, rec)
	if err != nil || wt.Dir() != g.Handle(wtDir).Dir() {
		t.Fatalf("OpenWorktree = %v, %v", wt, err)
	}
	moved := rec
	moved.Common = filepath.Join(t.TempDir(), "other.git")
	if _, err := g.OpenWorktree(ctx, moved); !errors.Is(err, gitx.ErrWorktreeMoved) {
		t.Fatalf("OpenWorktree of a record that points elsewhere = %v; want ErrWorktreeMoved", err)
	}
}
