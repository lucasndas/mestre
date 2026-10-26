//go:build linux

package safefile_test

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/mestre-ia/mestre/internal/safefile"
)

func TestRemoveEntryTakesAFifoAndALinkButNotAFolder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink("/etc/passwd", link); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{fifo, link, filepath.Join(dir, "missing")} {
		if err := safefile.RemoveEntry(p); err != nil {
			t.Errorf("RemoveEntry(%s) = %v", p, err)
		}
	}
	if _, err := os.Lstat(fifo); err == nil {
		t.Error("the fifo is still there")
	}
	if _, err := os.Lstat(link); err == nil {
		t.Error("the link is still there")
	}
	if _, err := os.Stat("/etc/passwd"); err != nil {
		t.Errorf("the target of the link was touched: %v", err)
	}
	if err := safefile.RemoveEntry(sub); !errors.Is(err, safefile.ErrIsFolder) {
		t.Errorf("RemoveEntry(folder) = %v, want ErrIsFolder", err)
	}
}
