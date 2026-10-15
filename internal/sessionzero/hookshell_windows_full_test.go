//go:build full && windows

package sessionzero_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
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

// antigravityShell runs a hook line with cmd /s /c "<line>", the form that
// keeps the quotes of the line intact.
func antigravityShell(_ *testing.T, line string) *exec.Cmd {
	c := exec.Command("cmd")
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /s /c "` + line + `"`}
	return c
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
