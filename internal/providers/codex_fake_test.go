package providers_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/internal/testkit"
	"github.com/lucasndas/mestre/internal/testkit/fakeagent"
	"github.com/lucasndas/mestre/pkg/clock"
)

// hookCalls records the hook calls of the fake agent and answers each with
// respond (nil: an empty answer, which lets the tool run).
type hookCalls struct {
	mu      sync.Mutex
	calls   []fakeagent.HookCall
	respond func(fakeagent.HookCall) fakeagent.HookResult
}

func (h *hookCalls) RunHook(_ context.Context, c fakeagent.HookCall) (fakeagent.HookResult, error) {
	h.mu.Lock()
	h.calls = append(h.calls, c)
	h.mu.Unlock()
	if h.respond == nil {
		return fakeagent.HookResult{}, nil
	}
	return h.respond(c), nil
}

func (h *hookCalls) snapshot() []fakeagent.HookCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.calls)
}

// codexProfile prepares a profile and a worktree with the files the
// adapter generates, written as the engine writes them.
func codexProfile(t *testing.T, a providers.Adapter) (home, worktree, bin string) {
	t.Helper()
	root := t.TempDir()
	home, worktree, bin = filepath.Join(root, "contas", "codex-1"), filepath.Join(root, "work"), filepath.Join(root, "bin", "mestre")
	testkit.WriteFiles(t, root, map[string]string{"work/.git/HEAD": "ref: refs/heads/main\n", "contas/codex-1/auth.json": "{}"})
	if _, err := a.PreTrust(providers.PreTrustRequest{Profile: home, Worktree: worktree, RepoRoot: worktree}); err != nil {
		t.Fatal(err)
	}
	hooks, err := a.HookFiles(providers.HookFilesRequest{Root: home, Binary: bin, Fallbacks: providers.NativeFallbacks{CodexHookTrust: true}})
	if err != nil {
		t.Fatal(err)
	}
	writeFiles(t, hooks)
	return home, worktree, bin
}

func typeText(t *testing.T, a providers.Adapter, p *fakeagent.MemProcess, text string) {
	t.Helper()
	b, err := a.TypeMessage(text)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.PTY().Write(b); err != nil {
		t.Fatal(err)
	}
	synctest.Wait()
}

