// Package gitxsuite is the conformance suite of gitx.Git: the same scenarios
// run against the real git (full suite) and against internal/testkit/fakegit
// (fast suite), so the fake cannot drift from the behavior the engine
// relies on (docs/qualidade/dubles.md).
package gitxsuite

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/gitx"
)

// Now is the instant the clock of the harness must report.
var Now = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Harness binds the suite to an implementation.
type Harness struct {
	// New returns a Git whose clock reports Now.
	New func(t testing.TB) gitx.Git
	// Write puts a file in the working tree of r (creating directories),
	// the way a program running in the worktree would.
	Write func(t testing.TB, r gitx.Repo, rel, content string)
	// Remove deletes a file from the working tree of r.
	Remove func(t testing.TB, r gitx.Repo, rel string)
}

var (
	alice = gitx.Signature{Name: "Alice Dev", Email: "alice@example.invalid", When: time.Unix(1767225600, 0)}
	bot   = gitx.Signature{Name: "mestre-dev[bot]", Email: "123+mestre-dev[bot]@users.noreply.github.com", When: time.Unix(1767229200, 0)}
)

// Run runs every scenario as a parallel subtest.
func Run(t *testing.T, h Harness) {
	t.Helper()
	cases := []struct {
		name string
		fn   func(t *testing.T, h Harness)
	}{
		{"commit identity and history", commitIdentityAndHistory},
		{"status stage exclude and skip-worktree", statusStageExcludeSkip},
		{"mirror and worktrees", mirrorAndWorktrees},
		{"merge fast-forward only", mergeFastForwardOnly},
		{"bundles", bundles},
		{"fetch and config", fetchAndConfig},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if r := recover(); r != nil {
					f, isFailure := r.(failure)
					if !isFailure {
						panic(r)
					}
					t.Fatalf("unexpected error: %v", f.err)
				}
			}()
			c.fn(t, h)
		})
	}
}

// failure carries an unexpected error from must and ok to the subtest that
// reports it (a generic helper cannot take t and a two-value call).
type failure struct{ err error }

func must[T any](v T, err error) T {
	if err != nil {
		panic(failure{err})
	}
	return v
}

func ok(err error) {
	if err != nil {
		panic(failure{err})
	}
}

func isErr(t testing.TB, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

// canon makes two spellings of the same existing path comparable (symlinks,
// 8.3 names on Windows).
func canon(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(r)
	}
	return filepath.Clean(p)
}

// seed makes a repository with a main branch holding files, one commit.
func seed(t *testing.T, h Harness, g gitx.Git, dir string, files map[string]string) gitx.Repo {
	t.Helper()
	ctx := context.Background()
	r := must(g.Init(ctx, dir, gitx.InitOptions{InitialBranch: "main"}))
	for _, p := range slices.Sorted(maps.Keys(files)) {
		h.Write(t, r, p, files[p])
	}
	ok(r.Stage(ctx))
	must(r.Commit(ctx, gitx.CommitOptions{Message: "chore: seed", Author: alice}))
	return r
}

func commitFile(t *testing.T, h Harness, r gitx.Repo, rel, content, msg string) gitx.Hash {
	t.Helper()
	ctx := context.Background()
	h.Write(t, r, rel, content)
	ok(r.Stage(ctx))
	return must(r.Commit(ctx, gitx.CommitOptions{Message: msg, Author: alice}))
}

func statusOf(t *testing.T, r gitx.Repo) []string {
	t.Helper()
	var out []string
	for _, e := range must(r.Status(context.Background())) {
		out = append(out, string([]byte{e.Index, e.Worktree})+" "+e.Path)
	}
	slices.Sort(out)
	return out
}

