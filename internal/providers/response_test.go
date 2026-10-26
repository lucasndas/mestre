package providers_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/providers"
)

func adapter(t *testing.T, id providers.ID) providers.Adapter {
	t.Helper()
	a, err := providers.NewRegistry(providers.Options{}).For(id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func decodeObject(t *testing.T, out providers.HookOutput) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(out.Stdout, &m); err != nil {
		t.Fatalf("stdout %q is not a JSON object: %v", out.Stdout, err)
	}
	return m
}

// CA-HOOK-05: for each decision, the Antigravity stdout is valid JSON with
// an explicit decision, and the exit code is 0.
func TestAntigravityDecisionsAreJSONWithAnExplicitDecision(t *testing.T) {
	t.Parallel()
	a := adapter(t, providers.Antigravity)
	for _, c := range []struct {
		reply  providers.Reply
		want   string
		reason string
	}{
		{providers.Reply{Kind: providers.ReplyAllow}, "allow", ""},
		{providers.Reply{Kind: providers.ReplyDeny, Reason: "fora de files.write"}, "deny", "fora de files.write"},
	} {
		out := a.FormatResponse("PreToolUse", c.reply)
		m := decodeObject(t, out)
		if out.Exit != 0 || m["decision"] != c.want {
			t.Fatalf("%v: exit %d, stdout %s", c.reply.Kind, out.Exit, out.Stdout)
		}
		if c.reason != "" && m["reason"] != c.reason {
			t.Fatalf("deny without its reason: %s", out.Stdout)
		}
	}
}

// The rest of the table of docs/provedores/despachante-de-hooks.md for
// Antigravity: neutral and internal error in PreToolUse are explicit, the
// context goes in injectSteps of PreInvocation, every other event is {}.
func TestAntigravityOtherReplies(t *testing.T) {
	t.Parallel()
	a := adapter(t, providers.Antigravity)
	if m := decodeObject(t, a.FormatResponse("PreToolUse", providers.Reply{})); m["decision"] != "allow" {
		t.Fatalf("neutral PreToolUse = %v", m)
	}
	out := a.FormatResponse("PreToolUse", providers.Reply{Kind: providers.ReplyError, Reason: "bug"})
	if m := decodeObject(t, out); out.Exit != 0 || m["decision"] != "deny" || m["reason"] != "bug" {
		t.Fatalf("internal error = exit %d %s", out.Exit, out.Stdout)
	}
	m := decodeObject(t, a.FormatResponse("PreInvocation", providers.Reply{Kind: providers.ReplyContext, Context: "decreto 3"}))
	steps, _ := m["injectSteps"].([]any)
	if len(steps) != 1 || steps[0].(map[string]any)["ephemeralMessage"] != "decreto 3" {
		t.Fatalf("context = %v", m)
	}
	for _, ev := range []string{"PreInvocation", "PostToolUse", "Stop", "PostInvocation", "Unheard"} {
		for _, k := range []providers.ReplyKind{providers.ReplyNeutral, providers.ReplyError, providers.ReplyAllow} {
			out := a.FormatResponse(ev, providers.Reply{Kind: k, Reason: "x"})
			if out.Exit != 0 || strings.TrimSpace(string(out.Stdout)) != "{}" {
				t.Fatalf("%s %v = exit %d %q", ev, k, out.Exit, out.Stdout)
			}
		}
	}
}

// Claude Code and Codex: exit 0 with hookSpecificOutput for the decisions
// and the context, exit 2 with the reason on stderr for an internal error
// of a decision, and nothing at all elsewhere (never exit 1).
func TestClaudeAndCodexReplies(t *testing.T) {
	t.Parallel()
	for _, id := range []providers.ID{providers.ClaudeCode, providers.Codex} {
		a := adapter(t, id)
		hso := func(native string, r providers.Reply) map[string]any {
			t.Helper()
			out := a.FormatResponse(native, r)
			if out.Exit != 0 || len(out.Stderr) != 0 {
				t.Fatalf("%s %s: exit %d stderr %q", id, native, out.Exit, out.Stderr)
			}
			h, _ := decodeObject(t, out)["hookSpecificOutput"].(map[string]any)
			if h["hookEventName"] != native {
				t.Fatalf("%s %s: %s", id, native, out.Stdout)
			}
			return h
		}
		if id == providers.Codex {
			// Codex 0.162.1 takes permissionDecision "allow" as unsupported
			// (the hook fails): the allow is no output at all.
			if out := a.FormatResponse("PreToolUse", providers.Reply{Kind: providers.ReplyAllow}); out.Exit != 0 || len(out.Stdout)+len(out.Stderr) != 0 {
				t.Fatalf("codex allow = %+v", out)
			}
		} else if h := hso("PreToolUse", providers.Reply{Kind: providers.ReplyAllow}); h["permissionDecision"] != "allow" {
			t.Fatalf("%s allow = %v", id, h)
		}
		if h := hso("PreToolUse", providers.Reply{Kind: providers.ReplyDeny, Reason: "não"}); h["permissionDecision"] != "deny" || h["permissionDecisionReason"] != "não" {
			t.Fatalf("%s deny = %v", id, h)
		}
		d, _ := hso("PermissionRequest", providers.Reply{Kind: providers.ReplyDeny, Reason: "não"})["decision"].(map[string]any)
		if d["behavior"] != "deny" || d["message"] != "não" {
			t.Fatalf("%s permission deny = %v", id, d)
		}
		d, _ = hso("PermissionRequest", providers.Reply{Kind: providers.ReplyAllow})["decision"].(map[string]any)
		if d["behavior"] != "allow" {
			t.Fatalf("%s permission allow = %v", id, d)
		}
		if h := hso("UserPromptSubmit", providers.Reply{Kind: providers.ReplyContext, Context: "c"}); h["additionalContext"] != "c" {
			t.Fatalf("%s context = %v", id, h)
		}
		for _, native := range []string{"PreToolUse", "PermissionRequest"} {
			out := a.FormatResponse(native, providers.Reply{Kind: providers.ReplyError, Reason: "erro\ninterno"})
			if out.Exit != 2 || len(out.Stdout) != 0 || string(out.Stderr) != "erro interno\n" {
				t.Fatalf("%s %s internal error = %+v", id, native, out)
			}
		}
		for _, native := range []string{"Stop", "PostToolUse", "SessionEnd", "Unheard", "PreToolUse"} {
			for _, k := range []providers.ReplyKind{providers.ReplyNeutral, providers.ReplyContext} {
				if out := a.FormatResponse(native, providers.Reply{Kind: k}); out.Exit != 0 || len(out.Stdout)+len(out.Stderr) != 0 {
					t.Fatalf("%s %s %v = %+v", id, native, k, out)
				}
			}
			if native != "PreToolUse" {
				if out := a.FormatResponse(native, providers.Reply{Kind: providers.ReplyError}); out.Exit != 0 || len(out.Stdout) != 0 {
					t.Fatalf("%s %s internal error = %+v", id, native, out)
				}
			}
		}
	}
	long := strings.Repeat("é", providers.ClaudeMaxContext+5)
	out := adapter(t, providers.ClaudeCode).FormatResponse("SessionStart", providers.Reply{Kind: providers.ReplyContext, Context: long})
	ctx := decodeObject(t, out)["hookSpecificOutput"].(map[string]any)["additionalContext"].(string)
	if n := len([]rune(ctx)); n != providers.ClaudeMaxContext {
		t.Fatalf("Claude context of %d characters", n)
	}
}
