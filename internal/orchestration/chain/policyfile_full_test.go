//go:build full

package chain_test

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
)

// Part of CA-APROV-POL-21: a folder on the way to the policy file that is a link
// is not followed. On Windows it is a junction, which any account makes (a
// symbolic link takes a privilege there); elsewhere, a symbolic link.
func TestAJunctionOnTheWayToThePolicyFileIsNotFollowed(t *testing.T) {
	t.Parallel()
	linkedFolderIsNotRead(t, func(target, link string) error {
		if runtime.GOOS != "windows" {
			return os.Symlink(target, link)
		}
		out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
		if err != nil {
			t.Fatalf("mklink /J: %v: %s", err, out)
		}
		return nil
	})
}
