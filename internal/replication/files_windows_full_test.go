//go:build full && windows

package replication_test

import (
	"fmt"
	"os/exec"
	"testing"
)

// CA-REPL-08
// A junction is a link to a folder that needs no privilege, and Go does not
// take it for a symbolic link (os.ModeSymlink is not set): the folders of the
// replication must not follow it out of their root either.
func TestAFileNeverLeavesItsFolderThroughAJunction(t *testing.T) {
	t.Parallel()
	fileEscapeScenario(t, func(target, name string) error {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", name, target).CombinedOutput(); err != nil {
			return fmt.Errorf("mklink /J: %v: %s", err, out)
		}
		return nil
	})
}
