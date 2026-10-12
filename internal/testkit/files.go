package testkit

import (
	"net"
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

// LocalListener listens on a free 127.0.0.1 port, closed at cleanup. It is
// real network, so only the full and heavy suites may use it; the fast suite
// uses the fake network (internal/testkit/fakenet).
func LocalListener(t testing.TB, suite string) net.Listener {
	t.Helper()
	if suite == "fast" {
		t.Fatal("real network in fast suite: use the fake network (internal/testkit/fakenet)")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}