func commitIdentityAndHistory(t *testing.T, h Harness) {
	ctx := context.Background()
	g := h.New(t)
	ver := must(g.Version(ctx))
	if ver.Less(gitx.MinVersion) {
		t.Fatalf("version %s is older than the minimum", ver)
	}
	r := must(g.Init(ctx, filepath.Join(t.TempDir(), "repo"), gitx.InitOptions{InitialBranch: "main"}))

	_, err := r.ResolveRef(ctx, "main")
	isErr(t, err, gitx.ErrRefNotFound)
	_, err = r.Commit(ctx, gitx.CommitOptions{Message: "empty", Author: alice})
	isErr(t, err, gitx.ErrNothingToCommit)
	_, err = r.Commit(ctx, gitx.CommitOptions{Message: "no author"})
	isErr(t, err, gitx.ErrInvalidArgument)

	h.Write(t, r, "a.txt", "one\n")
	ok(r.Stage(ctx))
	msg := "feat: first\n\nMestre-Mesa: epico/42-login\nRefs: #57\n"
	c1 := must(r.Commit(ctx, gitx.CommitOptions{Message: msg, Author: alice, Committer: bot}))
	if got := must(r.ResolveRef(ctx, "main")); got != c1 {
		t.Fatalf("main = %s, commit returned %s", got, c1)
	}
	log := must(r.Log(ctx, "main"))
	if len(log) != 1 || log[0].Hash != c1 {
		t.Fatalf("log = %+v", log)
	}
	got := log[0]
	if got.Author.Name != alice.Name || got.Author.Email != alice.Email || got.Author.When.Unix() != alice.When.Unix() {
		t.Fatalf("author = %+v", got.Author)
	}
	if got.Committer.Name != bot.Name || got.Committer.Email != bot.Email || got.Committer.When.Unix() != bot.When.Unix() {
		t.Fatalf("committer = %+v", got.Committer)
	}
	if got.Message != strings.TrimRight(msg, "\n") || got.Subject() != "feat: first" || len(got.Parents) != 0 {
		t.Fatalf("message = %q, parents = %v", got.Message, got.Parents)
	}

	// A signature without a date takes it from the injected clock.
	h.Write(t, r, "a.txt", "two\n")
	ok(r.Stage(ctx))
	c2 := must(r.Commit(ctx, gitx.CommitOptions{Message: "fix: second", Author: gitx.Signature{Name: alice.Name, Email: alice.Email}}))
	log = must(r.Log(ctx, "main"))
	if len(log) != 2 || log[0].Hash != c2 || log[1].Hash != c1 || log[0].Parents[0] != c1 {
		t.Fatalf("log = %+v", log)
	}
	if log[0].Author.When.Unix() != Now.Unix() || log[0].Committer.When.Unix() != Now.Unix() {
		t.Fatalf("dates = %v, %v; clock = %v", log[0].Author.When, log[0].Committer.When, Now)
	}
	if rng := must(r.Log(ctx, string(c1)+"..main")); len(rng) != 1 || rng[0].Hash != c2 {
		t.Fatalf("range log = %+v", rng)
	}
	if st := statusOf(t, r); len(st) != 0 {
		t.Fatalf("status after commit = %v", st)
	}

	if b := string(must(r.ReadFile(ctx, "main", "a.txt"))); b != "two\n" {
		t.Fatalf("a.txt = %q", b)
	}
	if b := string(must(r.ReadFile(ctx, string(c1), "a.txt"))); b != "one\n" {
		t.Fatalf("a.txt at c1 = %q", b)
	}
	if b := string(must(r.ReadFile(ctx, "HEAD", "a.txt"))); b != "two\n" {
		t.Fatalf("a.txt at HEAD = %q", b)
	}
	_, err = r.ReadFile(ctx, "main", "missing.txt")
	isErr(t, err, gitx.ErrFileNotFound)
	_, err = r.ReadFile(ctx, "nope", "a.txt")
	isErr(t, err, gitx.ErrRefNotFound)

	yes := must(r.IsAncestor(ctx, string(c1), "main"))
	no := must(r.IsAncestor(ctx, "main", string(c1)))
	if !yes || no {
		t.Fatalf("IsAncestor = %v, %v", yes, no)
	}

	ok(r.CreateBranch(ctx, "mesa/12-login", "main"))
	isErr(t, r.CreateBranch(ctx, "mesa/12-login", "main"), gitx.ErrBranchExists)
	isErr(t, r.CreateBranch(ctx, "other", "nope"), gitx.ErrRefNotFound)
	names := func() []string {
		var n []string
		for _, ref := range must(r.ListRefs(ctx, "refs/heads")) {
			n = append(n, ref.Name)
		}
		return n
	}
	if got, want := names(), []string{"refs/heads/main", "refs/heads/mesa/12-login"}; !slices.Equal(got, want) {
		t.Fatalf("branches = %v", got)
	}
	isErr(t, r.DeleteBranch(ctx, "main"), gitx.ErrBranchInUse)
	ok(r.DeleteBranch(ctx, "mesa/12-login"))
	isErr(t, r.DeleteBranch(ctx, "mesa/12-login"), gitx.ErrRefNotFound)

	ok(r.UpdateRef(ctx, "refs/mestre/recuperados/epoca-2/12", c1))
	refs := must(r.ListRefs(ctx, "refs/mestre"))
	if len(refs) != 1 || refs[0].Name != "refs/mestre/recuperados/epoca-2/12" || refs[0].Hash != c1 {
		t.Fatalf("refs = %+v", refs)
	}
	ok(r.DeleteRef(ctx, "refs/mestre/recuperados/epoca-2/12"))
	ok(r.DeleteRef(ctx, "refs/mestre/recuperados/epoca-2/12"))
	if refs := must(r.ListRefs(ctx, "refs/mestre")); len(refs) != 0 {
		t.Fatalf("refs after delete = %+v", refs)
	}
	isErr(t, r.UpdateRef(ctx, "-x", c1), gitx.ErrInvalidArgument)
}

