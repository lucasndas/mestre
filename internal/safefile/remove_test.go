package safefile_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/lucasndas/mestre/internal/safefile"
)

func TestRemove(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "a", "f")
	if err := safefile.Write(file, []byte("x"), safefile.Options{}); err != nil {
		t.Fatal(err)
	}
	stop := errors.New("not this one")
	if err := safefile.Remove(file, func(safefile.Info) error { return stop }); !errors.Is(err, stop) {
		t.Fatalf("remove stopped by the guard = %v", err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("the guard did not keep the file: %v", err)
	}
	if err := safefile.Remove(file, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the file is still there: %v", err)
	}
	if err := safefile.Remove(file, nil); err != nil {
		t.Fatalf("remove of a missing file = %v", err)
	}
	if err := safefile.Remove(filepath.Join(dir, "none", "f"), nil); err != nil {
		t.Fatalf("remove under a missing folder = %v", err)
	}
	if err := safefile.Remove(filepath.Join(dir, "a"), nil); !errors.Is(err, safefile.ErrNotRegular) {
		t.Fatalf("remove of a folder = %v, want ErrNotRegular", err)
	}
}

// A link is never followed: the one in place of the file is refused, the one
// in place of a folder too, and the engine file behind it stays.
func TestRemoveRefusesLinks(t *testing.T) {
	t.Parallel()
	secret, content := secretFile(t)
	wt := t.TempDir()
	link := filepath.Join(wt, "f")
	symlink(t, secret, link)
	if err := safefile.Remove(link, nil); !errors.Is(err, safefile.ErrNotRegular) && !errors.Is(err, safefile.ErrLink) {
		t.Fatalf("remove of a link = %v", err)
	}
	symlink(t, filepath.Dir(secret), filepath.Join(wt, ".mestre"))
	if err := safefile.Remove(filepath.Join(wt, ".mestre", filepath.Base(secret)), nil); !errors.Is(err, safefile.ErrLink) {
		t.Fatalf("remove through a linked folder = %v, want ErrLink", err)
	}
	checkSecret(t, secret, content)
}
