package safefile_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lucasndas/mestre/internal/safefile"
)

// MkdirAll makes folders the way Write does: missing ones are made, the same
// call twice changes nothing, and a link or a file in the way is refused with
// nothing created behind it.
func TestMkdirAllMakesFoldersAndRefusesLinks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c")
	for range 2 {
		if err := safefile.MkdirAll(deep); err != nil {
			t.Fatal(err)
		}
	}
	if fi, err := os.Stat(deep); err != nil || !fi.IsDir() {
		t.Fatalf("folder = %v, %v", fi, err)
	}
	outside := t.TempDir()
	symlink(t, outside, filepath.Join(root, "link"))
	if err := safefile.MkdirAll(filepath.Join(root, "link")); !errors.Is(err, safefile.ErrLink) {
		t.Fatalf("MkdirAll of a link = %v, want ErrLink", err)
	}
	if err := safefile.MkdirAll(filepath.Join(root, "link", "new")); !errors.Is(err, safefile.ErrLink) {
		t.Fatalf("MkdirAll through a link = %v, want ErrLink", err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("the link was followed: %v", entries)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := safefile.MkdirAll(file); err == nil {
		t.Fatal("MkdirAll over a file = nil")
	}
	if err := safefile.MkdirAll("relative/dir"); !errors.Is(err, safefile.ErrPath) {
		t.Fatalf("MkdirAll of a relative path = %v, want ErrPath", err)
	}
}