func statusStageExcludeSkip(t *testing.T, h Harness) {
	ctx := context.Background()
	g := h.New(t)
	r := seed(t, h, g, filepath.Join(t.TempDir(), "repo"), map[string]string{"keep.txt": "keep\n", "hooks.json": "{}\n"})

	h.Write(t, r, "new.txt", "new\n")
	h.Write(t, r, "keep.txt", "changed\n")
	if got, want := statusOf(t, r), []string{" M keep.txt", "?? new.txt"}; !slices.Equal(got, want) {
		t.Fatalf("status = %q, want %q", got, want)
	}
	ok(r.Stage(ctx, "new.txt"))
	if got, want := statusOf(t, r), []string{" M keep.txt", "A  new.txt"}; !slices.Equal(got, want) {
		t.Fatalf("status = %q, want %q", got, want)
	}
	_, err := r.Commit(ctx, gitx.CommitOptions{Message: "feat: new", Author: alice})
	ok(err)
	h.Remove(t, r, "keep.txt")
	if got, want := statusOf(t, r), []string{" D keep.txt"}; !slices.Equal(got, want) {
		t.Fatalf("status = %q, want %q", got, want)
	}
	ok(r.Stage(ctx))
	if got, want := statusOf(t, r), []string{"D  keep.txt"}; !slices.Equal(got, want) {
		t.Fatalf("status = %q, want %q", got, want)
	}
	if err := r.Stage(ctx, "no-such-file"); err == nil {
		t.Fatal("staging a path that matches nothing succeeded")
	}

	// info/exclude block (docs/sessao-zero/workspace.md: /.mestre/).
	h.Write(t, r, ".mestre/run/state.json", "{}\n")
	if got := statusOf(t, r); !slices.Contains(got, "?? .mestre/run/state.json") {
		t.Fatalf("status = %q: .mestre should show before the exclude", got)
	}
	ok(r.SetExcludeBlock(ctx, "mestre", []string{"/.mestre/"}))
	if got := statusOf(t, r); slices.ContainsFunc(got, func(s string) bool { return strings.Contains(s, ".mestre") }) {
		t.Fatalf("status = %q: .mestre should be excluded", got)
	}
	if got := must(r.ExcludeBlock(ctx, "mestre")); !slices.Equal(got, []string{"/.mestre/"}) {
		t.Fatalf("block = %q", got)
	}
	ok(r.SetExcludeBlock(ctx, "mestre", []string{"/.mestre/run/", "*.tmp"}))
	if got := must(r.ExcludeBlock(ctx, "mestre")); !slices.Equal(got, []string{"/.mestre/run/", "*.tmp"}) {
		t.Fatalf("block after replace = %q", got)
	}
	ok(r.SetExcludeBlock(ctx, "mestre", nil))
	if got := must(r.ExcludeBlock(ctx, "mestre")); len(got) != 0 {
		t.Fatalf("block after removal = %q", got)
	}
	isErr(t, r.SetExcludeBlock(ctx, "bad name", []string{"x"}), gitx.ErrInvalidArgument)

	// skip-worktree keeps the Mestre part of a tracked file out of status.
	h.Write(t, r, "hooks.json", "{\"mestre\":true}\n")
	if got := statusOf(t, r); !slices.Contains(got, " M hooks.json") {
		t.Fatalf("status = %q", got)
	}
	ok(r.SetSkipWorktree(ctx, true, "hooks.json"))
	if got := statusOf(t, r); slices.ContainsFunc(got, func(s string) bool { return strings.HasSuffix(s, "hooks.json") }) {
		t.Fatalf("status = %q: hooks.json should be skipped", got)
	}
	if got := must(r.SkipWorktreePaths(ctx)); !slices.Equal(got, []string{"hooks.json"}) {
		t.Fatalf("skip-worktree paths = %q", got)
	}
	ok(r.SetSkipWorktree(ctx, false, "hooks.json"))
	if got := statusOf(t, r); !slices.Contains(got, " M hooks.json") {
		t.Fatalf("status = %q", got)
	}
	if got := must(r.SkipWorktreePaths(ctx)); len(got) != 0 {
		t.Fatalf("skip-worktree paths = %q", got)
	}
}

