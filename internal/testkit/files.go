package testkit

import (
	"os"
	"path/filepath"
	"testing"
)

// WriteFiles creates files (relative path -> content) under dir, with the
// parent folders, and returns dir.
func WriteFiles(t testing.TB, dir string, files map[string]string) string {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
