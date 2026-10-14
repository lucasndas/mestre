//go:build full

package providers_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
)

// This test binary is also the `mestre` the hook lines call: called with
// "hook" as the first argument it prints its arguments and exits with
// HELPER_EXIT.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "hook" {
		fmt.Println("ARGS " + strings.Join(os.Args[1:], " "))
		code, _ := strconv.Atoi(os.Getenv("HELPER_EXIT"))
		os.Exit(code)
	}
	os.Exit(m.Run())
}

// hookLine is the command of event in the hook file HookFiles gives.
func hookLine(t *testing.T, binary, event string) string {
	t.Helper()
	root := t.TempDir()
	files, err := providers.NewClaudeCode(providers.Options{}).HookFiles(providers.HookFilesRequest{OS: runtime.GOOS, Root: root, Binary: binary})
	if err != nil {
		t.Fatal(err)
	}
	h := mapOf(object(t, apply(t, files[0], nil))["hooks"])
	return mapOf(list(mapOf(list(h[event])[0])["hooks"])[0])["command"].(string)
}

// run executes line in the shell of the system with env (the environment
// of the process without the variables of the Mestre, plus extra).
func run(t *testing.T, line string, extra ...string) (stdout string, exit int) {
	t.Helper()
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, "MESTRE_") || strings.HasPrefix(kv, "HELPER_")
	})
	c := shellCommand(line)
	var out bytes.Buffer
	c.Stdout, c.Stderr, c.Stdin, c.Env = &out, io.Discard, strings.NewReader(`{}`), append(env, extra...)
	err := c.Run()
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		return out.String(), ee.ExitCode()
	case err != nil:
		t.Fatalf("running %q: %v", line, err)
	}
	return out.String(), 0
}

// The line of a hook file, run by the shell of the system: it calls `mestre
// hook` with the arguments of the event when the character and the binary
// exist, passes the exit code of the call (2 blocks), and otherwise answers
// nothing with exit 0, as the clone of a folder without the Mestre needs.
func TestHookLineInTheShellOfTheSystem(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	spaced := filepath.Join(t.TempDir(), "with space")
	if err := os.MkdirAll(spaced, 0o755); err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(spaced, filepath.Base(self))
	b, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copied, b, 0o755); err != nil {
		t.Fatal(err)
	}
	const args = "ARGS hook --provider claude-code --event PreToolUse"
	missing := filepath.Join(t.TempDir(), "no-such-mestre")

	tests := []struct {
		name    string
		binary  string // the stable path given to HookFiles
		env     []string
		stdout  string
		exit    int
		skipLoc bool
	}{
		{"no MESTRE_AGENT_ID", "", []string{"MESTRE_CLI=" + self}, "", 0, false},
		{"the character, by MESTRE_CLI", "", []string{"MESTRE_AGENT_ID=a1", "MESTRE_CLI=" + self}, args, 0, false},
		{"the call blocks with exit 2", "", []string{"MESTRE_AGENT_ID=a1", "MESTRE_CLI=" + self, "HELPER_EXIT=2"}, args, 2, false},
		{"MESTRE_CLI names a path with a space", "", []string{"MESTRE_AGENT_ID=a1", "MESTRE_CLI=" + copied}, args, 0, false},
		{"MESTRE_CLI names no binary", "", []string{"MESTRE_AGENT_ID=a1", "MESTRE_CLI=" + missing}, "", 0, false},
		{"the stable binary", self, []string{"MESTRE_AGENT_ID=a1"}, args, 0, false},
		{"the stable binary with a space", copied, []string{"MESTRE_AGENT_ID=a1"}, args, 0, false},
		{"the stable binary is missing", missing, []string{"MESTRE_AGENT_ID=a1"}, "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out, exit := run(t, hookLine(t, tt.binary, "PreToolUse"), tt.env...)
			if got := strings.TrimSpace(out); got != tt.stdout || exit != tt.exit {
				t.Errorf("stdout %q, exit %d; want %q, %d", got, exit, tt.stdout, tt.exit)
			}
		})
	}
	// With the Mestre's variable unset and no `mestre` in the PATH, the line
	// that looks for `mestre` finds nothing and says nothing.
	if _, err := exec.LookPath("mestre"); err != nil {
		if out, exit := run(t, hookLine(t, "", "PreToolUse"), "MESTRE_AGENT_ID=a1"); strings.TrimSpace(out) != "" || exit != 0 {
			t.Errorf("without MESTRE_CLI and without mestre in the PATH: stdout %q, exit %d", out, exit)
		}
	}
}
