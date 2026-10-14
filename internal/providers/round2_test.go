package providers_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
)

// N-B regression: NormalizePath follows a dangling symlink to the target a
// write through it would create, so the permission decision sees the target
// (docs/classes/formato-das-permissoes.md). A chain of dangling links is
// followed, bounded, and a relative target is resolved from the link's
// folder.
func TestRound2NormalizeFollowsDanglingLink(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(root, "wt")
	if err := os.MkdirAll(filepath.Join(wt, "permitido"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "secret"), 0o755); err != nil {
		t.Fatal(err)
	}
	mk := func(target, name string) {
		if err := os.Symlink(target, filepath.Join(wt, name)); err != nil {
			t.Skipf("no symbolic links here: %v", err)
		}
	}
	mk(filepath.Join(root, "secret", "new.txt"), "permitido/dangling")     // absolute target
	mk(filepath.Join(root, "secret", "nodir", "f"), "permitido/dangling2") // target dir missing too
	mk("dangling", "permitido/chain")                                      // link to a dangling link
	mk("../.git/hooks/pre-commit", "permitido/rel")                        // relative target
	windows := runtime.GOOS == "windows"
	o := providers.Options{}
	// Windows takes the ".." out of the text before it opens the path, and
	// folds case; elsewhere a ".." after a link is next to its target.
	dotdot := filepath.Join(root, "secret", "z")
	if windows {
		dotdot = filepath.Join(wt, "permitido", "z")
	}
	for _, c := range []struct{ in, want string }{
		{"permitido/dangling", filepath.Join(root, "secret", "new.txt")},
		{filepath.FromSlash("permitido/dangling/../z"), dotdot},
		{"permitido/dangling2", filepath.Join(root, "secret", "nodir", "f")},
		{"permitido/chain", filepath.Join(root, "secret", "new.txt")},
		{"permitido/rel", filepath.Join(wt, ".git", "hooks", "pre-commit")},
	} {
		want := c.want
		if windows {
			want = strings.ToLower(want)
		}
		if got := o.NormalizePath(filepath.FromSlash(c.in), wt); got != want {
			t.Errorf("NormalizePath(%s) = %s; want %s", c.in, got, want)
		}
	}
}
