package fakeagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

// What the fake agent does only as Claude Code
// (docs/provedores/claude-code.md): the hooks beyond the first five, the
// statusLine, the permission screen, the usage limit menu and the end of the
// session.

// claudeSettingsFiles are the settings files the fake reads for the
// statusLine, the --settings file first.
func (a *agent) claudeSettingsFiles() []string {
	var files []string
	if a.settingsArg != "" {
		files = append(files, a.resolve(a.settingsArg))
	}
	return append(files,
		filepath.Join(a.dir, ".claude", "settings.local.json"),
		filepath.Join(a.dir, ".claude", "settings.json"),
		filepath.Join(a.accountDir(), "settings.json"),
	)
}

// claudeStatusLine is the command of the statusLine, "" when no settings
// file declares one (or the provider is not Claude Code).
func (a *agent) claudeStatusLine() string {
	if a.p != Claude {
		return ""
	}
	for _, f := range a.claudeSettingsFiles() {
		m, err := readJSONMap(f)
		if err != nil {
			continue
		}
		if sl, ok := m["statusLine"].(map[string]any); ok {
			if t, _ := sl["type"].(string); t == "" || t == "command" {
				if c, _ := sl["command"].(string); c != "" {
					return c
				}
			}
		}
	}
	return ""
}

// runStatusLine runs the statusLine command with the recorded payload and
// the usage of the session so far: 1 cent and 2% of the context per turn.
func (a *agent) runStatusLine(ctx context.Context) {
	if a.statusLine == "" || ctx.Err() != nil {
		return
	}
	a.mu.Lock()
	turn := a.turn
	a.mu.Unlock()
	pl, ok := a.payload("StatusLine", a.common(map[string]any{
		"cost": map[string]any{
			"total_cost_usd": 0.01 * float64(turn), "total_duration_ms": 1000 * turn, "total_api_duration_ms": 200 * turn,
			"total_lines_added": 0, "total_lines_removed": 0,
		},
		"context_window": map[string]any{
			"total_input_tokens": 1000 * turn, "total_output_tokens": 300 * turn, "context_window_size": 200000,
			"used_percentage": 2 * turn, "remaining_percentage": 100 - 2*turn,
		},
	}))
	if !ok {
		return
	}
	hctx, cancel := clock.WithTimeout(ctx, a.clk, 10*time.Second)
	defer cancel()
	_, _ = a.runHandler(hctx, "StatusLine", a.statusLine, pl, 10*time.Second)
}

// runHandler runs one hook command and logs it among the hooks fired.
func (a *agent) runHandler(ctx context.Context, event, command string, payload []byte, timeout time.Duration) (HookResult, error) {
	res, err := a.cfg.Hooks.RunHook(ctx, HookCall{
		Event: event, Command: command, Dir: a.dir, Env: a.cfg.Env, Stdin: payload, Timeout: timeout,
	})
	if err == nil && ctx.Err() != nil {
		err = context.Cause(ctx)
	}
	a.mu.Lock()
	a.hookLog = append(a.hookLog, event)
	a.mu.Unlock()
	return res, err
}

// fireClaude fires an informative hook event of Claude Code.
func (a *agent) fireClaude(ctx context.Context, event string, extra map[string]any) {
	if pl, ok := a.payload(event, a.common(extra)); ok {
		a.fire(ctx, event, "", pl)
	}
}

// endSession fires SessionEnd, when the user leaves with /exit or Ctrl-C
// twice.
func (a *agent) endSession(ctx context.Context) {
	if a.p == Claude {
		a.fireClaude(ctx, "SessionEnd", map[string]any{"reason": "exit"})
	}
}