func mirrorAndWorktrees(t *testing.T, h Harness) {
	ctx := context.Background()
	g := h.New(t)
	root := t.TempDir()
	origin := seed(t, h, g, filepath.Join(root, "origin"), map[string]string{"README.md": "hello\n"})
	mainHash := must(origin.ResolveRef(ctx, "main"))

	mirror := must(g.Clone(ctx, origin.Dir(), filepath.Join(root, "espelho.git"), gitx.CloneOptions{Bare: true}))
	if got := must(mirror.ResolveRef(ctx, "main")); got != mainHash {
		t.Fatalf("mirror main = %s, want %s", got, mainHash)
	}
	wts := must(mirror.ListWorktrees(ctx))
	if len(wts) != 1 || !wts[0].Bare || canon(wts[0].Path) != canon(mirror.Dir()) {
		t.Fatalf("worktrees of a fresh mirror = %+v", wts)
	}
	_, err := mirror.Status(ctx)
	isErr(t, err, gitx.ErrBareRepo)
	_, err = mirror.Commit(ctx, gitx.CommitOptions{Message: "x", Author: alice})
	isErr(t, err, gitx.ErrBareRepo)

	// A mesa: worktree with its own branch, from the mirror.
	wtDir := filepath.Join(root, "mesas", "42-login")
	wt := must(mirror.AddWorktree(ctx, wtDir, gitx.WorktreeOptions{Branch: "epico/42-login", Create: true, StartPoint: "main"}))
	if canon(wt.Dir()) != canon(wtDir) {
		t.Fatalf("worktree dir = %s", wt.Dir())
	}
	_, err = mirror.AddWorktree(ctx, filepath.Join(root, "mesas", "other"), gitx.WorktreeOptions{Branch: "epico/42-login"})
	isErr(t, err, gitx.ErrBranchInUse)
	_, err = mirror.AddWorktree(ctx, filepath.Join(root, "mesas", "other"), gitx.WorktreeOptions{Branch: "epico/42-login", Create: true, StartPoint: "main"})
	isErr(t, err, gitx.ErrBranchExists)
	_, err = mirror.AddWorktree(ctx, wtDir, gitx.WorktreeOptions{Branch: "epico/43", Create: true, StartPoint: "main"})
	isErr(t, err, gitx.ErrPathExists)
	_, err = mirror.AddWorktree(ctx, filepath.Join(root, "mesas", "none"), gitx.WorktreeOptions{Branch: "no-such-branch"})
	isErr(t, err, gitx.ErrRefNotFound)
	_, err = mirror.AddWorktree(ctx, filepath.Join(root, "mesas", "none"), gitx.WorktreeOptions{Branch: "x", Create: true, StartPoint: "no-such-ref"})
	isErr(t, err, gitx.ErrRefNotFound)

	// Commits made in the worktree move the branch of the mirror.
	c := commitFile(t, h, wt, "login.go", "package login\n", "feat(login): add login\n\nMestre-Mesa: epico/42-login")
	if got := must(mirror.ResolveRef(ctx, "epico/42-login")); got != c {
		t.Fatalf("mirror branch = %s, want %s", got, c)
	}
	if got := must(wt.ResolveRef(ctx, "HEAD")); got != c {
		t.Fatalf("HEAD = %s, want %s", got, c)
	}
	wts = must(mirror.ListWorktrees(ctx))
	if len(wts) != 2 || wts[1].Branch != "epico/42-login" || wts[1].Head != c || canon(wts[1].Path) != canon(wtDir) || wts[1].Bare {
		t.Fatalf("worktrees = %+v", wts)
	}
	reopened := must(g.Open(ctx, wtDir))
	if got := must(reopened.ResolveRef(ctx, "HEAD")); got != c {
		t.Fatalf("reopened HEAD = %s", got)
	}
	_, err = g.Open(ctx, filepath.Join(root, "not-a-repo"))
	isErr(t, err, gitx.ErrNotARepo)

	isErr(t, mirror.DeleteBranch(ctx, "epico/42-login"), gitx.ErrBranchInUse)

	// Removing a worktree with uncommitted work needs force.
	h.Write(t, wt, "scratch.txt", "wip\n")
	isErr(t, mirror.RemoveWorktree(ctx, wtDir, false), gitx.ErrDirtyWorktree)
	ok(mirror.RemoveWorktree(ctx, wtDir, true))
	if wts := must(mirror.ListWorktrees(ctx)); len(wts) != 1 {
		t.Fatalf("worktrees after removal = %+v", wts)
	}
	isErr(t, mirror.RemoveWorktree(ctx, wtDir, true), gitx.ErrNoSuchWorktree)
	ok(mirror.DeleteBranch(ctx, "epico/42-login"))
	if _, err := mirror.ResolveRef(ctx, "epico/42-login"); !errors.Is(err, gitx.ErrRefNotFound) {
		t.Fatalf("deleted branch: %v", err)
	}

	// A clean worktree goes without force; an existing branch can be reused.
	wt2 := must(mirror.AddWorktree(ctx, filepath.Join(root, "mesas", "again"), gitx.WorktreeOptions{Branch: "main"}))
	if got := must(wt2.ResolveRef(ctx, "HEAD")); got != mainHash {
		t.Fatalf("HEAD = %s", got)
	}
	ok(mirror.RemoveWorktree(ctx, wt2.Dir(), false))
}

