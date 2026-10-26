package fakeagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mestre-ia/mestre/internal/terminal"
	"github.com/mestre-ia/mestre/internal/testkit"
	"github.com/mestre-ia/mestre/internal/testkit/fakeagent"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// hooks is a HookRunner that records calls and answers with respond.
type hooks struct {
	mu      sync.Mutex
	calls   []fakeagent.HookCall
	respond func(fakeagent.HookCall) (fakeagent.HookResult, error)
}

func (h *hooks) RunHook(_ context.Context, c fakeagent.HookCall) (fakeagent.HookResult, error) {
	h.mu.Lock()
	h.calls = append(h.calls, c)
	h.mu.Unlock()
	if h.respond == nil {
		return fakeagent.HookResult{}, nil
	}
	return h.respond(c)
}

func (h *hooks) events() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, c := range h.calls {
		out = append(out, c.Event+" "+c.Command)
	}
	return out
}

type commands struct {
	mu  sync.Mutex
	ran []string
}

func (c *commands) RunCommand(_ context.Context, cmd, _ string, _ []string) (string, int, error) {
	c.mu.Lock()
	c.ran = append(c.ran, cmd)
	c.mu.Unlock()
	return "ok\n", 0, nil
}

type env struct {
	dir, home string
	vars      []string
}

func newEnv(t *testing.T, extra ...string) env {
	t.Helper()
	root := t.TempDir()
	e := env{dir: filepath.Join(root, "work"), home: filepath.Join(root, "home")}
	testkit.WriteFiles(t, root, map[string]string{"work/.git/HEAD": "ref: refs/heads/main\n", "home/.keep": ""})
	e.vars = append([]string{"HOME=" + e.home, "MESTRE_AGENT_ID=a1"}, extra...)
	return e
}

func start(t *testing.T, l *fakeagent.MemLauncher, program string, e env, args ...string) *fakeagent.MemProcess {
	t.Helper()
	p, err := l.Start(t.Context(), terminal.Spec{Path: program, Args: args, Env: e.vars, Dir: e.dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Kill() })
	return p.(*fakeagent.MemProcess)
}

func typeLine(t *testing.T, p *fakeagent.MemProcess, s string) {
	t.Helper()
	if _, err := p.PTY().Write([]byte("\x1b[200~" + s + "\x1b[201~\r")); err != nil {
		t.Fatal(err)
	}
	synctest.Wait()
}

func stop(t *testing.T, p *fakeagent.MemProcess) error {
	t.Helper()
	_ = p.Kill()
	return p.Wait()
}

// keyPaths lists the field paths of a payload. Tool arguments vary with the
// tool, so their content is not compared.
func keyPaths(v any, prefix string, out *[]string) {
	if m, ok := v.(map[string]any); ok {
		for k, x := range m {
			*out = append(*out, prefix+k)
			if k != "args" && k != "tool_input" && k != "tool_response" {
				keyPaths(x, prefix+k+".", out)
			}
		}
	}
}

func sameShape(t *testing.T, got []byte, origin, event string) map[string]any {
	t.Helper()
	rec, err := testkit.LoadRecording(testkit.Recordings(), origin, "", event)
	if err != nil {
		t.Fatal(err)
	}
	var g, r map[string]any
	_ = json.Unmarshal(got, &g)
	_ = json.Unmarshal(rec.Body, &r)
	var gk, rk []string
	keyPaths(g, "", &gk)
	keyPaths(r, "", &rk)
	sort.Strings(gk)
	sort.Strings(rk)
	if strings.Join(gk, ",") != strings.Join(rk, ",") {
		t.Fatalf("%s payload fields\n got %v\nwant %v", event, gk, rk)
	}
	return g
}

const claudeHooks = `{"hooks": {"SessionStart": [{"matcher": "*", "hooks": [{"type": "command", "command": "mestre hook --provider claude-code --event SessionStart"}]}]}}`

