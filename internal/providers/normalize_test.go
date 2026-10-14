package providers_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/testkit"
)

var at = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

// noLinks is a resolver for paths that are not on this machine: nothing is a
// link, so a path normalizes to its clean form.
func noLinks(p string) (string, error) { return p, nil }

// wantPath is p as the normalizer gives it on this system.
func wantPath(p string) string {
	p = filepath.Clean(filepath.FromSlash(p))
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

func recorded(t *testing.T, origin, name string) []byte {
	t.Helper()
	r, err := testkit.LoadRecording(testkit.Recordings(), origin, "", name)
	if err != nil {
		t.Fatal(err)
	}
	return r.Body
}

// step is one recorded payload of the map and what the internal event must
// carry. turn is the expected turn_id: the payload's (prompt_id, turn_id,
// executionNum) or, where the payload has none, the turn in progress that
// Turns completes; session_start has none.
type step struct {
	recording, native string
	want              []providers.EventKind // more than one: Antigravity's derived session_start
	turn              string
	toolKind          providers.ToolKind
	command           string
	ok                *bool // post_tool result
	stop              providers.StopReason
}

func yes() *bool { b := true; return &b }
func no() *bool  { b := false; return &b }

// CA-ADAPT-01
func TestNormalizeHookFillsTheMapFromRecordedPayloads(t *testing.T) {
	t.Parallel()
	ev := func(k ...providers.EventKind) []providers.EventKind { return k }
	tests := []struct {
		provider providers.ID
		origin   string
		session  string
		steps    []step
	}{
		{providers.ClaudeCode, "claude-code", "abc123", []step{
			{recording: "SessionStart", want: ev(providers.EventSessionStart)},
			{recording: "UserPromptSubmit", want: ev(providers.EventTurnStart), turn: "p1"},
			{recording: "PreToolUse", want: ev(providers.EventPreTool), turn: "p1", toolKind: providers.KindShell, command: "npm test"},
			{recording: "PermissionRequest", want: ev(providers.EventPermissionRequest), turn: "p1", toolKind: providers.KindShell, command: "rm -rf build"},
			{recording: "PostToolUse", want: ev(providers.EventPostTool), turn: "p1", toolKind: providers.KindShell, command: "npm test", ok: yes()},
			{recording: "PostToolUseFailure", want: ev(providers.EventPostTool), turn: "p1", toolKind: providers.KindShell, command: "npm test", ok: no()},
			{recording: "Notification", want: ev(providers.EventNotification), turn: "p1"},
			{recording: "PreCompact", want: ev(providers.EventPreCompact), turn: "p1"},
			{recording: "StatusLine", want: ev(providers.EventUsage), turn: "p1"},
			{recording: "Stop", want: ev(providers.EventTurnEnd), turn: "p1", stop: providers.StopNormal},
			{recording: "StopFailure", want: ev(providers.EventTurnFailure), turn: "p1", stop: providers.StopRateLimit},
			{recording: "SessionEnd", want: ev(providers.EventSessionEnd), turn: "p1"},
		}},
		{providers.Codex, "codex", "019a0000-0000-7000-8000-000000000001", []step{
			{recording: "SessionStart", want: ev(providers.EventSessionStart)},
			{recording: "UserPromptSubmit", want: ev(providers.EventTurnStart), turn: "t1"},
			{recording: "PreToolUse", want: ev(providers.EventPreTool), turn: "t1", toolKind: providers.KindShell, command: "ls"},
			{recording: "PermissionRequest", want: ev(providers.EventPermissionRequest), turn: "t1", toolKind: providers.KindShell, command: "curl https://example.com"},
			{recording: "PostToolUse", want: ev(providers.EventPostTool), turn: "t1", toolKind: providers.KindShell, command: "ls", ok: yes()},
			{recording: "PreCompact", want: ev(providers.EventPreCompact), turn: "t1"},
			{recording: "Interrupt", want: ev(providers.EventInterrupt), turn: "t1", stop: providers.StopInterrupted},
			{recording: "Stop", want: ev(providers.EventTurnEnd), turn: "t1", stop: providers.StopNormal},
			{recording: "SessionEnd", want: ev(providers.EventSessionEnd), turn: "t1"},
		}},
		{providers.Antigravity, "antigravity", "b38a07d5-f7a3-4e27-b4d0-43e9110f60bb", []step{
			// The first PreInvocation of the conversation is session_start and turn_start.
			{recording: "PreInvocation", want: ev(providers.EventSessionStart, providers.EventTurnStart), turn: "1"},
			{recording: "PreToolUse", want: ev(providers.EventPreTool), turn: "1", toolKind: providers.KindShell, command: "echo hello > out.txt"},
			{recording: "PostToolUse", want: ev(providers.EventPostTool), turn: "1", toolKind: providers.KindShell, command: "echo hello > out.txt", ok: yes()},
			{recording: "Stop", want: ev(providers.EventTurnEnd), turn: "1", stop: providers.StopNormal},
			{recording: "Stop-error", native: "Stop", want: ev(providers.EventTurnFailure), turn: "1", stop: providers.StopError},
		}},
	}
	reg := providers.NewRegistry(providers.Options{Resolve: noLinks})
	for _, tt := range tests {
		t.Run(string(tt.provider), func(t *testing.T) {
			t.Parallel()
			a, err := reg.For(tt.provider)
			if err != nil {
				t.Fatal(err)
			}
			turns := providers.NewTurns(a.Info())
			for _, s := range tt.steps {
				native := s.native
				if native == "" {
					native = s.recording
				}
				got, err := a.NormalizeHook(providers.HookInput{
					NativeEvent: native, Stdin: recorded(t, tt.origin, s.recording),
					AgentID: "a1", TableID: "3-login", ReceivedAt: at,
				})
				if err != nil {
					t.Fatalf("%s: %v", s.recording, err)
				}
				evs := turns.Apply(got)
				if len(evs) != len(s.want) {
					t.Fatalf("%s: %d events, want %v", s.recording, len(evs), s.want)
				}
				for i, e := range evs {
					checkEvent(t, s, tt.provider, tt.session, native, s.want[i], e, i < len(evs)-1)
				}
			}
		})
	}
}

func checkEvent(t *testing.T, s step, p providers.ID, session, native string, want providers.EventKind, e providers.Event, derived bool) {
	t.Helper()
	name := s.recording + "/" + string(want)
	if e.Event != want || e.Version != 1 || e.Provider != p || e.NativeEvent != native {
		t.Errorf("%s: event %q version %d provider %q native %q", name, e.Event, e.Version, e.Provider, e.NativeEvent)
	}
	if e.SessionID != session {
		t.Errorf("%s: session_id = %q, want %q", name, e.SessionID, session)
	}
	wantTurn := s.turn
	if want == providers.EventSessionStart {
		wantTurn = "" // no turn before the first prompt
	}
	if e.TurnID != wantTurn {
		t.Errorf("%s: turn_id = %q, want %q", name, e.TurnID, wantTurn)
	}
	if e.AgentID != "a1" || e.TableID != "3-login" || !e.ReceivedAt.Equal(at) {
		t.Errorf("%s: agent %q table %q received_at %v", name, e.AgentID, e.TableID, e.ReceivedAt)
	}
	if e.Cwd != wantPath("/home/user/p") {
		t.Errorf("%s: cwd = %q", name, e.Cwd)
	}
	if want.ToolEvent() {
		if e.Tool == nil {
			t.Fatalf("%s: no tool", name)
		}
		if e.Tool.Kind != s.toolKind || e.Tool.Command != s.command || len(e.Tool.RawInput) == 0 {
			t.Errorf("%s: tool = %+v, want kind %q command %q", name, *e.Tool, s.toolKind, s.command)
		}
	} else if e.Tool != nil {
		t.Errorf("%s: tool %+v on a non-tool event", name, *e.Tool)
	}
	if s.ok != nil && !derived && (e.ToolResult == nil || e.ToolResult.OK != *s.ok) {
		t.Errorf("%s: tool_result = %+v, want ok %v", name, e.ToolResult, *s.ok)
	}
	if !derived && e.StopReason != s.stop {
		t.Errorf("%s: stop_reason = %q, want %q", name, e.StopReason, s.stop)
	}
}

// A Codex Stop is turn_failure when the reading of the transcript says so
// (the reading itself is codex.md's).
func TestCodexStopIsAFailureWhenTheTranscriptSaysSo(t *testing.T) {
	t.Parallel()
	var seen string
	a := providers.NewCodex(providers.Options{Resolve: noLinks, CodexTurnFailure: func(stop providers.Event) (providers.StopReason, string, bool) {
		seen = stop.TranscriptPath
		return providers.StopRateLimit, "usage limit reached", true
	}})
	e, err := a.NormalizeHook(providers.HookInput{NativeEvent: "Stop", Stdin: recorded(t, "codex", "Stop")})
	if err != nil {
		t.Fatal(err)
	}
	if e.Event != providers.EventTurnFailure || e.StopReason != providers.StopRateLimit || e.Error != "usage limit reached" || e.TurnID != "t1" {
		t.Errorf("event = %+v", e)
	}
	if seen != "/home/user/.codex/sessions/rollout.jsonl" {
		t.Errorf("transcript given to the reader = %q", seen)
	}
}

func TestUnknownAndUnmappedNativeEvents(t *testing.T) {
	t.Parallel()
	reg := providers.NewRegistry(providers.Options{Resolve: noLinks})
	for _, id := range []providers.ID{providers.ClaudeCode, providers.Codex, providers.Antigravity, providers.Null} {
		a, _ := reg.For(id)
		if _, err := a.NormalizeHook(providers.HookInput{NativeEvent: "BrandNewEvent", Stdin: []byte(`{}`)}); !errors.Is(err, providers.ErrUnknownEvent) {
			t.Errorf("%q: unknown event: err = %v", id, err)
		}
	}
	agy, _ := reg.For(providers.Antigravity)
	e, err := agy.NormalizeHook(providers.HookInput{NativeEvent: "PostInvocation", Stdin: recorded(t, "antigravity", "PostInvocation")})
	if err != nil || e.Event != providers.EventNone {
		t.Errorf("PostInvocation = %q, %v; want no Mestre event (only logged)", e.Event, err)
	}
	claude, _ := reg.For(providers.ClaudeCode)
	if _, err := claude.NormalizeHook(providers.HookInput{NativeEvent: "Stop", Stdin: []byte(`not json`)}); err == nil {
		t.Error("a payload that is not JSON was accepted")
	}
}

// The tool table of each provider, and its fallback: a tool or a field
// outside the map is `other`, decided by the class default.
func TestToolKindsAndTheOtherFallback(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	in := func(p string) string { return filepath.Join(root, p) }
	claudePre := func(tool string, input any) []byte {
		b, _ := json.Marshal(map[string]any{"session_id": "s", "prompt_id": "p", "cwd": root, "hook_event_name": "PreToolUse", "tool_name": tool, "tool_input": input})
		return b
	}
	codexPre := func(tool string, input any) []byte {
		b, _ := json.Marshal(map[string]any{"session_id": "s", "turn_id": "t", "cwd": root, "hook_event_name": "PreToolUse", "tool_name": tool, "tool_input": input})
		return b
	}
	agyPre := func(tool string, args map[string]any) []byte {
		b, _ := json.Marshal(map[string]any{"conversationId": "c", "toolCall": map[string]any{"name": tool, "args": args}, "workspacePaths": []string{root}})
		return b
	}
	patch := "*** Begin Patch\n*** Add File: new.go\n+package x\n*** End Patch\n"
	tests := []struct {
		name     string
		provider providers.ID
		stdin    []byte
		kind     providers.ToolKind
		command  string
		paths    []string
		url      string
	}{
		{"claude Bash", providers.ClaudeCode, claudePre("Bash", map[string]any{"command": "go test"}), providers.KindShell, "go test", nil, ""},
		{"claude PowerShell", providers.ClaudeCode, claudePre("PowerShell", map[string]any{"command": "dir"}), providers.KindShell, "dir", nil, ""},
		{"claude Read", providers.ClaudeCode, claudePre("Read", map[string]any{"file_path": in("a.go")}), providers.KindRead, "", []string{in("a.go")}, ""},
		{"claude Write, relative", providers.ClaudeCode, claudePre("Write", map[string]any{"file_path": "sub/../b.go"}), providers.KindWrite, "", []string{in("b.go")}, ""},
		{"claude NotebookEdit", providers.ClaudeCode, claudePre("NotebookEdit", map[string]any{"notebook_path": in("n.ipynb")}), providers.KindEdit, "", []string{in("n.ipynb")}, ""},
		{"claude Grep without path searches cwd", providers.ClaudeCode, claudePre("Grep", map[string]any{"pattern": "x"}), providers.KindSearch, "", []string{root}, ""},
		{"claude WebFetch", providers.ClaudeCode, claudePre("WebFetch", map[string]any{"url": "https://go.dev"}), providers.KindWeb, "", nil, "https://go.dev"},
		{"claude Agent", providers.ClaudeCode, claudePre("Agent", map[string]any{"prompt": "x"}), providers.KindSubagent, "", nil, ""},
		{"claude mcp", providers.ClaudeCode, claudePre("mcp__browser__open", map[string]any{}), providers.KindMCP, "", nil, ""},
		{"claude TodoWrite", providers.ClaudeCode, claudePre("TodoWrite", map[string]any{}), providers.KindInternal, "", nil, ""},
		{"claude unmapped tool", providers.ClaudeCode, claudePre("BrandNewTool", map[string]any{"file_path": in("a")}), providers.KindOther, "", nil, ""},
		{"claude Bash without command", providers.ClaudeCode, claudePre("Bash", map[string]any{"cmd": "ls"}), providers.KindOther, "", nil, ""},
		{"claude Edit without a known path field", providers.ClaudeCode, claudePre("Edit", map[string]any{"path": in("a")}), providers.KindOther, "", nil, ""},
		{"codex Bash", providers.Codex, codexPre("Bash", map[string]any{"command": "ls"}), providers.KindShell, "ls", nil, ""},
		{"codex apply_patch adding a file", providers.Codex, codexPre("apply_patch", map[string]any{"command": patch}), providers.KindWrite, "", []string{in("new.go")}, ""},
		{"codex apply_patch updating", providers.Codex, codexPre("apply_patch", map[string]any{"input": "*** Update File: a.go\n*** Delete File: b.go\n"}), providers.KindEdit, "", []string{in("a.go"), in("b.go")}, ""},
		{"codex apply_patch without headers", providers.Codex, codexPre("apply_patch", map[string]any{"command": "garbage"}), providers.KindOther, "", nil, ""},
		{"codex update_plan", providers.Codex, codexPre("update_plan", map[string]any{}), providers.KindInternal, "", nil, ""},
		{"codex mcp", providers.Codex, codexPre("mcp__fs__read", map[string]any{}), providers.KindMCP, "", nil, ""},
		{"antigravity run_command", providers.Antigravity, agyPre("run_command", map[string]any{"CommandLine": "ls", "Cwd": root}), providers.KindShell, "ls", nil, ""},
		{"antigravity browser tool", providers.Antigravity, agyPre("browser_click", map[string]any{}), providers.KindWeb, "", nil, ""},
		{"antigravity subagent", providers.Antigravity, agyPre("invoke_subagent", map[string]any{}), providers.KindSubagent, "", nil, ""},
		{"antigravity internal", providers.Antigravity, agyPre("ask_permission", map[string]any{}), providers.KindInternal, "", nil, ""},
		{"antigravity generate_image", providers.Antigravity, agyPre("generate_image", map[string]any{}), providers.KindOther, "", nil, ""},
		// The path arguments of the file tools are not recorded yet: other.
		{"antigravity write_to_file, path field not in the map", providers.Antigravity, agyPre("write_to_file", map[string]any{"TargetFile": in("a")}), providers.KindOther, "", nil, ""},
	}
	reg := providers.NewRegistry(providers.Options{Resolve: func(p string) (string, error) {
		if p == root || len(p) < len(root) { // the temporary folder and its parents exist; nothing under it does
			return p, nil
		}
		return "", fs.ErrNotExist
	}})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a, _ := reg.For(tt.provider)
			e, err := a.NormalizeHook(providers.HookInput{NativeEvent: "PreToolUse", Stdin: tt.stdin})
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			for _, p := range tt.paths {
				want = append(want, fold(p))
			}
			if e.Tool.Kind != tt.kind || e.Tool.Command != tt.command || e.Tool.URL != tt.url || strings.Join(e.Tool.Paths, "|") != strings.Join(want, "|") {
				t.Errorf("tool = %+v, want kind %q command %q paths %v url %q", *e.Tool, tt.kind, tt.command, want, tt.url)
			}
		})
	}
}

