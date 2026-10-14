//go:build full && windows

package providers_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// shellCommand runs a hook line the way Claude Code does on Windows: with
// the bash of Git for Windows (bash -c), which it requires for a command
// hook. The bash.exe of System32 is WSL's, not Git's: it is never used.
func shellCommand(t *testing.T, line string) *exec.Cmd {
	t.Helper()
	var candidates []string
	if git, err := exec.LookPath("git"); err == nil {
		root := filepath.Dir(filepath.Dir(git)) // <Git>\cmd\git.exe or <Git>\bin\git.exe
		candidates = append(candidates, filepath.Join(root, "bin", "bash.exe"), filepath.Join(root, "usr", "bin", "bash.exe"))
	}
	candidates = append(candidates, filepath.Join(os.Getenv("ProgramFiles"), "Git", "bin", "bash.exe"))
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return exec.Command(c, "-c", line)
		}
	}
	t.Skip("no Git Bash on this machine: Claude Code needs it for its hooks on Windows")
	return nil
}