func mergeFastForwardOnly(t *testing.T, h Harness) {
	ctx := context.Background()
	g := h.New(t)
	root := t.TempDir()
	origin := seed(t, h, g, filepath.Join(root, "origin"), map[string]string{"f.txt": "base\n", "g.txt": "base\n"})
	mirror := must(g.Clone(ctx, origin.Dir(), filepath.Join(root, "espelho.git"), gitx.CloneOptions{Bare: true}))
	mesa := must(mirror.AddWorktree(ctx, filepath.Join(root, "cidadela"), gitx.WorktreeOptions{Branch: "mesa", Create: true, StartPoint: "main"}))
	torre := must(mirror.AddWorktree(ctx, filepath.Join(root, "torre"), gitx.WorktreeOptions{Branch: "torre", Create: true, StartPoint: "main"}))

	// The Torre commits; its tip arrives as a ref, the mesa fast-forwards.
	t1 := commitFile(t, h, torre, "t.txt", "from torre\n", "feat: torre work")
	ok(mirror.UpdateRef(ctx, "refs/mestre/torres/t1/mesa", t1))
	got := must(mesa.MergeFFOnly(ctx, "refs/mestre/torres/t1/mesa"))
	if got != t1 || must(mesa.ResolveRef(ctx, "HEAD")) != t1 || must(mirror.ResolveRef(ctx, "mesa")) != t1 {
		t.Fatalf("after ff: %s, want %s", got, t1)
	}
	if b := string(must(mesa.ReadFile(ctx, "HEAD", "t.txt"))); b != "from torre\n" {
		t.Fatalf("t.txt = %q", b)
	}
	if st := statusOf(t, mesa); len(st) != 0 {
		t.Fatalf("status after ff = %q", st)
	}
	if again := must(mesa.MergeFFOnly(ctx, "refs/mestre/torres/t1/mesa")); again != t1 {
		t.Fatalf("merge of what we already have = %s", again)
	}

	// Unrelated local work does not block it; work on the same file does.
	t2 := commitFile(t, h, torre, "f.txt", "torre edit\n", "fix: torre edit f")
	ok(mirror.UpdateRef(ctx, "refs/mestre/torres/t1/mesa", t2))
	h.Write(t, mesa, "g.txt", "local scratch\n")
	h.Write(t, mesa, "f.txt", "local edit\n")
	_, err := mesa.MergeFFOnly(ctx, "refs/mestre/torres/t1/mesa")
	isErr(t, err, gitx.ErrLocalChanges)
	if must(mesa.ResolveRef(ctx, "HEAD")) != t1 {
		t.Fatal("a refused merge moved HEAD")
	}
	h.Write(t, mesa, "f.txt", "base\n")
	if got := must(mesa.MergeFFOnly(ctx, "refs/mestre/torres/t1/mesa")); got != t2 {
		t.Fatalf("merge with unrelated local change = %s, want %s", got, t2)
	}
	if st := statusOf(t, mesa); !slices.Equal(st, []string{" M g.txt"}) {
		t.Fatalf("status = %q", st)
	}

	// Divergence: nothing changes.
	ok(mesa.Stage(ctx))
	m3 := must(mesa.Commit(ctx, gitx.CommitOptions{Message: "feat: cidadela work", Author: alice}))
	t3 := commitFile(t, h, torre, "other.txt", "x\n", "feat: torre other")
	ok(mirror.UpdateRef(ctx, "refs/mestre/torres/t1/mesa", t3))
	_, err = mesa.MergeFFOnly(ctx, "refs/mestre/torres/t1/mesa")
	isErr(t, err, gitx.ErrNotFastForward)
	if must(mesa.ResolveRef(ctx, "HEAD")) != m3 {
		t.Fatal("a refused merge moved HEAD")
	}
	left := must(mirror.Log(ctx, "refs/mestre/torres/t1/mesa.."+"mesa"))
	right := must(mirror.Log(ctx, "mesa..refs/mestre/torres/t1/mesa"))
	if len(left) != 1 || left[0].Hash != m3 || len(right) != 1 || right[0].Hash != t3 {
		t.Fatalf("divergence = %+v | %+v", left, right)
	}
	_, err = mesa.MergeFFOnly(ctx, "refs/mestre/no-such")
	isErr(t, err, gitx.ErrRefNotFound)
}

