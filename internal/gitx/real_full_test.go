//go:build full

package gitx_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/gitx"
	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/testkit/gitxsuite"
	"github.com/mestre-ia/mestre/pkg/clock"
)

var author = gitx.Signature{Name: "Alice Dev", Email: "alice@example.invalid", When: time.Unix(1767225600, 0)}

func newReal(opts gitx.Options) *gitx.Real {
	if opts.Clock == nil {
		opts.Clock = clock.NewFake(gitxsuite.Now)
	}
	if opts.Masker == nil {
		opts.Masker = logging.NewMasker()
	}
	g, err := gitx.New(opts)
	if err != nil {
		panic(err)
	}
	return g
}

func writeFile(t testing.TB, r gitx.Repo, rel, content string) {
	t.Helper()
	p := filepath.Join(r.Dir(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitAll(t testing.TB, r gitx.Repo, msg string) gitx.Hash {
	t.Helper()
	ctx := context.Background()
	if err := r.Stage(ctx); err != nil {
		t.Fatal(err)
	}
	h, err := r.Commit(ctx, gitx.CommitOptions{Message: msg, Author: author})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func initRepo(t testing.TB, g gitx.Git) gitx.Repo {
	t.Helper()
	r, err := g.Init(context.Background(), filepath.Join(t.TempDir(), "repo"), gitx.InitOptions{InitialBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The fake and the real git run the same scenarios; the fake runs them in
// the fast suite (internal/testkit/fakegit).
func TestRealConformance(t *testing.T) {
	t.Parallel()
	gitxsuite.Run(t, gitxsuite.Harness{
		New:   func(testing.TB) gitx.Git { return newReal(gitx.Options{}) },
		Write: writeFile,
		Remove: func(t testing.TB, r gitx.Repo, rel string) {
			if err := os.Remove(filepath.Join(r.Dir(), filepath.FromSlash(rel))); err != nil {
				t.Fatal(err)
			}
		},
	})
}

func TestRealVersionCheck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	v, err := newReal(gitx.Options{}).Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v.Less(gitx.MinVersion) || !strings.HasPrefix(v.Raw, "git version") {
		t.Fatalf("version = %+v", v)
	}

	_, err = newReal(gitx.Options{MinVersion: gitx.Version{Major: 99}}).Version(ctx)
	if !errors.Is(err, gitx.ErrGitTooOld) || !strings.Contains(err.Error(), "99.0.0") {
		t.Fatalf("err = %v, want ErrGitTooOld naming 99.0.0", err)
	}
	// Every call checks it, so no repository is touched with an old git.
	g := newReal(gitx.Options{MinVersion: gitx.Version{Major: 99}})
	if _, err := g.Init(ctx, filepath.Join(t.TempDir(), "r"), gitx.InitOptions{}); !errors.Is(err, gitx.ErrGitTooOld) {
		t.Fatalf("Init with an old git: %v", err)
	}

	_, err = newReal(gitx.Options{Binary: "mestre-no-such-git-binary"}).Version(ctx)
	if !errors.Is(err, gitx.ErrGitNotFound) {
		t.Fatalf("err = %v, want ErrGitNotFound", err)
	}
}

func TestRealStopsOnCancelledContext(t *testing.T) {
	t.Parallel()
	g := newReal(gitx.Options{})
	r := initRepo(t, g)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.ResolveRef(ctx, "HEAD"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ResolveRef: %v, want context.Canceled", err)
	}
	if err := r.CreateBranch(ctx, "x", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("CreateBranch: %v, want context.Canceled", err)
	}
}

func TestRealCommitIgnoresHooksSigningAndUserConfig(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g := newReal(gitx.Options{})
	r := initRepo(t, g)
	// Repository-level settings that would stop or change a commit.
	hook := filepath.Join(r.Dir(), ".git", "hooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"commit.gpgsign": "true", "gpg.program": "mestre-no-such-gpg", "user.name": "Local Name", "user.email": "local@example.invalid",
		"core.autocrlf": "false",
	} {
		if err := r.SetConfig(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, r, "a.txt", "a\n")
	h := commitAll(t, r, "feat: a")
	log, err := r.Log(ctx, string(h))
	if err != nil || len(log) != 1 || log[0].Author.Name != author.Name || log[0].Committer.Email != author.Email {
		t.Fatalf("log = %+v, %v: the identity must come from CommitOptions only", log, err)
	}
}

func TestRealCommitIsReproducible(t *testing.T) {
	t.Parallel()
	g := newReal(gitx.Options{})
	var hashes [2]gitx.Hash
	for i := range hashes {
		r := initRepo(t, g)
		writeFile(t, r, "dir with space/ação.txt", "olá\n")
		hashes[i] = commitAll(t, r, "feat: reproducible\n\nRefs: #1")
	}
	if hashes[0] != hashes[1] {
		t.Fatalf("same tree, identity, date and message gave %s and %s", hashes[0], hashes[1])
	}
}

func TestRealFilesKeepTheirBytes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g := newReal(gitx.Options{})
	r := initRepo(t, g)
	crlf := "line one\r\nline two\r\n"
	bin := string([]byte{0, 1, 2, 0xff, '\n', 0})
	writeFile(t, r, "crlf.txt", crlf)
	writeFile(t, r, "bin.dat", bin)
	writeFile(t, r, "dir with space/ação.txt", "olá\n")
	commitAll(t, r, "chore: files")
	for rel, want := range map[string]string{"crlf.txt": crlf, "bin.dat": bin, "dir with space/ação.txt": "olá\n"} {
		got, err := r.ReadFile(ctx, "main", rel)
		if err != nil || !bytes.Equal(got, []byte(want)) {
			t.Errorf("%s = %q, %v; want %q", rel, got, err, want)
		}
	}
	writeFile(t, r, "dir with space/ação.txt", "mudou\n")
	st, err := r.Status(ctx)
	if err != nil || len(st) != 1 || st[0].Path != "dir with space/ação.txt" {
		t.Fatalf("status = %+v, %v: paths with spaces and accents must come back unquoted", st, err)
	}
}

// A file that is in the folder but not in the commit is a missing file, not
// a failure of the tool: it says "exists on disk, but not in" for it.
func TestRealReadFileOfAnUntrackedFileIsNotFound(t *testing.T) {
	t.Parallel()
	g := newReal(gitx.Options{})
	r := initRepo(t, g)
	writeFile(t, r, "a.txt", "a\n")
	commitAll(t, r, "chore: a")
	writeFile(t, r, "local.txt", "local\n")
	if _, err := r.ReadFile(context.Background(), "HEAD", "local.txt"); !errors.Is(err, gitx.ErrFileNotFound) {
		t.Fatalf("ReadFile of an untracked file = %v; want ErrFileNotFound", err)
	}
}

func TestRealErrorsNeverCarryTheAuth(t *testing.T) {
	t.Parallel()
	g := newReal(gitx.Options{})
	const secret = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	_, err := g.Clone(context.Background(), "https://bot:"+secret+"@127.0.0.1:1/o/r.git", filepath.Join(t.TempDir(), "r.git"), gitx.CloneOptions{Bare: true})
	if !errors.Is(err, gitx.ErrCredentialsInURL) {
		t.Fatalf("clone of a URL with credentials: %v, want ErrCredentialsInURL", err)
	}
	auth := gitx.BasicAuth("https://127.0.0.1:1/", "bot", secret)
	_, err = g.Clone(context.Background(), "https://127.0.0.1:1/o/r.git", filepath.Join(t.TempDir(), "r.git"), gitx.CloneOptions{Bare: true, Auth: &auth})
	if err == nil {
		t.Fatal("clone of an unreachable URL succeeded")
	}
	for _, leak := range []string{"ghp_", "bot:", auth.Header} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("error leaks %q: %v", leak, err)
		}
	}
	var ge *gitx.Error
	if !errors.As(err, &ge) || ge.ExitCode == 0 {
		t.Fatalf("error is not a *gitx.Error with the exit code: %v", err)
	}
}

func TestRealExcludeBlockLivesInSharedInfoExclude(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g := newReal(gitx.Options{})
	r := initRepo(t, g)
	writeFile(t, r, "a.txt", "a\n")
	commitAll(t, r, "chore: a")
	file := filepath.Join(r.Dir(), ".git", "info", "exclude")
	user := "# my own ignores\r\n*.swp\r\n"
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.SetExcludeBlock(ctx, "mestre", []string{"/.mestre/"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(file)
	if err != nil || !strings.HasPrefix(string(got), user) || !strings.Contains(string(got), "/.mestre/\n") {
		t.Fatalf("info/exclude = %q, %v", got, err)
	}

	// A worktree sees the same block, and git honors it there.
	wt, err := r.AddWorktree(ctx, filepath.Join(t.TempDir(), "wt"), gitx.WorktreeOptions{Branch: "mesa", Create: true})
	if err != nil {
		t.Fatal(err)
	}
	if block, err := wt.ExcludeBlock(ctx, "mestre"); err != nil || len(block) != 1 || block[0] != "/.mestre/" {
		t.Fatalf("block seen from the worktree = %q, %v", block, err)
	}
	writeFile(t, wt, ".mestre/state.json", "{}\n")
	writeFile(t, wt, "b.txt", "b\n")
	st, err := wt.Status(ctx)
	if err != nil || len(st) != 1 || st[0].Path != "b.txt" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	if err := wt.SetExcludeBlock(ctx, "mestre", nil); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(file)
	if string(got) != user {
		t.Fatalf("info/exclude after removal = %q, want the user lines only", got)
	}
}

func TestRealSerializesChangesToTheSameRepository(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g := newReal(gitx.Options{})
	root := t.TempDir()
	origin := initRepo(t, g)
	writeFile(t, origin, "a.txt", "a\n")
	commitAll(t, origin, "chore: a")
	mirror, err := g.Clone(ctx, origin.Dir(), filepath.Join(root, "espelho.git"), gitx.CloneOptions{Bare: true})
	if err != nil {
		t.Fatal(err)
	}
	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			if _, err := mirror.AddWorktree(ctx, filepath.Join(root, "mesas", fmt.Sprint(i)), gitx.WorktreeOptions{
				Branch: fmt.Sprintf("mesa/%d", i), Create: true, StartPoint: "main",
			}); err != nil {
				errs[i] = err
				return
			}
			errs[i] = mirror.CreateBranch(ctx, fmt.Sprintf("extra/%d", i), "main")
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("worker %d: %v", i, err)
		}
	}
	if wts, err := mirror.ListWorktrees(ctx); err != nil || len(wts) != n+1 {
		t.Fatalf("worktrees = %d, %v", len(wts), err)
	}
}

func TestRealOpenFindsTheCommonDirFromAnyWorktree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g := newReal(gitx.Options{})
	r := initRepo(t, g)
	writeFile(t, r, "a.txt", "a\n")
	commitAll(t, r, "chore: a")
	wt, err := r.AddWorktree(ctx, filepath.Join(t.TempDir(), "wt"), gitx.WorktreeOptions{Branch: "mesa", Create: true})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, wt, "b.txt", "b\n")
	h := commitAll(t, wt, "feat: b")
	if got, err := r.ResolveRef(ctx, "mesa"); err != nil || got != h {
		t.Fatalf("main checkout sees mesa = %s, %v; want %s", got, err, h)
	}
	if _, err := g.Open(ctx, filepath.Join(r.Dir(), "no", "such", "dir")); !errors.Is(err, gitx.ErrNotARepo) {
		t.Fatalf("Open of a missing dir: %v", err)
	}
}
