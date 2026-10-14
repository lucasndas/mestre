//go:build !windows

package hook_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/lucasndas/mestre/internal/hook"
)

func mkfifo(path string) error { return syscall.Mkfifo(path, 0o600) }

// The file the dispatcher leaves in the queue is readable by the engine
// through the group (0640) whatever the umask of the agent's shell. Not
// parallel: the umask is the process's.
func TestAQueueFileIsReadableByTheEngineWhateverTheUmask(t *testing.T) { //nolint:paralleltest // the umask is the process's
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	path := filepath.Join(t.TempDir(), "x.json")
	if err := (hook.OSFiles{}).CreateNew(path, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v (%v); want 0640", st.Mode().Perm(), err)
	}
}
