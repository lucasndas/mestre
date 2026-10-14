//go:build full && !windows

package classes_test

import (
	"path/filepath"
	"syscall"
	"testing"
)

// A FIFO in place of a script is changed, and reading it does not wait for
// a writer.
func TestTableScriptsFIFOIsChangedWithoutBlocking(t *testing.T) {
	t.Parallel()
	tr := newTableRepo(t)
	if err := syscall.Mkfifo(filepath.Join(tr.wtDir, "scripts", "fifo.sh"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !tr.changed(t, "scripts/fifo.sh") {
		t.Fatal("a FIFO counted as a script of the repository")
	}
}
