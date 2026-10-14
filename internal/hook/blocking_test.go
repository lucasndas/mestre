package hook_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/providers"
)

// The blocking pre_tool hooks of a class exist to deny, and they do not run
// without the engine: the engine says so in env.json, and with the engine
// down the dispatcher then allows only what reading allows, even what the
// snapshot allows. A class without them keeps the snapshot; without env.json
// nothing says the class has none, and only reading holds too.
func TestTheDegradedModeAllowsOnlyReadingWhenTheClassHasBlockingHooks(t *testing.T) {
	t.Parallel()
	read := func() []byte {
		b, _ := json.Marshal(map[string]any{"session_id": "s1", "cwd": worktree(), "hook_event_name": "PreToolUse",
			"tool_name": "Read", "tool_input": map[string]any{"file_path": filepath.Join(worktree(), "README.md")}})
		return b
	}
	for _, c := range []struct {
		name, hooks string
		env         bool // the engine decided once, writing env.json
		npm         string
	}{
		{"blocking hook", "pre_tool:\n  - {name: checar, run: \"npm test\", timeout: 5, blocking: true}\n", true, "deny"},
		{"no blocking hook", "pre_tool:\n  - {name: checar, run: \"npm test\", timeout: 5}\n", true, "allow"},
		{"no env.json", "pre_tool:\n  - {name: checar, run: \"npm test\", timeout: 5}\n", false, "deny"},
	} {
		synctest.Test(t, func(t *testing.T) {
			agents := t.TempDir()
			m := &hook.Machine{Instance: "principal", Agents: agents, Queue: t.TempDir(), Layout: machine().Layout}
			r := rules(t, qaYAML)
			snap, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(agents, agent), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(agents, agent, classes.SnapshotName), snap, 0o640); err != nil {
				t.Fatal(err)
			}
			if c.env {
				e, _ := engineWith(t, classWith(t, c.hooks))
				e.Agents = agents
				up := call{provider: providers.ClaudeCode, event: "PreToolUse", stdin: claudeShell("npm test"), dial: engine(t, e),
					files: hook.OSFiles{}, machine: m, runner: &runner{}}.run(t.Context())
				if v, why := decision(t, providers.ClaudeCode, up); v != "allow" {
					t.Fatalf("%s: with the engine up: %s (%q)", c.name, v, why)
				}
			}
			offline := func(stdin []byte) (string, string) {
				return decision(t, providers.ClaudeCode, call{provider: providers.ClaudeCode, event: "PreToolUse", stdin: stdin, dial: down,
					files: hook.OSFiles{}, machine: m}.run(t.Context()))
			}
			if v, why := offline(claudeShell("npm test")); v != c.npm || (v == "deny" && !has(why, "motor indisponível")) {
				t.Errorf("%s: npm test with the engine down: %s (%q); want %s", c.name, v, why, c.npm)
			}
			if v, why := offline(read()); c.env && v != "allow" {
				t.Errorf("%s: a read with the engine down: %s (%q)", c.name, v, why)
			}
		})
	}
}