// The files of the adapter are what the fake Codex reads as the real one:
// the pre-trust skips the trust screen, the trust entries of the fallback
// skip the hook review, the nine hooks call `mestre hook` and the
// transcript has the use that ReadUsage reads.
func TestTheFakeCodexRunsWithTheFilesOfTheAdapter(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		a := providers.NewCodex(providers.Options{})
		home, worktree, bin := codexProfile(t, a)
		h := &hookCalls{respond: func(c fakeagent.HookCall) fakeagent.HookResult {
			if c.Event == "PermissionRequest" {
				out := a.FormatResponse(c.Event, providers.Reply{Kind: providers.ReplyDeny, Reason: "no internet"})
				return fakeagent.HookResult{Stdout: out.Stdout, Stderr: out.Stderr, ExitCode: out.Exit}
			}
			return fakeagent.HookResult{}
		}}
		testkit.WriteFiles(t, worktree, map[string]string{"s.yaml": `
prompts:
  - match: ^fetch
    steps:
      - tool: {name: Bash, input: {command: "curl https://example.com", sandbox_permissions: require_escalated}}
  - match: ^stuck
    steps:
      - hang: true
`})
		l := &fakeagent.MemLauncher{Clock: clock.Real{}, Hooks: h, Reporter: t}
		pr, err := l.Start(t.Context(), terminal.Spec{Path: "codex", Dir: worktree,
			Env: []string{"CODEX_HOME=" + home, "HOME=" + home, "MESTRE_AGENT_ID=a1", "FAKEAGENT_SCRIPT=s.yaml"}})
		if err != nil {
			t.Fatal(err)
		}
		p := pr.(*fakeagent.MemProcess)
		t.Cleanup(func() { _ = p.Kill() })
		synctest.Wait()
		if out := p.Output(); strings.Contains(out, "Trust this folder?") || strings.Contains(out, fakeagent.HookReviewTitle) {
			t.Fatalf("the pre-trust or the hook trust did not hold:\n%s", out)
		}
		typeText(t, a, p, "hello")
		typeText(t, a, p, "fetch it")
		typeText(t, a, p, "/compact")
		typeText(t, a, p, "stuck")
		if _, err := p.PTY().Write([]byte{0x03}); err != nil { // interrupts the turn
			t.Fatal(err)
		}
		synctest.Wait()
		if _, err := p.PTY().Write([]byte{0x03, 0x03}); err != nil { // leaves, at the prompt
			t.Fatal(err)
		}
		_ = p.Wait()

		var events []string
		var stop []byte
		for _, c := range h.snapshot() {
			events = append(events, c.Event)
			want := bin + " hook --provider codex --event " + c.Event
			if runtime.GOOS == "windows" { // the line PowerShell runs
				want = `& "` + bin + `" hook --provider codex --event ` + c.Event + "; exit $LASTEXITCODE"
			}
			if c.Command != want {
				t.Errorf("hook command %q", c.Command)
			}
			if c.Event == "Stop" && stop == nil {
				stop = c.Stdin
			}
		}
		for _, ev := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop", "PermissionRequest", "PreCompact", "Interrupt", "SessionEnd"} {
			if !slices.Contains(events, ev) {
				t.Errorf("no %s among %v", ev, events)
			}
		}
		if out := p.Output(); !strings.Contains(out, "Tool Bash not approved: no internet") {
			t.Errorf("the denied escalation ran:\n%s", out)
		}
		ev, err := a.NormalizeHook(providers.HookInput{NativeEvent: "Stop", Stdin: stop, AgentID: "a1"})
		if err != nil {
			t.Fatal(err)
		}
		r, err := a.ReadUsage(providers.UsageRequest{AgentID: "a1", Profile: home, Last: ev})
		if err != nil {
			t.Fatal(err)
		}
		if r.InputTokens == 0 || len(r.LimitWindows) != 2 || r.LimitsReached {
			t.Fatalf("usage = %+v", r)
		}
		var cur struct{ Offset int64 }
		if json.Unmarshal([]byte(r.Cursor), &cur) != nil || cur.Offset == 0 {
			t.Fatalf("cursor = %s", r.Cursor)
		}
	})
}

// At the usage limit the fake Codex, as the real one, runs no Stop hook:
// the reading of the transcript finds the limit and the failed turn.
func TestTheFakeCodexAtTheUsageLimit(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		a := providers.NewCodex(providers.Options{})
		home, worktree, _ := codexProfile(t, a)
		h := &hookCalls{}
		l := &fakeagent.MemLauncher{Clock: clock.Real{}, Hooks: h, Reporter: t}
		pr, err := l.Start(t.Context(), terminal.Spec{Path: "codex", Dir: worktree,
			Env: []string{"CODEX_HOME=" + home, "HOME=" + home, "MESTRE_AGENT_ID=a1", "FAKEAGENT_CODEX_USAGE_LIMIT=1"}})
		if err != nil {
			t.Fatal(err)
		}
		p := pr.(*fakeagent.MemProcess)
		t.Cleanup(func() { _ = p.Kill() })
		synctest.Wait()
		typeText(t, a, p, "hello")
		var start []byte
		for _, c := range h.snapshot() {
			if c.Event == "Stop" {
				t.Fatal("a Stop hook ran on a turn at the usage limit")
			}
			if c.Event == "SessionStart" {
				start = c.Stdin
			}
		}
		ev, err := a.NormalizeHook(providers.HookInput{NativeEvent: "SessionStart", Stdin: start, AgentID: "a1"})
		if err != nil {
			t.Fatal(err)
		}
		r, err := a.ReadUsage(providers.UsageRequest{AgentID: "a1", Profile: home, Last: ev})
		if err != nil {
			t.Fatal(err)
		}
		if !r.LimitsReached || len(r.Failures) != 1 || r.Failures[0].StopReason != providers.StopRateLimit {
			t.Fatalf("reading = %+v", r)
		}
	})
}