// CA-DUBLES-03
func TestClaudeTrustScreenAndPreTrustedSessionStart(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := &hooks{}
		l := &fakeagent.MemLauncher{Clock: clock.Real{}, Hooks: h, Reporter: t}
		cfgDir := t.TempDir()
		testkit.WriteFiles(t, cfgDir, map[string]string{".credentials.json": "{}"})
		e := newEnv(t, "CLAUDE_CONFIG_DIR="+cfgDir)
		testkit.WriteFiles(t, e.dir, map[string]string{".claude/settings.local.json": claudeHooks})

		p := start(t, l, "claude", e, "--session-id", "11111111-2222-4333-8444-555555555555")
		synctest.Wait()
		if out := p.Output(); !strings.Contains(out, "Do you trust the files in this folder?") {
			t.Fatalf("no trust screen:\n%s", out)
		}
		if got := h.events(); len(got) != 0 {
			t.Fatalf("hooks ran in an untrusted folder: %v", got)
		}
		_ = stop(t, p)

		trust := fmt.Sprintf(`{"numStartups": 3, "projects": {%q: {"hasTrustDialogAccepted": true}}}`, filepath.ToSlash(e.dir))
		testkit.WriteFiles(t, cfgDir, map[string]string{".claude.json": trust})
		p = start(t, l, "claude", e, "--session-id", "11111111-2222-4333-8444-555555555555")
		synctest.Wait()
		if out := p.Output(); strings.Contains(out, "trust") || !strings.HasSuffix(out, fakeagent.PromptMarker) {
			t.Fatalf("pre-trusted start:\n%s", out)
		}
		if got := h.events(); len(got) != 1 || got[0] != "SessionStart mestre hook --provider claude-code --event SessionStart" {
			t.Fatalf("hooks = %v", got)
		}
		g := sameShape(t, h.calls[0].Stdin, "claude-code", "SessionStart")
		if g["session_id"] != "11111111-2222-4333-8444-555555555555" || g["cwd"] != e.dir || g["hook_event_name"] != "SessionStart" {
			t.Fatalf("payload = %v", g)
		}
		_ = stop(t, p)
	})
}

// CA-DUBLES-04
func TestAntigravityBlocksToolWhenHookAnswersNonJSON(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := &hooks{respond: func(fakeagent.HookCall) (fakeagent.HookResult, error) {
			return fakeagent.HookResult{Stdout: []byte("allow, I guess")}, nil
		}}
		l := &fakeagent.MemLauncher{Clock: clock.Real{}, Hooks: h, Reporter: t}
		e := newEnv(t)
		testkit.WriteFiles(t, e.home, map[string]string{
			".gemini/antigravity-cli/settings.json":           fmt.Sprintf(`{"trustedWorkspaces": [%q]}`, e.dir),
			".gemini/antigravity-cli/antigravity-oauth-token": "{}",
		})
		testkit.WriteFiles(t, e.dir, map[string]string{
			".agents/hooks.json": `{"mestre": {"enabled": true, "PreToolUse": [{"matcher": "*", "hooks": [{"type": "command", "command": "gate", "timeout": 30}]}]}}`,
			"script.yaml":        "prompts:\n  - match: write\n    steps:\n      - edit: {path: out.txt, content: hi}\n",
		})

		p := start(t, l, "agy", e, "--fake-script", "script.yaml")
		synctest.Wait()
		typeLine(t, p, "write it")

		if out := p.Output(); !strings.Contains(out, "Tool write_to_file blocked by hook: hook output is not JSON") {
			t.Fatalf("tool not blocked:\n%s", out)
		}
		if _, err := os.Stat(filepath.Join(e.dir, "out.txt")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("blocked tool changed the file: %v", err)
		}
		g := sameShape(t, h.calls[0].Stdin, "antigravity", "PreToolUse")
		if g["toolCall"].(map[string]any)["name"] != "write_to_file" {
			t.Fatalf("payload = %v", g)
		}
		_ = stop(t, p)
	})
}

// CA-DUBLES-05
func TestCodexReviewsModifiedHookAndDoesNotRunIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := &hooks{}
		l := &fakeagent.MemLauncher{Clock: clock.Real{}, Hooks: h, Reporter: t}
		codexHome := t.TempDir()
		e := newEnv(t, "CODEX_HOME="+codexHome)
		hooksJSON := func(cmd string) string {
			return fmt.Sprintf(`{"hooks": {"SessionStart": [{"matcher": "*", "hooks": [{"type": "command", "command": %q}]}]}}`, cmd)
		}
		testkit.WriteFiles(t, codexHome, map[string]string{
			"auth.json":   "{}",
			"config.toml": fmt.Sprintf("model = \"x\"\n\n[projects.%q]\ntrust_level = \"trusted\"\n", e.dir),
			"hooks.json":  hooksJSON("mestre hook --provider codex --event SessionStart"),
		})
		// First start: the user trusts the hook once (as in the profile setup).
		p := start(t, l, "codex", e)
		synctest.Wait()
		_, _ = p.PTY().Write([]byte("\x1b[B\r")) // "Trust all and continue"
		synctest.Wait()
		_ = stop(t, p)
		p = start(t, l, "codex", e)
		synctest.Wait()
		if out := p.Output(); strings.Contains(out, fakeagent.HookReviewTitle) {
			t.Fatalf("trusted hook asked for review again:\n%s", out)
		}
		_ = stop(t, p)
		if got := h.events(); len(got) != 2 {
			t.Fatalf("trusted hook runs = %v, want 2", got)
		}

		testkit.WriteFiles(t, codexHome, map[string]string{"hooks.json": hooksJSON("evil --event SessionStart")})
		p = start(t, l, "codex", e)
		synctest.Wait()
		if out := p.Output(); !strings.Contains(out, fakeagent.HookReviewTitle) {
			t.Fatalf("no hook review:\n%s", out)
		}
		_, _ = p.PTY().Write([]byte("\x1b[B\x1b[B\r")) // "Continue without trusting"
		synctest.Wait()
		for _, ev := range h.events() {
			if strings.Contains(ev, "evil") {
				t.Fatalf("modified hook ran: %v", h.events())
			}
		}
		_ = stop(t, p)
	})
}

