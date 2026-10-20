//go:build full && windows

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
	p := filepath.Join(t.TempDir(), "mestre.bat")
	if err := os.WriteFile(p, []byte("@echo RAN\r\n@echo x> \"%~dp0mestre.bat.ran\"\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// antigravityShell runs a hook line as agy 1.4.2 does on Windows: cmd /c with
// the quoting of Go, which turns every " of the line into \" (a line with
// quotes breaks here as it breaks in agy).
func antigravityShell(_ *testing.T, line string) *exec.Cmd {
	return exec.Command("cmd", "/c", line)
}

// claudeShell runs a hook line the way Claude Code does on Windows: with the
// bash of Git for Windows. Without it the case is left out (nil).
func claudeShell(t *testing.T, line string) *exec.Cmd {
	t.Helper()
	var candidates []string
	if git, err := exec.LookPath("git"); err == nil {
		root := filepath.Dir(filepath.Dir(git))
		candidates = append(candidates, filepath.Join(root, "bin", "bash.exe"), filepath.Join(root, "usr", "bin", "bash.exe"))
	}
	candidates = append(candidates, filepath.Join(os.Getenv("ProgramFiles"), "Git", "bin", "bash.exe"))
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return exec.Command(c, "-c", line)
		}
	}
	t.Log("no Git Bash on this machine: the Claude Code line is not run")
	return nil
}
