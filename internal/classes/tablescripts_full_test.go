//go:build full

package classes_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/pkg/clock"
)

// tableRepo is a repository with scripts/build.sh on main and the table's
// worktree on its own branch.
type tableRepo struct {
	ctx        context.Context
	g          *gitx.Real
	main, wt   gitx.Repo
	mainDir    string
	wtDir      string
	scripts    classes.GitxState
	outsideDir string
}

func newTableRepo(t *testing.T) *tableRepo {
	t.Helper()
	ctx := context.Background()
	g, err := gitx.New(gitx.Options{Clock: clock.NewFake(now), Masker: logging.NewMasker()})
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tr := &tableRepo{ctx: ctx, g: g, mainDir: filepath.Join(root, "main"), wtDir: filepath.Join(root, "mesa"), outsideDir: filepath.Join(root, "fora")}
	if tr.main, err = g.Init(ctx, tr.mainDir, gitx.InitOptions{InitialBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	tr.write(t, tr.mainDir, "scripts/build.sh", "echo build\n")
	tr.write(t, tr.mainDir, "README.md", "x\n")
	tr.commit(t, tr.main)
	if tr.wt, err = tr.main.AddWorktree(ctx, tr.wtDir, gitx.WorktreeOptions{Branch: "epico/42-login", Create: true}); err != nil {
		t.Fatal(err)
	}
	rec, err := g.Record(ctx, tr.wtDir)
	if err != nil {
		t.Fatal(err)
	}
	tr.scripts = classes.GitxState{Git: g, Worktree: rec, Main: "main"}
	return tr
}

func (tr *tableRepo) write(t *testing.T, dir, rel, data string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (tr *tableRepo) commit(t *testing.T, r gitx.Repo) {
	t.Helper()
	if err := r.Stage(tr.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Commit(tr.ctx, gitx.CommitOptions{Message: "x", Author: gitx.Signature{Name: "Dev", Email: "dev@example.invalid", When: now}}); err != nil {
		t.Fatal(err)
	}
}

func (tr *tableRepo) changed(t *testing.T, rel string) bool {
	t.Helper()
	c, _ := tr.scripts.Changed(tr.ctx, tr.wtDir, filepath.Join(tr.wtDir, filepath.FromSlash(rel)))
	return c
}

// The base of the table branch, read with the real git: a script is the
// repository's while its content is the one of the merge-base with main.
func TestTableScriptsAgainstTheBaseOfTheBranch(t *testing.T) {
	t.Parallel()
	tr := newTableRepo(t)
	check := func(rel string, want bool) {
		t.Helper()
		if got := tr.changed(t, rel); got != want {
			t.Fatalf("Changed(%s) = %v; want %v", rel, got, want)
		}
	}
	check("scripts/build.sh", false)
	// Created at the table: untracked, then committed on the branch.
	tr.write(t, tr.wtDir, "scripts/new.sh", "echo new\n")
	check("scripts/new.sh", true)
	tr.commit(t, tr.wt)
	check("scripts/new.sh", true)
	check("scripts/build.sh", false)
	// Changed in the worktree, with only the line endings, and committed.
	tr.write(t, tr.wtDir, "scripts/build.sh", "echo build\r\n")
	check("scripts/build.sh", false)
	tr.write(t, tr.wtDir, "scripts/build.sh", "echo evil\n")
	check("scripts/build.sh", true)
	tr.commit(t, tr.wt)
	check("scripts/build.sh", true)
	tr.write(t, tr.wtDir, "scripts/build.sh", "echo build\n")
	check("scripts/build.sh", false) // back to the base: the repository's again
	// main moving on does not change the base of the branch.
	tr.write(t, tr.mainDir, "scripts/build.sh", "echo build v2\n")
	tr.commit(t, tr.main)
	check("scripts/build.sh", false)
	// A link out of the worktree, a folder, a missing file: changed.
	tr.write(t, tr.outsideDir, "x.sh", "echo build\n")
	if err := os.Symlink(filepath.Join(tr.outsideDir, "x.sh"), filepath.Join(tr.wtDir, "scripts", "out.sh")); err == nil {
		check("scripts/out.sh", true)
	}
	check("scripts", true)
	check("scripts/none.sh", true)
	// Without the main branch there is no base.
	if c, err := (classes.GitxState{Git: tr.g, Worktree: tr.scripts.Worktree}).Changed(tr.ctx, tr.wtDir, filepath.Join(tr.wtDir, "scripts", "build.sh")); !c || err == nil {
		t.Fatalf("without Main: %v, %v; want changed with an error", c, err)
	}
}
