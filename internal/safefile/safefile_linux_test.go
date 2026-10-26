package safefile_test

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/mestre-ia/mestre/internal/safefile"
)

// A FIFO in place of teto.yaml would block a plain read forever; a device
// like /dev/zero would never end. Both are refused without reading.
func TestSpecialFilesAreRefusedWithoutBlocking(t *testing.T) {
	t.Parallel()
	fifo := filepath.Join(t.TempDir(), "teto.yaml")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := safefile.Read(fifo, 0); !errors.Is(err, safefile.ErrNotRegular) {
		t.Fatalf("read of a FIFO = %v, want ErrNotRegular", err)
	}
	if _, _, err := safefile.Read("/dev/zero", 0); !errors.Is(err, safefile.ErrNotRegular) {
		t.Fatalf("read of /dev/zero = %v, want ErrNotRegular", err)
	}
}

// The new file takes the mode of the rule and the group of its folder,
// whatever mode the old one had (agy writes its settings.json 0600).
func TestWriteSetsTheModeOfTheRule(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := safefile.Write(path, []byte(`{"a":1}`), safefile.Options{Sync: true}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o664 {
		t.Fatalf("mode = %o, want 0664", fi.Mode().Perm())
	}
	dir, _ := os.Stat(filepath.Dir(path))
	if fi.Sys().(*syscall.Stat_t).Gid != dir.Sys().(*syscall.Stat_t).Gid {
		t.Fatal("the file does not have the group of its folder")
	}
}
