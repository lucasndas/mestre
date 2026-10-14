package providers

import (
	"encoding/json"
	"unicode/utf8"
)

// ReplyKind is what the dispatcher answers a hook call, before the format of
// the provider (docs/provedores/despachante-de-hooks.md#respostas-por-provedor).
type ReplyKind int

// The replies.
const (
	// ReplyNeutral changes nothing: the call without MESTRE_AGENT_ID, an
	// unknown native event, an informative event.
	ReplyNeutral ReplyKind = iota
	// ReplyAllow lets the tool run (pre_tool, permission_request).
	ReplyAllow
	// ReplyDeny blocks the tool, with the reason the agent sees.
	ReplyDeny
	// ReplyContext gives the context of the session or of the turn
	// (session_start, turn_start).
	ReplyContext
	// ReplyError is an internal error of the hook itself: it blocks in
	// pre_tool and permission_request and is neutral in the other events.
	ReplyError
)

// Reply is the answer to one hook call.
type Reply struct {
	Kind ReplyKind
	// Reason goes with a denial and an internal error.
	Reason string
	// Context goes with ReplyContext.
	Context string
}

// HookOutput is what `mestre hook` prints and the code it exits with: only
// 0 or 2, never 1 (Claude Code does not block on 1).
type HookOutput struct {
	Stdout []byte
	Stderr []byte
	Exit   int
}

// ClaudeMaxContext is the limit of Claude Code for an additionalContext
// (docs/provedores/claude-code.md).
const ClaudeMaxContext = 10_000

// formatHookSpecific is the format of Claude Code and Codex: exit 0 with
// hookSpecificOutput, exit 2 with the reason on stderr for an internal
// error of a decision. Nothing at all in the other events: Codex takes
// plain text in Stop and Interrupt as invalid.
func formatHookSpecific(native string, kind EventKind, r Reply, maxContext int) HookOutput {
	decision := kind == EventPreTool || kind == EventPermissionRequest
	switch {
	case r.Kind == ReplyError && decision:
		return HookOutput{Stderr: []byte(oneLine(r.Reason) + "\n"), Exit: 2}
	case r.Kind == ReplyAllow || r.Kind == ReplyDeny:
		if !decision {
			return HookOutput{}
		}
		out := map[string]any{"hookEventName": native}
		if kind == EventPermissionRequest {
			d := map[string]any{"behavior": "allow"}
			if r.Kind == ReplyDeny {
				d["behavior"], d["message"] = "deny", r.Reason
			}
			out["decision"] = d
		} else if r.Kind == ReplyAllow {
			out["permissionDecision"] = "allow"
		} else {
			out["permissionDecision"], out["permissionDecisionReason"] = "deny", r.Reason
		}
		return jsonOutput(map[string]any{"hookSpecificOutput": out})
	case r.Kind == ReplyContext && r.Context != "" && (kind == EventSessionStart || kind == EventTurnStart):
		ctx := r.Context
		if maxContext > 0 {
			ctx = cutRunes(ctx, maxContext)
		}
		return jsonOutput(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": native, "additionalContext": ctx}})
	}
	return HookOutput{}
}

// FormatResponse formats a reply for Claude Code.
func (c *claudeCode) FormatResponse(nativeEvent string, r Reply) HookOutput {
	return formatHookSpecific(nativeEvent, claudeEvents[nativeEvent], r, ClaudeMaxContext)
}

// FormatResponse formats a reply for Codex.
func (c *codex) FormatResponse(nativeEvent string, r Reply) HookOutput {
	return formatHookSpecific(nativeEvent, codexEvents[nativeEvent], r, 0)
}

// FormatResponse formats a reply for Antigravity: always exit 0 and valid
// JSON, because `{}`, invalid output, an exit other than 0 or a timeout
// block. PreToolUse always carries an explicit decision: an internal error
// is a deny; PreInvocation carries the context in injectSteps; the other
// events answer `{}`.
func (a *antigravity) FormatResponse(nativeEvent string, r Reply) HookOutput {
	switch antigravityEvents[nativeEvent] {
	case EventPreTool:
		if r.Kind == ReplyDeny || r.Kind == ReplyError {
			return jsonOutput(map[string]any{"decision": "deny", "reason": r.Reason})
		}
		return jsonOutput(map[string]any{"decision": "allow"})
	case EventTurnStart:
		if r.Kind == ReplyContext && r.Context != "" {
			return jsonOutput(map[string]any{"injectSteps": []any{map[string]any{"ephemeralMessage": r.Context}}})
		}
	}
	return HookOutput{Stdout: []byte("{}\n")}
}

// FormatResponse: no hook calls the null adapter; nothing, exit 0.
func (n *null) FormatResponse(string, Reply) HookOutput { return HookOutput{} }

func jsonOutput(v any) HookOutput {
	b, err := json.Marshal(v)
	if err != nil { // maps of strings always encode
		panic(err)
	}
	return HookOutput{Stdout: append(b, '\n')}
}

// oneLine keeps a reason on one line of stderr.
func oneLine(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r == '\n' || r == '\r' {
			out[i] = ' '
		}
	}
	return string(out)
}

// cutRunes keeps at most n characters.
func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	i := 0
	for j := range s {
		if i == n {
			return s[:j]
		}
		i++
	}
	return s
}
