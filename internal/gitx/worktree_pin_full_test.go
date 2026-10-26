//go:build full

package gitx_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mestre-ia/mestre/internal/gitx"
)

// C3 of the M3 audit: the .git file of a worktree and the commondir of its
// git folder are written by the account of the agents (Codex opens the git
// folder to writing for the commit). An agent that points the commondir at a
// repository of its own would have the engine read its refs (and its
// config) as the mirror's. The engine opens a worktree only by its record:
// pinned to it, and refused when the worktree points elsewhere.
func TestRealWorktreePinnedToItsRecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g := newReal(gitx.Options{})
	mirror := initRepo(t, g)
	writeFile(t, mirror, "a.txt", "a\n")
	commitAll(t, mirror, "chore: a")
	wt, err := mirror.AddWorktree(ctx, filepath.Join(t.TempDir(), "mesa"), gitx.WorktreeOptions{Branch: "mesa", Create: true})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, wt, "b.txt", "b\n")
	mine := commitAll(t, wt, "feat: b")

	rec, err := g.Record(ctx, wt.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(rec.GitDir) != filepath.Join(rec.Common, "worktrees") {
		t.Fatalf("record = %+v", rec)
	}
	if _, err := g.Record(ctx, mirror.Dir()); !errors.Is(err, gitx.ErrInvalidArgument) {
		t.Fatalf("record of the main checkout = %v, want ErrInvalidArgument", err)
	}
	pinned, err := g.OpenWorktree(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := pinned.ResolveRef(ctx, "mesa"); err != nil || got != mine {
		t.Fatalf("pinned mesa = %s, %v; want %s", got, err, mine)
	}

	// The agent points the commondir at a repository of its own, with a
	// branch mesa of its own.
	evil := initRepo(t, g)
	writeFile(t, evil, "x.txt", "x\n")
	commitAll(t, evil, "chore: x")
	if err := evil.CreateBranch(ctx, "mesa", "main"); err != nil {
		t.Fatal(err)
	}
	commondir := filepath.Join(rec.GitDir, "commondir")
	original, err := os.ReadFile(commondir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(commondir, []byte(filepath.Join(evil.Dir(), ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opened, err := g.Open(ctx, wt.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := opened.ResolveRef(ctx, "mesa"); got == mine {
		t.Fatal("the change of commondir had no effect: the test does not show the vector")
	}
	if err := g.CheckWorktree(ctx, rec); !errors.Is(err, gitx.ErrWorktreeMoved) {
		t.Fatalf("check after the change = %v, want ErrWorktreeMoved", err)
	}
	if _, err := g.OpenWorktree(ctx, rec); !errors.Is(err, gitx.ErrWorktreeMoved) {
		t.Fatalf("open after the change = %v, want ErrWorktreeMoved", err)
	}
	// A worktree already open by its record refuses every call from then on:
	// the refs git would read are the agent's.
	if _, err := pinned.ResolveRef(ctx, "mesa"); !errors.Is(err, gitx.ErrWorktreeMoved) {
		t.Fatalf("a call on the open worktree after the change = %v, want ErrWorktreeMoved", err)
	}
	writeFile(t, pinned, "c.txt", "c\n")
	if err := pinned.Stage(ctx); !errors.Is(err, gitx.ErrWorktreeMoved) {
		t.Fatalf("stage after the change = %v, want ErrWorktreeMoved", err)
	}

	// The same for the .git file of the worktree, pointed at the git folder
	// of another worktree.
	if err := os.WriteFile(commondir, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := pinned.ResolveRef(ctx, "mesa"); err != nil {
		t.Fatalf("after the repair: %v", err)
	}
	other, err := mirror.AddWorktree(ctx, filepath.Join(t.TempDir(), "outra"), gitx.WorktreeOptions{Branch: "outra", Create: true})
	if err != nil {
		t.Fatal(err)
	}
	orec, err := g.Record(ctx, other.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt.Dir(), ".git"), []byte("gitdir: "+orec.GitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := pinned.ResolveRef(ctx, "HEAD"); !errors.Is(err, gitx.ErrWorktreeMoved) {
		t.Fatalf("a call after the change of the .git file = %v, want ErrWorktreeMoved", err)
	}
}
