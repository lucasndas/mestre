package providers_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
)

// writeFiles writes generated files the way the engine does: merged with
// what is on disk.
func writeFiles(t *testing.T, files []providers.ManagedFile) {
	t.Helper()
	for _, f := range files {
		cur, err := os.ReadFile(f.Path)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.Path, mergeFile(t, f, cur), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// CA-CODEX-01
func TestHookFilesAreTheSameForEveryProfileAndNoneInTheWorktree(t *testing.T) {
	t.Parallel()
	a := codexAdapter(providers.Options{})
	root := t.TempDir()
	worktree := filepath.Join(root, "work", "1-login")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin", "mestre")
	var contents [][]byte
	for _, profile := range []string{"codex-pessoal", "codex-empresa"} {
		home := filepath.Join(root, "contas", profile)
		files, err := a.HookFiles(providers.HookFilesRequest{OS: "linux", Root: home, Binary: bin})
		if err != nil {
			t.Fatal(err)
		}
		if len(files) != 1 || files[0].Path != filepath.Join(home, "hooks.json") {
			t.Fatalf("files = %+v, want only %s", files, filepath.Join(home, "hooks.json"))
		}
		writeFiles(t, files)
		b, err := os.ReadFile(files[0].Path)
		if err != nil {
			t.Fatal(err)
		}
		contents = append(contents, b)
	}
	if !bytes.Equal(contents[0], contents[1]) {
		t.Fatalf("the hooks.json of the two profiles differ:\n%s\n---\n%s", contents[0], contents[1])
	}
	err := filepath.WalkDir(worktree, func(p string, d fs.DirEntry, err error) error {
		if err == nil && p != worktree {
			t.Errorf("a file in the worktree: %s", p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// The content: each bound event calls `mestre hook` by the stable path,
	// with the time limits of the document.
	var file struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type, Command string
				Timeout       int
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(contents[0], &file); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		"SessionStart": 30, "UserPromptSubmit": 30, "PreToolUse": 30, "PermissionRequest": 30,
		"PostToolUse": 60, "Stop": 60, "PreCompact": 60, "SessionEnd": 3, "Interrupt": 3,
	}
	if len(file.Hooks) != len(want) {
		t.Fatalf("events = %v", file.Hooks)
	}
	for ev, timeout := range want {
		g := file.Hooks[ev]
		if len(g) != 1 || len(g[0].Hooks) != 1 {
			t.Fatalf("%s: %+v", ev, g)
		}
		h := g[0].Hooks[0]
		if h.Type != "command" || h.Command != bin+" hook --provider codex --event "+ev || h.Timeout != timeout {
			t.Errorf("%s: %+v", ev, h)
		}
		if tools := ev != "UserPromptSubmit" && ev != "Stop" && ev != "Interrupt"; tools != (g[0].Matcher == "*") {
			t.Errorf("%s: matcher %q", ev, g[0].Matcher)
		}
	}
}

func TestHookCommandQuotesABlankAndRefusesShellSyntax(t *testing.T) {
	t.Parallel()
	a := codexAdapter(providers.Options{})
	files, err := a.HookFiles(providers.HookFilesRequest{OS: "windows", Root: `C:\ProgramData\Mestre\contas\c1`, Binary: `C:\Program Files\Mestre\atual\mestre.exe`})
	if err != nil {
		t.Fatal(err)
	}
	// Windows: PowerShell, the shell Codex runs hooks with there; & calls
	// the quoted path and the exit code of the call is passed on.
	if b := mergeFile(t, files[0], nil); !strings.Contains(string(b), `"\u0026 \"C:\\Program Files\\Mestre\\atual\\mestre.exe\" hook --provider codex --event Stop; exit $LASTEXITCODE"`) {
		t.Fatalf("hooks.json = %s", b)
	}
	files, err = a.HookFiles(providers.HookFilesRequest{OS: "linux", Root: "/p", Binary: "/opt/me stre/mestre"})
	if err != nil {
		t.Fatal(err)
	}
	if b := mergeFile(t, files[0], nil); !strings.Contains(string(b), `"\"/opt/me stre/mestre\" hook --provider codex --event Stop"`) {
		t.Fatalf("hooks.json = %s", b)
	}
	for _, bin := range []string{"mestre", "/opt/$HOME/mestre", "/opt/a;b/mestre", `/opt/"q"/mestre`, "/opt/%x%/mestre"} {
		if _, err := a.HookFiles(providers.HookFilesRequest{Root: "/p", Binary: bin}); err == nil {
			t.Errorf("binary %q was accepted", bin)
		}
	}
	if _, err := a.HookFiles(providers.HookFilesRequest{Binary: "/usr/local/bin/mestre"}); err == nil {
		t.Error("hooks without the profile")
	}
}
