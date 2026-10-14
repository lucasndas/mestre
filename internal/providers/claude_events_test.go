package providers_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/testkit/fakeagent"
	"github.com/lucasndas/mestre/pkg/clock"
)

type capturedHook struct {
	event, command string
	stdin          []byte
}

// capture is the hook runner of the fake agent that records each call and
// answers the first PermissionRequest with "allow", like the dispatcher.
type capture struct {
	mu          sync.Mutex
	got         []capturedHook
	permissions int
}

func (c *capture) RunHook(_ context.Context, call fakeagent.HookCall) (fakeagent.HookResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, capturedHook{call.Event, call.Command, call.Stdin})
	if call.Event == "PermissionRequest" {
		if c.permissions++; c.permissions == 1 {
			return fakeagent.HookResult{Stdout: []byte(`{"hookSpecificOutput": {"hookEventName": "PermissionRequest", "decision": {"behavior": "allow"}}}`)}, nil
		}
	}
	return fakeagent.HookResult{}, nil
}

func (c *capture) calls() []capturedHook {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.got)
}

func writeManaged(t *testing.T, f providers.ManagedFile) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.Path, apply(t, f, nil), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The files and arguments the adapter gives a character are what a Claude
// Code reads: the fake agent loads the hooks of HookFiles, the layer of
// DeliverMestreLayer and the statusLine of the --settings file, fires every
// event of the map, and each payload normalizes to the event the map says;
// the statusLine's reads as usage.
func TestEveryHookOfTheMapArrivesThroughTheGeneratedFiles(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		clk := clock.NewFake(at)
		const script = `prompts:
  - match: "^first"
    steps:
      - say: starting
      - tool: {name: Bash, input: {command: "ls"}, ask: true}
      - tool: {name: Bash, input: {command: "ls -l"}, ask: true}
      - edit: {path: "a.txt", content: "x"}
      - run: "fail now"
  - match: "^limit"
    steps:
      - limit: weekly
`
		rig := newFakeRig(t, map[string]string{"script.yaml": script})
		claude := providers.NewClaudeCode(providers.Options{})
		goos := runtime.GOOS

		files, err := claude.HookFiles(providers.HookFilesRequest{OS: goos, Root: rig.work})
		if err != nil {
			t.Fatal(err)
		}
		layer, err := claude.DeliverMestreLayer(providers.LayerRequest{Path: ".mestre/agents/a1/MESTRE.md", CharacterSkills: true})
		if err != nil {
			t.Fatal(err)
		}
		nc, err := claude.(providers.NativeTranslator).Native(providers.NativePolicy{AgentID: "a1", OS: goos, Worktree: rig.work})
		if err != nil {
			t.Fatal(err)
		}
		writeManaged(t, files[0])
		writeManaged(t, providers.ManagedFile(nc.Files[0]))
		if err := os.WriteFile(filepath.Join(rig.work, ".mestre", "agents", "a1", "MESTRE.md"), []byte("LAYER-OF-THE-MESTRE"), 0o644); err != nil {
			t.Fatal(err)
		}

		hooks := &capture{}
		agent := rig.start(t, clk, &fakeagent.MemLauncher{Hooks: hooks, Commands: commands{}}, slices.Concat(layer.Args, nc.Args),
			"FAKEAGENT_SCRIPT="+filepath.Join(rig.root, "script.yaml"))
		synctest.Wait()
		say := func(text string) {
			b, err := claude.TypeMessage(text)
			if err != nil {
				t.Fatal(err)
			}
			if err := agent.Input(t.Context(), b); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
		}
		screenHas := func(what string) {
			t.Helper()
			if s := agent.em.Screen(); !strings.Contains(s, what) {
				t.Fatalf("the screen lacks %q:\n%s", what, s)
			}
		}

		say("first")
		screenHas("Do you want to proceed?") // the second tool asks: nobody decided it
		if err := agent.Input(t.Context(), []byte("\r")); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		say("limit")
		screenHas("What do you want to do?")
		if err := agent.Input(t.Context(), []byte("\r")); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		say("/compact")
		say("/fake-dump")
		dump := agent.em.History(-1)
		say("/exit")
		if err := agent.proc.Wait(); err != nil {
			t.Fatalf("the fake agent ended with %v", err)
		}

		// The layer and the folder of the character reached the agent.
		for _, want := range []string{`"append_system_prompt":"LAYER-OF-THE-MESTRE"`, `"add_dirs":[".mestre/agents/a1"]`} {
			if !strings.Contains(dump, want) {
				t.Errorf("/fake-dump lacks %s:\n%s", want, dump)
			}
		}

		// Every event of the map for Claude Code, with the command of the file.
		type want struct {
			kind   providers.EventKind
			stop   providers.StopReason
			failed bool
		}
		wants := map[string]want{
			"SessionStart": {kind: providers.EventSessionStart}, "UserPromptSubmit": {kind: providers.EventTurnStart},
			"PreToolUse": {kind: providers.EventPreTool}, "PermissionRequest": {kind: providers.EventPermissionRequest},
			"Notification": {kind: providers.EventNotification}, "PostToolUse": {kind: providers.EventPostTool},
			"PostToolUseFailure": {kind: providers.EventPostTool, failed: true},
			"Stop":               {kind: providers.EventTurnEnd, stop: providers.StopNormal},
			"StopFailure":        {kind: providers.EventTurnFailure, stop: providers.StopRateLimit},
			"PreCompact":         {kind: providers.EventPreCompact}, "SessionEnd": {kind: providers.EventSessionEnd},
			"StatusLine": {kind: providers.EventUsage},
		}
		seen := map[string]bool{}
		var usage providers.Event
		for _, h := range hooks.calls() {
			w, ok := wants[h.event]
			if !ok {
				t.Errorf("an event outside the map fired: %s", h.event)
				continue
			}
			seen[h.event] = true
			if !strings.Contains(h.command, "hook --provider claude-code --event "+h.event) {
				t.Errorf("%s ran %q, not the line of the generated files", h.event, h.command)
			}
			ev, err := claude.NormalizeHook(providers.HookInput{NativeEvent: h.event, Stdin: h.stdin, AgentID: "a1", ReceivedAt: at})
			if err != nil {
				t.Errorf("%s: %v", h.event, err)
				continue
			}
			if ev.Event != w.kind || ev.SessionID == "" || (w.stop != "" && ev.StopReason != w.stop) ||
				(h.event != "StatusLine" && ev.Event.ToolEvent() && ev.Tool == nil) ||
				(w.failed && (ev.ToolResult == nil || ev.ToolResult.OK)) {
				t.Errorf("%s normalized to %+v, want %s", h.event, ev, w.kind)
			}
			if h.event == "StatusLine" {
				usage = ev
			}
		}
		for event := range wants {
			if !seen[event] {
				t.Errorf("the event %s never arrived", event)
			}
		}
		if got := hooks.calls(); len(got) == 0 || got[0].event != "SessionStart" {
			t.Errorf("the first hook = %v, want SessionStart", got)
		}
		if usage.Event != providers.EventUsage {
			t.Fatal("the statusLine of the --settings file did not run")
		}
		u, err := claude.ReadUsage(providers.UsageRequest{AgentID: "a1", Last: usage})
		if err != nil || u.CostUSD <= 0 || u.ContextPct <= 0 || len(u.LimitWindows) != 2 {
			t.Errorf("usage of the fake agent = %+v, %v", u, err)
		}
	})
}