// claudePermission is the permission of a tool the settings do not allow:
// the PermissionRequest hook decides (exit 2 or behavior deny denies, allow
// allows) and, without a decision, the agent shows the screen, a
// notification fires and the answer decides. Option 1 is "Yes" once, the
// last one is "No"; Esc is "No".
func (a *agent) claudePermission(ctx context.Context, name string, vals map[string]any) bool {
	if pl, ok := a.payload("PermissionRequest", vals); ok {
		for _, h := range a.hooks {
			if h.event != "PermissionRequest" || !h.matches(name) || !h.trusted {
				continue
			}
			hctx, cancel := clock.WithTimeout(ctx, a.clk, h.timeout)
			res, err := a.runHandler(hctx, "PermissionRequest", h.command, pl, h.timeout)
			cancel()
			if err != nil {
				continue // a hook that fails does not decide
			}
			if res.ExitCode == 2 {
				return false
			}
			var out struct {
				Hook struct {
					Decision struct {
						Behavior string `json:"behavior"`
					} `json:"decision"`
				} `json:"hookSpecificOutput"`
			}
			if json.Unmarshal(res.Stdout, &out) == nil {
				switch out.Hook.Decision.Behavior {
				case "allow":
					return true
				case "deny":
					return false
				}
			}
		}
	}
	a.fireClaude(ctx, "Notification", map[string]any{
		"message": "Claude needs your permission to use " + name, "notification_type": "permission_prompt",
	})
	choice, err := a.claudeDialog(ctx, fmt.Sprintf("\r\n%s\r\n\r\n   %s\r\n\r\nDo you want to proceed?\r\n", name, toolSummary(vals)), []string{
		"Yes", "Yes, and don't ask again for " + name + " in this folder", "No, and tell Claude what to do differently (esc)",
	})
	return err == nil && choice <= 1 // "Yes" and "Yes, and don't ask again" both let it run
}

// toolSummary is the line of the permission screen about what the tool does.
func toolSummary(vals map[string]any) string {
	if in, ok := vals["tool_input"].(map[string]any); ok {
		for _, k := range []string{"command", "file_path"} {
			if s, ok := in[k].(string); ok {
				return s
			}
		}
	}
	return ""
}

// claudeLimit hits the usage limit: the message, the menu "What do you want
// to do?" and, after "Stop and wait for limit to reset", the StopFailure
// with rate_limit that ends the turn.
func (a *agent) claudeLimit(ctx context.Context, kind string) error {
	if a.p != Claude {
		return notImplemented("%s: the usage limit is emulated for claude only", a.p)
	}
	if kind != "session" && kind != "weekly" {
		return notImplemented("claude usage limit %q (the fake has session and weekly)", kind)
	}
	choice, err := a.claudeDialog(ctx, fmt.Sprintf("You've hit your %s limit · resets 3pm (UTC)\r\n/upgrade to increase your usage limit.\r\n\r\nWhat do you want to do?\r\n", kind),
		[]string{"Stop and wait for limit to reset", "Upgrade your plan"})
	if err != nil {
		return nil // the turn was interrupted
	}
	if choice != 0 {
		return notImplemented("claude usage limit menu: option %d (the fake only waits)", choice+1)
	}
	a.print("Stopped. Waiting for the limit to reset…\r\n")
	a.turnFailed = true
	a.fireClaude(ctx, "StopFailure", map[string]any{"error": "rate_limit", "error_details": kind + " limit reached"})
	return nil
}

// claudeDialog draws a dialog of Claude Code, the message and the options,
// and takes the answer from the keys of the turn: arrows move, Enter picks,
// Esc is the last option. The dialog is erased when it is answered, as the
// real screen is, so what the screen shows afterwards is not a question.
func (a *agent) claudeDialog(ctx context.Context, message string, opts []string) (int, error) {
	a.drainKeys()
	lines := strings.Count(message, "\n") + len(opts)
	sel := 0
	draw := func(again bool) {
		var b strings.Builder
		if again {
			fmt.Fprintf(&b, "\x1b[%dA", len(opts))
		}
		for i, o := range opts {
			mark := "  "
			if i == sel {
				mark = "❯ "
			}
			fmt.Fprintf(&b, "%s%d. %s\x1b[K\r\n", mark, i+1, o)
		}
		a.print(b.String())
	}
	answered := func(choice int) (int, error) {
		a.print(fmt.Sprintf("\x1b[%dA\x1b[0J", lines))
		return choice, nil
	}
	a.print(message)
	draw(false)
	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case ev := <-a.keys:
			switch ev.kind {
			case evUp:
				sel = (sel + len(opts) - 1) % len(opts)
				draw(true)
			case evDown:
				sel = (sel + 1) % len(opts)
				draw(true)
			case evEnter:
				return answered(sel)
			case evEscape:
				return answered(len(opts) - 1)
			}
		}
	}
}

// drainKeys forgets the keys typed before a screen was drawn.
func (a *agent) drainKeys() {
	for {
		select {
		case <-a.keys:
		default:
			return
		}
	}
}

// layerText is the content of the file of --append-system-prompt-file, for
// /fake-dump.
func (a *agent) layerText() string {
	if a.layerFile == "" {
		return ""
	}
	b, err := os.ReadFile(a.resolve(a.layerFile))
	if err != nil {
		return "unreadable: " + strings.ReplaceAll(err.Error(), a.dir, ".")
	}
	return string(b)
}
