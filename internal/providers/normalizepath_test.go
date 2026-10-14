package providers_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
)

// A ".." after a link is resolved where the system resolves it: next to the
// folder the link points to, not next to the link (the write of
// permitido/lnk/../x lands outside the worktree, so the decision must see
// it there).
func TestNormalizePathResolvesTheLinkBeforeTheDotDot(t *testing.T) {
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
	fold := func(p string) string {
		if runtime.GOOS == "windows" {
			return strings.ToLower(p)
		}
		return p
	}
	o := providers.Options{}
	sep := string(filepath.Separator)
	for in, want := range map[string]string{
		"permitido" + sep + "lnk" + sep + ".." + sep + "x.txt":                      filepath.Join(root, "secret", "x.txt"),
		"permitido" + sep + "lnk" + sep + ".." + sep + ".." + sep + "y":             filepath.Join(root, "y"),
		"permitido" + sep + "lnk" + sep + "novo" + sep + ".." + sep + "z":           filepath.Join(outside, "z"),
		"permitido" + sep + "novo" + sep + ".." + sep + "a.txt":                     filepath.Join(wt, "permitido", "a.txt"),
		"." + sep + "permitido" + sep + "." + sep + "b.txt":                         filepath.Join(wt, "permitido", "b.txt"),
		filepath.Join(wt, "permitido") + sep + "lnk" + sep + ".." + sep + "abs.txt": filepath.Join(root, "secret", "abs.txt"),
	} {
		if got := o.NormalizePath(in, wt); got != fold(want) {
			t.Errorf("NormalizePath(%s) = %s; want %s", in, got, fold(want))
		}
	}
	// The write lands where the normalized path says.
	p := filepath.Join(wt, "permitido", "lnk", "..", "w.txt")
	if err := os.WriteFile(wt+sep+"permitido"+sep+"lnk"+sep+".."+sep+"w.txt", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "secret", "w.txt")); err != nil {
		t.Fatalf("the system did not write next to the link's target: %v (cleaned path %s)", err, p)
	}
	if got := o.NormalizePath("permitido"+sep+"lnk"+sep+".."+sep+"w.txt", wt); got != fold(filepath.Join(root, "secret", "w.txt")) {
		t.Errorf("existing file: %s", got)
	}
}
