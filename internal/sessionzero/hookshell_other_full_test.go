//go:build full && !windows

package sessionzero_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// stubMestre is a `mestre` that says it ran and leaves a marker next to
// itself (<binary>.ran).
func stubMestre(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mestre")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho RAN\n: > '"+p+".ran'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// claudeShell and antigravityShell run a hook line the way each provider does
// outside Windows: sh -c.
func claudeShell(_ *testing.T, line string) *exec.Cmd      { return exec.Command("sh", "-c", line) }
func antigravityShell(_ *testing.T, line string) *exec.Cmd { return exec.Command("sh", "-c", line) }