func bundles(t *testing.T, h Harness) {
	ctx := context.Background()
	g := h.New(t)
	root := t.TempDir()
	a := seed(t, h, g, filepath.Join(root, "a"), map[string]string{"a.txt": "1\n"})
	c1 := must(a.ResolveRef(ctx, "main"))
	c2 := commitFile(t, h, a, "a.txt", "2\n", "feat: two")

	full := filepath.Join(root, "full.bundle")
	ok(a.CreateBundle(ctx, full, "main"))
	info := must(gitx.ReadBundle(full))
	if len(info.Refs) != 1 || info.Refs[0].Name != "refs/heads/main" || info.Refs[0].Hash != c2 || len(info.Prerequisites) != 0 {
		t.Fatalf("full bundle header = %+v", info)
	}

	b := must(g.Init(ctx, filepath.Join(root, "b.git"), gitx.InitOptions{Bare: true, InitialBranch: "main"}))
	ok(b.VerifyBundle(ctx, full))
	ok(b.FetchBundle(ctx, full, "refs/heads/main:refs/mestre/torres/t1/main"))
	if got := must(b.ResolveRef(ctx, "refs/mestre/torres/t1/main")); got != c2 {
		t.Fatalf("fetched = %s, want %s", got, c2)
	}
	if got := string(must(b.ReadFile(ctx, "refs/mestre/torres/t1/main", "a.txt"))); got != "2\n" {
		t.Fatalf("a.txt = %q", got)
	}
	ok(b.FetchBundle(ctx, full, "refs/heads/main:refs/mestre/torres/t1/main")) // idempotent

	// Incremental: only what b does not have, based on the last known commit.
	c3 := commitFile(t, h, a, "a.txt", "3\n", "feat: three")
	inc := filepath.Join(root, "inc.bundle")
	ok(a.CreateBundle(ctx, inc, string(c2)+"..main"))
	info = must(gitx.ReadBundle(inc))
	if len(info.Refs) != 1 || info.Refs[0].Hash != c3 || !slices.Contains(info.Prerequisites, c2) {
		t.Fatalf("incremental header = %+v", info)
	}
	ok(b.FetchBundle(ctx, inc, "refs/heads/main:refs/mestre/torres/t1/main"))
	if got := must(b.ResolveRef(ctx, "refs/mestre/torres/t1/main")); got != c3 {
		t.Fatalf("after incremental = %s, want %s", got, c3)
	}
	if log := must(b.Log(ctx, "refs/mestre/torres/t1/main")); len(log) != 3 || log[2].Hash != c1 {
		t.Fatalf("history on b = %+v", log)
	}

	// A repository without the base cannot apply it.
	empty := must(g.Init(ctx, filepath.Join(root, "empty.git"), gitx.InitOptions{Bare: true}))
	isErr(t, empty.VerifyBundle(ctx, inc), gitx.ErrMissingPrerequisite)
	isErr(t, empty.FetchBundle(ctx, inc, "refs/heads/main:refs/heads/main"), gitx.ErrMissingPrerequisite)
	if refs := must(empty.ListRefs(ctx)); len(refs) != 0 {
		t.Fatalf("refs after refused bundle = %+v", refs)
	}

	// Nothing new: no bundle.
	isErr(t, a.CreateBundle(ctx, filepath.Join(root, "none.bundle"), string(c3)+"..main"), gitx.ErrEmptyBundle)
	isErr(t, a.CreateBundle(ctx, filepath.Join(root, "none.bundle"), "no-such-branch"), gitx.ErrRefNotFound)
	isErr(t, a.CreateBundle(ctx, filepath.Join(root, "none.bundle")), gitx.ErrInvalidArgument)

	// Garbage is not a bundle.
	bad := filepath.Join(root, "bad.bundle")
	ok(os.WriteFile(bad, []byte("not a bundle\n"), 0o644))
	isErr(t, b.VerifyBundle(ctx, bad), gitx.ErrInvalidBundle)
	isErr(t, b.FetchBundle(ctx, bad, "refs/heads/main:refs/heads/x"), gitx.ErrInvalidBundle)
	isErr(t, b.FetchBundle(ctx, full), gitx.ErrInvalidArgument)

	// A ref moves only forward unless the refspec forces it.
	other := seed(t, h, g, filepath.Join(root, "other"), map[string]string{"z.txt": "z\n"})
	div := filepath.Join(root, "div.bundle")
	ok(other.CreateBundle(ctx, div, "main"))
	isErr(t, b.FetchBundle(ctx, div, "refs/heads/main:refs/mestre/torres/t1/main"), gitx.ErrNotFastForward)
	if got := must(b.ResolveRef(ctx, "refs/mestre/torres/t1/main")); got != c3 {
		t.Fatalf("refused fetch moved the ref to %s", got)
	}
	ok(b.FetchBundle(ctx, div, "+refs/heads/main:refs/mestre/torres/t1/main"))
	if got, want := must(b.ResolveRef(ctx, "refs/mestre/torres/t1/main")), must(other.ResolveRef(ctx, "main")); got != want {
		t.Fatalf("forced fetch = %s, want %s", got, want)
	}
	isErr(t, b.FetchBundle(ctx, div, "refs/heads/nope:refs/heads/x"), gitx.ErrRefNotFound)
}