func fold(p string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(p)
	}
	return p
}

// Paths are absolute, without "..", with links resolved even when the file
// does not exist yet.
func TestPathsResolveLinksOfTheExistingPrefix(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	link, target := filepath.Join(root, "link"), filepath.Join(root, "real")
	a := providers.NewClaudeCode(providers.Options{Resolve: func(p string) (string, error) {
		switch {
		case p == link:
			return target, nil
		case len(p) <= len(root):
			return p, nil
		}
		return "", fs.ErrNotExist
	}})
	stdin, _ := json.Marshal(map[string]any{"session_id": "s", "cwd": root, "tool_name": "Write",
		"tool_input": map[string]any{"file_path": filepath.Join(link, "x", "..", "new.txt")}})
	e, err := a.NormalizeHook(providers.HookInput{NativeEvent: "PreToolUse", Stdin: stdin})
	if err != nil {
		t.Fatal(err)
	}
	if want := fold(filepath.Join(target, "new.txt")); len(e.Tool.Paths) != 1 || e.Tool.Paths[0] != want {
		t.Errorf("paths = %v, want [%s]", e.Tool.Paths, want)
	}
	// NormalizePath is the same normalization, for the paths the permission
	// decision reads from command arguments.
	o := providers.Options{Resolve: func(p string) (string, error) {
		switch {
		case p == link:
			return target, nil
		case len(p) <= len(root):
			return p, nil
		}
		return "", fs.ErrNotExist
	}}
	if got, want := o.NormalizePath(filepath.Join("link", "x", "..", "new.txt"), root), fold(filepath.Join(target, "new.txt")); got != want {
		t.Errorf("NormalizePath = %s, want %s", got, want)
	}
}
