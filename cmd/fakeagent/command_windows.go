package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/lucasndas/mestre/internal/testkit/fakeagent"
)

// command runs line with cmd /s /c "<line>", the form that keeps quoted
// paths inside line intact (Go would escape them with backslashes, which
// cmd does not understand).
func command(ctx context.Context, line, dir string, env []string) *exec.Cmd {
	c := exec.CommandContext(ctx, "cmd")
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /s /c "` + line + `"`}
	c.Dir, c.Env = dir, env
	return c
}

// hookCommand runs a hook line with the shell each provider uses on Windows
// (docs/sessao-zero/mesa.md#verificar-na-implementação): Claude Code the
// bash of Git for Windows, Codex PowerShell (-NoProfile -Command, the shell
// it detects for the user), Antigravity cmd /c. The Antigravity case is built
// the way agy builds it, exec.Command("cmd", "/c", line): Go quotes the line
// as one argument (backslash-escaping its own quotes) and there is no /s, so
// a hook line that only works under the lenient cmd /s /c form fails here as
// it fails in the real agy.
func hookCommand(ctx context.Context, p fakeagent.Provider, line, dir string, env []string) *exec.Cmd {
	var c *exec.Cmd
	switch p {
	case fakeagent.Claude:
		c = exec.CommandContext(ctx, gitBash(), "-c", line)
	case fakeagent.Codex:
		c = exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", line)
	case fakeagent.Antigravity:
		c = exec.CommandContext(ctx, "cmd", "/c", line)
	default:
		return command(ctx, line, dir, env)
	}
	c.Dir, c.Env = dir, env
	return c
}

// gitBash is the bash.exe of Git for Windows, next to the git on the PATH
// or in Program Files; never the bash.exe of System32, which is WSL's.
func gitBash() string {
	var candidates []string
	if git, err := exec.LookPath("git"); err == nil {
		root := filepath.Dir(filepath.Dir(git)) // <Git>\cmd\git.exe or <Git>\bin\git.exe
		candidates = append(candidates, filepath.Join(root, "bin", "bash.exe"), filepath.Join(root, "usr", "bin", "bash.exe"))
	}
	candidates = append(candidates, filepath.Join(os.Getenv("ProgramFiles"), "Git", "bin", "bash.exe"))
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "bash.exe"
}
