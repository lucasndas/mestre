package providers_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
)

// A ".." after a link is resolved where the system resolves it: on Linux
// next to the folder the link points to, not next to the link (the write of
// permitido/lnk/../x lands outside the worktree, so the decision must see
// it there); Windows takes the ".." out of the text before it opens the
// path.
func TestNormalizePathResolvesTheDotDotWhereTheSystemDoes(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(root, "wt")
	outside := filepath.Join(root, "secret", "deep")
	for _, d := range []string{filepath.Join(wt, "permitido"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(wt, "permitido", "lnk")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	windows := runtime.GOOS == "windows"
	fold := func(p string) string {
		if windows {
			return strings.ToLower(p)
		}
		return p
	}
	o := providers.Options{}
	sep := string(filepath.Separator)
	rel := func(parts ...string) string { return strings.Join(parts, sep) }
	for _, c := range []struct{ in, posix, windows string }{
		{rel("permitido", "lnk", "..", "x.txt"), filepath.Join(root, "secret", "x.txt"), filepath.Join(wt, "permitido", "x.txt")},
		{rel("permitido", "lnk", "..", "..", "y"), filepath.Join(root, "y"), filepath.Join(wt, "y")},
		{rel("permitido", "lnk", "novo", "..", "z"), filepath.Join(outside, "z"), filepath.Join(outside, "z")},
		{rel("permitido", "novo", "..", "a.txt"), filepath.Join(wt, "permitido", "a.txt"), filepath.Join(wt, "permitido", "a.txt")},
		{rel(".", "permitido", ".", "b.txt"), filepath.Join(wt, "permitido", "b.txt"), filepath.Join(wt, "permitido", "b.txt")},
		{rel(wt, "permitido", "lnk", "..", "abs.txt"), filepath.Join(root, "secret", "abs.txt"), filepath.Join(wt, "permitido", "abs.txt")},
	} {
		want := c.posix
		if windows {
			want = c.windows
		}
		if got := o.NormalizePath(c.in, wt); got != fold(want) {
			t.Errorf("NormalizePath(%s) = %s; want %s", c.in, got, fold(want))
		}
	}
	// The system writes where the normalized path says, before and after
	// the file exists.
	in := rel("permitido", "lnk", "..", "w.txt")
	before := o.NormalizePath(in, wt)
	if err := os.WriteFile(wt+sep+in, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(before); err != nil {
		t.Fatalf("the system did not write at %s: %v", before, err)
	}
	if after := o.NormalizePath(in, wt); after != before {
		t.Errorf("existing file: %s; before it existed: %s", after, before)
	}
}
