package fakegit_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/testkit/fakegit"
	"github.com/lucasndas/mestre/internal/testkit/gitxsuite"
	"github.com/lucasndas/mestre/pkg/clock"
)

var (
	_ gitx.Git  = (*fakegit.Git)(nil)
	_ gitx.Repo = (*fakegit.Handle)(nil)
)

// TestConformance runs the scenarios the real git also runs in the full
// suite (internal/gitx/real_full_test.go), so the fake cannot drift.
func TestConformance(t *testing.T) {
	t.Parallel()
	gitxsuite.Run(t, gitxsuite.Harness{
		New: func(testing.TB) gitx.Git { return fakegit.NewGit(clock.NewFake(gitxsuite.Now)) },
		Write: func(_ testing.TB, r gitx.Repo, rel, content string) {
			r.(*fakegit.Handle).SetFile(rel, content)
		},
		Remove: func(_ testing.TB, r gitx.Repo, rel string) {
			r.(*fakegit.Handle).RemoveFile(rel)
		},
	})
}

// The data model stays reachable for tests that seed a repository directly,
// and handles see what Repo.Commit recorded.
func TestHandleSharesTheDataModel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g := fakegit.NewGit(nil)
	r, err := g.Init(ctx, filepath.Join(t.TempDir(), "r"), gitx.InitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	h := g.Handle(r.Dir())
	c := h.Data().Commit("main", "init", map[string]string{"README.md": "hi"})
	if got, err := r.ResolveRef(ctx, "HEAD"); err != nil || got != c {
		t.Fatalf("HEAD = %s, %v; want %s", got, err, c)
	}
	if b, err := r.ReadFile(ctx, "main", "README.md"); err != nil || string(b) != "hi" {
		t.Fatalf("README = %q, %v", b, err)
	}
	if g.Handle(filepath.Join(t.TempDir(), "elsewhere")) != nil {
		t.Fatal("Handle returned a repository nobody created")
	}
}

// The table lifecycle (M3, CA-MESAS-06) owns the rule; this is the primitive it
// stands on: the fake reports a file not committed in the worktree of a mesa.
func TestFakeReportsUncommittedFileInWorktree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g := fakegit.NewGit(nil)
	root := t.TempDir()
	mirror, err := g.Init(ctx, filepath.Join(root, "espelho.git"), gitx.InitOptions{Bare: true})
	if err != nil {
		t.Fatal(err)
	}
	g.Handle(mirror.Dir()).Data().Commit("main", "init", map[string]string{"a": "1"})
	wt, err := mirror.AddWorktree(ctx, filepath.Join(root, "mesa"), gitx.WorktreeOptions{Branch: "epico/42", Create: true, StartPoint: "main"})
	if err != nil {
		t.Fatal(err)
	}
	wt.(*fakegit.Handle).SetFile("notes.md", "wip")
	st, err := wt.Status(ctx)
	if err != nil || len(st) != 1 || !st[0].Untracked() || st[0].Path != "notes.md" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	if err := mirror.RemoveWorktree(ctx, wt.Dir(), false); !errors.Is(err, gitx.ErrDirtyWorktree) {
		t.Fatalf("remove = %v, want ErrDirtyWorktree", err)
	}
}

func TestFakeRefusesCredentialsInURLsWithoutEchoingThem(t *testing.T) {
	t.Parallel()
	g := fakegit.NewGit(nil)
	_, err := g.Clone(context.Background(), "https://user:ghp_secretsecretsecretsecret1234@example.invalid/o/r.git", filepath.Join(t.TempDir(), "x"), gitx.CloneOptions{Bare: true})
	if !errors.Is(err, gitx.ErrCredentialsInURL) {
		t.Fatalf("err = %v", err)
	}
	if got := err.Error(); containsAny(got, "user:", "ghp_") {
		t.Fatalf("error leaks credentials: %q", got)
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}