func fetchAndConfig(t *testing.T, h Harness) {
	ctx := context.Background()
	g := h.New(t)
	root := t.TempDir()
	origin := seed(t, h, g, filepath.Join(root, "origin"), map[string]string{"a.txt": "1\n"})
	mirror := must(g.Clone(ctx, origin.Dir(), filepath.Join(root, "espelho.git"), gitx.CloneOptions{Bare: true}))

	if _, err := mirror.GetConfig(ctx, "mestre.test"); !errors.Is(err, gitx.ErrConfigNotSet) {
		t.Fatalf("unset key: %v", err)
	}
	ok(mirror.SetConfig(ctx, "mestre.test", "value one"))
	if got := must(mirror.GetConfig(ctx, "mestre.test")); got != "value one" {
		t.Fatalf("config = %q", got)
	}
	ok(mirror.SetConfig(ctx, "remote.origin.url", origin.Dir()))
	ok(mirror.SetConfig(ctx, "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*"))

	c2 := commitFile(t, h, origin, "a.txt", "2\n", "feat: more")
	ok(mirror.Fetch(ctx, "origin", "+refs/heads/*:refs/remotes/origin/*"))
	if got := must(mirror.ResolveRef(ctx, "refs/remotes/origin/main")); got != c2 {
		t.Fatalf("origin/main = %s, want %s", got, c2)
	}
	if got := must(mirror.ResolveRef(ctx, "main")); got == c2 {
		t.Fatal("a fetch into refs/remotes moved the local branch")
	}

	c3 := commitFile(t, h, origin, "a.txt", "3\n", "feat: even more")
	ok(mirror.Fetch(ctx, origin.Dir(), "refs/heads/main:refs/remotes/origin/main"))
	if got := must(mirror.ResolveRef(ctx, "refs/remotes/origin/main")); got != c3 {
		t.Fatalf("origin/main = %s, want %s", got, c3)
	}
	isErr(t, mirror.Fetch(ctx, origin.Dir(), "refs/heads/nope:refs/remotes/origin/nope"), gitx.ErrRefNotFound)
	isErr(t, mirror.Fetch(ctx, "-bad"), gitx.ErrInvalidArgument)
}