func TestScriptStepsWaitCrashAndInterrupt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		clk := clock.Real{}
		cmds := &commands{}
		l := &fakeagent.MemLauncher{Clock: clk, Hooks: &hooks{}, Commands: cmds, Reporter: t}
		cfgDir := t.TempDir()
		e := newEnv(t, "CLAUDE_CONFIG_DIR="+cfgDir, "FAKEAGENT_SCRIPT=s.yaml")
		testkit.WriteFiles(t, cfgDir, map[string]string{
			".credentials.json": "{}",
			".claude.json":      fmt.Sprintf(`{"projects": {%q: {"hasTrustDialogAccepted": true}}}`, filepath.ToSlash(e.dir)),
		})
		testkit.WriteFiles(t, e.dir, map[string]string{"s.yaml": `
prompts:
  - match: ^slow
    steps:
      - wait: 5m
      - say: woke up
      - run: mestre done "slow"
  - match: ^stuck
    steps:
      - hang: true
  - match: ^boom
    steps:
      - crash: out of memory
`})
		p := start(t, l, "claude", e)
		synctest.Wait()

		typeLine(t, p, "slow please")
		_ = clock.Sleep(t.Context(), clk, 5*time.Minute-time.Second)
		synctest.Wait()
		if strings.Contains(p.Output(), "woke up") {
			t.Fatalf("woke before 5m:\n%s", p.Output())
		}
		_ = clock.Sleep(t.Context(), clk, time.Second)
		synctest.Wait()
		if !strings.Contains(p.Output(), "woke up") {
			t.Fatalf("still waiting after 5m:\n%s", p.Output())
		}
		if len(cmds.ran) != 1 || cmds.ran[0] != `mestre done "slow"` {
			t.Fatalf("commands = %v", cmds.ran)
		}

		typeLine(t, p, "stuck")
		_, _ = p.PTY().Write([]byte{0x03})
		synctest.Wait()
		if !strings.Contains(p.Output(), "Interrupted") {
			t.Fatalf("no interrupt:\n%s", p.Output())
		}

		typeLine(t, p, "/fake-dump")
		if !strings.Contains(p.Output(), `"provider":"claude"`) || !strings.Contains(p.Output(), `"MESTRE_AGENT_ID":"a1"`) {
			t.Fatalf("dump:\n%s", p.Output())
		}

		typeLine(t, p, "boom")
		if err := p.Wait(); !errors.Is(err, fakeagent.ErrCrash) {
			t.Fatalf("Wait = %v, want crash", err)
		}
	})
}

func TestWithoutScriptEchoesAndCallsDone(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		cmds := &commands{}
		l := &fakeagent.MemLauncher{Clock: clock.Real{}, Hooks: &hooks{}, Commands: cmds, Reporter: t}
		e := newEnv(t)
		testkit.WriteFiles(t, e.home, map[string]string{
			".gemini/antigravity-cli/settings.json":           fmt.Sprintf(`{"trustedWorkspaces": [%q]}`, e.dir),
			".gemini/antigravity-cli/antigravity-oauth-token": "{}",
		})
		p := start(t, l, "fakeagent", e, "--provider", "antigravity")
		synctest.Wait()
		typeLine(t, p, "linha 1\nlinha 2")
		if len(cmds.ran) != 1 || cmds.ran[0] != `mestre done "linha 1\nlinha 2"` {
			t.Fatalf("commands = %q", cmds.ran)
		}
		if err := stop(t, p); err != nil {
			t.Fatal(err)
		}
	})
}

type reporter struct{ msgs []string }

func (r *reporter) Errorf(f string, a ...any) { r.msgs = append(r.msgs, fmt.Sprintf(f, a...)) }

func TestMemLauncherRefusesUnknownProgram(t *testing.T) {
	t.Parallel()
	rep := &reporter{}
	l := &fakeagent.MemLauncher{Clock: clock.Real{}, Reporter: rep}
	_, err := l.Start(context.Background(), terminal.Spec{Path: "/usr/bin/vim"})
	if err == nil || len(rep.msgs) != 1 || rep.msgs[0] != "not implemented in double: launch /usr/bin/vim" {
		t.Fatalf("err = %v, msgs = %v", err, rep.msgs)
	}
}

func TestProviderFromName(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]fakeagent.Provider{
		"/x/claude": fakeagent.Claude, "codex.exe": fakeagent.Codex, `C:\bin\agy.exe`: fakeagent.Antigravity,
	} {
		if got, ok := fakeagent.ProviderFromName(strings.ReplaceAll(name, `\`, "/")); !ok || got != want {
			t.Fatalf("ProviderFromName(%q) = %q, %v", name, got, ok)
		}
	}
}
