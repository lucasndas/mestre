//go:build !windows

// Linux only: file modes and owners are how the key is protected on Linux.
// The Windows pair is keyfile_windows_test.go (DPAPI and the file ACL); the
// real service accounts are CA-COFRE-08, in the heavy suite.

package vault

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CA-COFRE-02
func TestKeyFileWiderThan0600IsRefused(t *testing.T) {
	t.Parallel()
	keyPath := filepath.Join(t.TempDir(), "cofre.key")
	f := newFixture(t, NewFileKeyStore(keyPath))
	v := f.create(t)
	put(t, v, "a", TypeProviderAPIKey, "sk-value-aaaaaaaaaaaaaaaa", nil)
	if fi, err := os.Stat(keyPath); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file after Create: %v, %v; want mode 0600", fi.Mode(), err)
	}
	if err := f.open(t).Err(); err != nil {
		t.Fatalf("open with a 0600 key file: %v", err)
	}

	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatal(err)
	}
	v2 := f.open(t)
	assertClosed(t, f, v2, ErrClosed)
	var unsafe *UnsafeKeyFileError
	if err := v2.Err(); !errors.As(err, &unsafe) || !strings.Contains(err.Error(), "0644") {
		t.Fatalf("Err = %v, want the unsafe key file error naming mode 0644", err)
	}
	if a := f.alertList()[0]; a.Check != "LIN-05" {
		t.Errorf("alert check = %q, want LIN-05", a.Check)
	}
}

func TestKeyFileSymlinkIsRefused(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := NewFileKeyStore(filepath.Join(dir, "real.key"))
	if err := target.Save(t.Context(), []byte("x")); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "cofre.key")
	if err := os.Symlink(target.Path(), link); err != nil {
		t.Fatal(err)
	}
	var unsafe *UnsafeKeyFileError
	if _, err := NewFileKeyStore(link).Load(t.Context()); !errors.As(err, &unsafe) {
		t.Errorf("Load through a symlink = %v, want refusal", err)
	}
	if _, err := NewFileKeyStore(filepath.Join(dir, "none.key")).Load(t.Context()); !errors.Is(err, ErrNoKey) {
		t.Errorf("Load of a missing file = %v, want ErrNoKey", err)
	}
}

func TestKeyFileOfAnotherOwnerIsRefused(t *testing.T) {
	t.Parallel()
	s := NewFileKeyStore(filepath.Join(t.TempDir(), "cofre.key"))
	if err := s.Save(t.Context(), []byte("x")); err != nil {
		t.Fatal(err)
	}
	s.uid++ // the engine runs as another account than the file owner
	var unsafe *UnsafeKeyFileError
	if _, err := s.Load(t.Context()); !errors.As(err, &unsafe) || !strings.Contains(err.Error(), "owned by") {
		t.Errorf("Load = %v, want refusal for another owner", err)
	}
}
