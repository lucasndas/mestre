//go:build full

package providers_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
)

// The line of the Codex hooks, run by the shell Codex runs it with ($SHELL
// -c on Linux, PowerShell -NoProfile -Command on Windows): it calls `mestre
// hook` with the arguments of the event and passes the exit code on, the 2
// that blocks included, also from a path with a blank (TestMain of
// claude_hookline_full_test.go is the `mestre`).
func TestCodexHookLineInTheShellOfCodex(t *testing.T) {
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
	shell := func(line string) *exec.Cmd { return exec.Command("sh", "-c", line) }
	if runtime.GOOS == "windows" {
		ps, err := exec.LookPath("powershell")
		if err != nil {
			t.Skip("no PowerShell")
		}
		shell = func(line string) *exec.Cmd { return exec.Command(ps, "-NoProfile", "-Command", line) }
	}
	for _, bin := range []string{self, copied} {
		files, err := providers.NewCodex(providers.Options{}).HookFiles(providers.HookFilesRequest{OS: runtime.GOOS, Root: t.TempDir(), Binary: bin})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := files[0].Merge(nil)
		if err != nil {
			t.Fatal(err)
		}
		var file struct {
			Hooks map[string][]struct {
				Hooks []struct{ Command string } `json:"hooks"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(raw, &file); err != nil {
			t.Fatal(err)
		}
		line := file.Hooks["PreToolUse"][0].Hooks[0].Command
		for _, exit := range []string{"0", "2"} {
			out, code := runIn(t, shell(line), "HELPER_EXIT="+exit)
			if got := strings.TrimSpace(out); got != "ARGS hook --provider codex --event PreToolUse" || code != map[string]int{"0": 0, "2": 2}[exit] {
				t.Errorf("%s, exit %s: stdout %q, exit %d", line, exit, got, code)
			}
		}
	}
}
