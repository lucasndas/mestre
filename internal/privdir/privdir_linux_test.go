package privdir

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsure(t *testing.T) {
	t.Parallel()
	mode := func(t *testing.T, p string) os.FileMode {
		t.Helper()
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		return fi.Mode().Perm()
	}

	t.Run("makes it private", func(t *testing.T) {
		t.Parallel()
		d := filepath.Join(t.TempDir(), "a", "b")
		if err := Ensure(d); err != nil {
			t.Fatal(err)
		}
		if m := mode(t, d); m != 0o700 {
			t.Errorf("mode = %o", m)
		}
	})
	t.Run("fixes a folder that was already there with another mode", func(t *testing.T) {
		t.Parallel()
		d := filepath.Join(t.TempDir(), "pty")
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(d, 0o777); err != nil { // the umask may have taken some
			t.Fatal(err)
		}
		if err := Ensure(d); err != nil {
			t.Fatal(err)
		}
		if m := mode(t, d); m != 0o700 {
			t.Errorf("mode = %o, want 700", m)
		}
	})
	t.Run("refuses a link in its place", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		target := filepath.Join(root, "elsewhere")
		if err := os.Mkdir(target, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "pty")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(link); err == nil {
			t.Error("a link to a folder was taken for the folder")
		}
		if m := mode(t, target); m != 0o755 {
			t.Errorf("the mode of the target changed to %o", m)
		}
	})
	t.Run("refuses a file", func(t *testing.T) {
		t.Parallel()
		f := filepath.Join(t.TempDir(), "pty")
		if err := os.WriteFile(f, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(f); err == nil {
			t.Error("a file was taken for a folder")
		}
	})
}
