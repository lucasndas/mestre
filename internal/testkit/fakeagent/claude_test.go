package fakeagent_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/testkit"
	"github.com/lucasndas/mestre/internal/testkit/fakeagent"
	"github.com/lucasndas/mestre/pkg/clock"
)

// allClaudeHooks binds every event the fake fires as Claude Code, each to a
// command named after the event.
func allClaudeHooks() string {
	var groups []string
	for _, ev := range []string{
		"SessionStart", "UserPromptSubmit", "PreToolUse", "PermissionRequest", "PostToolUse", "PostToolUseFailure",
		"Stop", "StopFailure", "Notification", "PreCompact", "SessionEnd",
	} {
		groups = append(groups, fmt.Sprintf(`%q: [{"hooks": [{"type": "command", "command": "hook-%s"}]}]`, ev, ev))
	}
	return `{"statusLine": {"type": "command", "command": "status-line"}, "hooks": {` + strings.Join(groups, ", ") + `}}`
}

// The payloads of the events the fake fires beyond the first five have the
// fields of the recordings, and the statusLine gets the usage of the session.
func TestClaudeFakeFiresTheOtherEventsWithTheRecordedShape(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := &hooks{}
		l := &fakeagent.MemLauncher{Clock: clock.Real{}, Hooks: h, Commands: failing{}, Reporter: t}
		e := claudeEnv(t)
		testkit.WriteFiles(t, e.dir, map[string]string{
			".claude/settings.local.json": allClaudeHooks(),
			"script.yaml": "prompts:\n  - match: go\n    steps:\n      - tool: {name: Bash, input: {command: \"ls\"}, ask: true}\n      - run: \"fail now\"\n" +
				"  - match: limit\n    steps:\n      - limit: session\n",
		})
		e.vars = append(e.vars, "FAKEAGENT_SCRIPT="+filepath.Join(e.dir, "script.yaml"))
		p := start(t, l, "claude", e)
		synctest.Wait()

		typeLine(t, p, "go") // PermissionRequest, undecided: the screen
		if out := p.Output(); !strings.Contains(out, "Do you want to proceed?") {
			t.Fatalf("no permission screen:\n%s", out)
		}
		if _, err := p.PTY().Write([]byte("\r")); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		typeLine(t, p, "limit")
		if _, err := p.PTY().Write([]byte("\r")); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		typeLine(t, p, "/compact")
		typeLine(t, p, "/exit")
		if err := p.Wait(); err != nil {
			t.Fatalf("Wait = %v", err)
		}

		seen := map[string]bool{}
		h.mu.Lock()
		defer h.mu.Unlock()
		for _, c := range h.calls {
			seen[c.Event] = true
			switch c.Event {
			case "PermissionRequest", "PostToolUseFailure", "StopFailure", "Notification", "PreCompact", "SessionEnd":
				sameShape(t, c.Stdin, "claude-code", c.Event)
			case "StatusLine":
				g := sameShape(t, c.Stdin, "claude-code", "StatusLine")
				if cost, _ := g["cost"].(map[string]any); cost["total_cost_usd"].(float64) <= 0 {
					t.Errorf("statusLine cost = %v", cost)
				}
				if c.Command != "status-line" {
					t.Errorf("statusLine ran %q", c.Command)
				}
			}
		}
		for _, ev := range []string{"PermissionRequest", "PostToolUseFailure", "StopFailure", "Notification", "PreCompact", "SessionEnd", "StatusLine"} {
			if !seen[ev] {
				t.Errorf("%s never fired", ev)
			}
		}
	})
}

// failing runs every command as an exit 1.
type failing struct{}

func (failing) RunCommand(context.Context, string, string, []string) (string, int, error) {
	return "no\n", 1, nil
}

// The permission request: the hook decides (allow, deny or exit 2); with no
// decision the screen asks, Enter is "Yes" once, Esc is "No".
func TestClaudeFakePermissionRequest(t *testing.T) {
	t.Parallel()
	const (
		allow = `{"hookSpecificOutput": {"hookEventName": "PermissionRequest", "decision": {"behavior": "allow"}}}`
		deny  = `{"hookSpecificOutput": {"hookEventName": "PermissionRequest", "decision": {"behavior": "deny", "message": "no"}}}`
	)
	tests := []struct {
		name   string
		answer fakeagent.HookResult
		key    string // what is typed on the screen; "" when the hook decides
		ran    bool
		asked  bool
	}{
		{"hook allows", fakeagent.HookResult{Stdout: []byte(allow)}, "", true, false},
		{"hook denies", fakeagent.HookResult{Stdout: []byte(deny)}, "", false, false},
		{"hook exits 2", fakeagent.HookResult{ExitCode: 2, Stderr: []byte("denied")}, "", false, false},
		{"no decision, Enter", fakeagent.HookResult{}, "\r", true, true},
		{"no decision, Esc", fakeagent.HookResult{}, "\x1b", false, true},
		{"no decision, down and Enter is the second option, still a yes", fakeagent.HookResult{}, "\x1b[B\r", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := &hooks{respond: func(c fakeagent.HookCall) (fakeagent.HookResult, error) {
					if c.Event == "PermissionRequest" {
						return tt.answer, nil
					}
					return fakeagent.HookResult{}, nil
				}}
				clk := clock.NewFake(time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC))
				l := &fakeagent.MemLauncher{Clock: clk, Hooks: h, Reporter: t}
				e := claudeEnv(t)
				testkit.WriteFiles(t, e.dir, map[string]string{
					".claude/settings.local.json": allClaudeHooks(),
					"script.yaml":                 "prompts:\n  - match: go\n    steps:\n      - tool: {name: Bash, input: {command: \"rm x\"}, ask: true}\n",
				})
				e.vars = append(e.vars, "FAKEAGENT_SCRIPT="+filepath.Join(e.dir, "script.yaml"))
				p := start(t, l, "claude", e)
				synctest.Wait()
				typeLine(t, p, "go")
				if asked := strings.Contains(p.Output(), "Do you want to proceed?"); asked != tt.asked {
					t.Fatalf("screen asked = %v, want %v:\n%s", asked, tt.asked, p.Output())
				}
				if tt.key != "" {
					if _, err := p.PTY().Write([]byte(tt.key)); err != nil {
						t.Fatal(err)
					}
					synctest.Wait()
					clk.Advance(time.Second) // an Esc alone is told by the quiet after it
					synctest.Wait()
				}
				if ran := strings.Contains(p.Output(), "Tool Bash ran"); ran != tt.ran {
					t.Errorf("tool ran = %v, want %v:\n%s", ran, tt.ran, p.Output())
				}
				h.mu.Lock()
				defer h.mu.Unlock()
				for _, c := range h.calls {
					if c.Event == "Notification" && !tt.asked {
						t.Error("a notification fired though the hook decided")
					}
				}
			})
		})
	}
}
