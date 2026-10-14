//go:build full

package conformance

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// A folder of the agent account that leads elsewhere is refused on the way
// to a file the engine reads or removes: on Windows a junction, which any
// account creates without a privilege (mklink /J), on Linux a symbolic link
// (M3 audit).
func TestNoLinkPathRefusesAJunction(t *testing.T) {
	t.Parallel()
	root, elsewhere := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "rollout-z.jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "real"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "real", "rollout-z.jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "2026")
	var err error
	if runtime.GOOS == "windows" {
		err = exec.Command("cmd", "/c", "mklink", "/J", link, elsewhere).Run()
	} else {
		err = os.Symlink(elsewhere, link)
	}
	if err != nil {
		t.Fatal(err)
	}
	if p, err := noLinkPath(root, "2026", "rollout-z.jsonl"); err == nil {
		t.Errorf("through the link: %s", p)
	}
	if _, err := noLinkPath(root, "real", "rollout-z.jsonl"); err != nil {
		t.Errorf("a plain path: %v", err)
	}
	for _, bad := range [][]string{{".."}, {"real", ".."}, {"a/b"}, {`a\b`}, {""}, {"."}} {
		if p, err := noLinkPath(root, bad...); err == nil {
			t.Errorf("%q was accepted: %s", bad, p)
		}
	}
}
