package hook_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/internal/testkit"
	"github.com/lucasndas/mestre/internal/testkit/fakeagent"
	"github.com/lucasndas/mestre/pkg/clock"
)

// inProcess runs the hook commands of the fake agent as calls to the
// dispatcher, as the fast suite does (docs/qualidade/dubles.md): the command
// line `mestre hook …`, the agent's environment and the payload on stdin.
type inProcess struct {
	dial    func(string) localchan.Dialer
	machine *hook.Machine
}

func (h inProcess) RunHook(ctx context.Context, c fakeagent.HookCall) (fakeagent.HookResult, error) {
	args := strings.Fields(c.Command)
	if len(args) < 2 || args[0] != "mestre" || args[1] != "hook" {
		return fakeagent.HookResult{}, fmt.Errorf("not a hook of the Mestre: %q", c.Command)
	}
	env := map[string]string{}
	for _, kv := range c.Env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	var out, errb bytes.Buffer
	code := hook.Run(ctx, hook.Env{
		Getenv: func(k string) string { return env[k] }, Stdin: bytes.NewReader(c.Stdin), Stdout: &out, Stderr: &errb,
		Clock: clock.Real{}, Getwd: func() (string, error) { return c.Dir, nil }, Dial: h.dial, Files: hook.OSFiles{}, Machine: h.machine,
	}, args[2:])
	return fakeagent.HookResult{Stdout: out.Bytes(), Stderr: errb.Bytes(), ExitCode: code}, nil
}

// CA-PERM-07: with the engine down and a snapshot that denies src/**, the
// fake agent's edit of src/a.ts and its command in ask are both denied by
// `mestre hook`, the second with "motor indisponível"; the file is not
// written, and both decisions are in the hook queue.
func TestWithTheEngineDownTheSnapshotDecides(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		base, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		work, cfg := filepath.Join(base, "work"), filepath.Join(base, "cfg")
		mc := &hook.Machine{Instance: "principal", Agents: filepath.Join(base, "agentes"), Queue: filepath.Join(base, "fila"),
			Layout: classes.Layout{Agents: filepath.Join(base, "agentes")}}
		snap, err := json.Marshal(rules(t, qaYAML))
		if err != nil {
			t.Fatal(err)
		}
		env, _ := json.Marshal(hook.CharacterEnv{Version: hook.EnvVersion, AgentID: agent, Worktree: work})
		testkit.WriteFiles(t, base, map[string]string{
			"agentes/" + agent + "/policy.json": string(snap),
			"agentes/" + agent + "/env.json":    string(env),
			"fila/.keep":                        "",
			"work/.git/HEAD":                    "ref: refs/heads/main\n",
			"work/.claude/settings.local.json": `{"hooks": {"PreToolUse": [{"matcher": "*", "hooks": [{"type": "command", ` +
				`"command": "mestre hook --provider claude-code --event PreToolUse", "onFailure": "block"}]}]}}`,
			"work/script.yaml": "prompts:\n  - match: go\n    steps:\n" +
				"      - edit: {path: src/a.ts, content: x}\n" +
				"      - tool: {name: Bash, input: {command: npm install x}}\n",
			"cfg/.credentials.json": "{}",
			"cfg/.claude.json":      fmt.Sprintf(`{"numStartups": 3, "projects": {%q: {"hasTrustDialogAccepted": true}}}`, work),
		})
		l := &fakeagent.MemLauncher{Clock: clock.Real{}, Reporter: t, Hooks: inProcess{dial: down, machine: mc}}
		p, err := l.Start(t.Context(), terminal.Spec{Path: "claude", Dir: work,
			Args: []string{"--session-id", "11111111-2222-4333-8444-555555555555", "--fake-script", "script.yaml"},
			Env: []string{"HOME=" + base, "CLAUDE_CONFIG_DIR=" + cfg, localchan.EnvAgentID + "=" + agent,
				localchan.EnvEndpoint + "=unix:///run/mestre/principal/cli.sock"}})
		if err != nil {
			t.Fatal(err)
		}
		mp := p.(*fakeagent.MemProcess)
		t.Cleanup(func() { _ = mp.Kill(); _ = mp.Wait() })
		synctest.Wait()
		if _, err := mp.PTY().Write([]byte("\x1b[200~go\x1b[201~\r")); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()

		out := mp.Output()
		edit := lineWith(out, "blocked by hook")
		if !has(edit, "src/a.ts") || !has(edit, `files.deny "src/**"`) {
			t.Fatalf("the edit was not denied by the snapshot:\n%s", out)
		}
		if !strings.Contains(out, "Tool Bash blocked by hook: "+classes.DegradedReason) {
			t.Fatalf("the command in ask was not denied as engine down:\n%s", out)
		}
		if _, err := os.Stat(filepath.Join(work, "src", "a.ts")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("src/a.ts was written: %v", err)
		}
		var kinds []string
		_, err = hook.Drain(mc.Queue, hook.MaxQueued, farFuture, slog.New(slog.DiscardHandler), func(r hook.QueueRecord) {
			kinds = append(kinds, string(r.Decision.Verdict)+" "+r.Event.Tool.NativeName)
		})
		sort.Strings(kinds) // the two calls share the instant of the bubble
		if err != nil || strings.Join(kinds, ",") != "deny Bash,deny Write" {
			t.Fatalf("queue = %v (%v)", kinds, err)
		}
	})
}

// farFuture is a clock past the settle time of any file of the queue: the
// files have the times of the disk, not of the bubble.
var farFuture = clock.NewFake(time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC))

func lineWith(s, sub string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, sub) {
			return l
		}
	}
	return ""
}
