package fakeagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// What only the fake Codex does: the trust key and hash of a hook as Codex
// computes them (docs/provedores/codex.md#hooks), and the session
// transcript with the use (token_count) and the usage limit.

// codexRaw is the hooks.json of Codex as the hash reads it: a matcher that
// is absent differs from an empty one.
type codexRaw struct {
	Hooks map[string][]struct {
		Matcher *string `json:"matcher"`
		Hooks   []struct {
			Type          string  `json:"type"`
			Command       string  `json:"command"`
			Timeout       float64 `json:"timeout"`
			Async         bool    `json:"async"`
			StatusMessage *string `json:"statusMessage"`
		} `json:"hooks"`
	} `json:"hooks"`
}

// codexTrust gives the key and the hash of each handler of a hooks.json
// file, by event, group and handler: the key is "<file>:<event>:<group>:
// <handler>", the hash the SHA-256 of the compact JSON, keys sorted, of the
// event, the matcher Codex uses and the handler with its effective time
// limit (hooks/src/engine/discovery.rs of openai/codex; the real CLI is
// checked by the conformance test).
func codexTrust(file string, body []byte) (map[string][2]string, error) {
	var raw codexRaw
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	out := map[string][2]string{}
	for ev, groups := range raw.Hooks {
		label := snake(ev)
		for gi, g := range groups {
			for hi, h := range g.Hooks {
				timeout := int64(h.Timeout)
				switch {
				case ev == "SessionEnd" || ev == "Interrupt":
					timeout = min(max(timeout, 1), 3) // default 1 s, at most 3 s
				case timeout <= 0:
					timeout = 600
				}
				handler := map[string]any{"type": "command", "command": h.Command, "timeout": timeout, "async": h.Async}
				if h.StatusMessage != nil {
					handler["statusMessage"] = *h.StatusMessage
				}
				identity := map[string]any{"event_name": label, "hooks": []any{handler}}
				if g.Matcher != nil && !slices.Contains([]string{"UserPromptSubmit", "Stop", "Interrupt"}, ev) {
					identity["matcher"] = *g.Matcher
				}
				var b strings.Builder
				enc := json.NewEncoder(&b)
				enc.SetEscapeHTML(false)
				if err := enc.Encode(identity); err != nil {
					return nil, err
				}
				sum := sha256.Sum256([]byte(strings.TrimSuffix(b.String(), "\n")))
				id := fmt.Sprintf("%s\x00%d\x00%d", ev, gi, hi)
				out[id] = [2]string{fmt.Sprintf("%s:%s:%d:%d", file, label, gi, hi), "sha256:" + hex.EncodeToString(sum[:])}
			}
		}
	}
	return out, nil
}

// codexTranscript is the session transcript of the fake Codex, where the
// real one keeps it: $CODEX_HOME/sessions/AAAA/MM/DD/rollout-<when>-<id>.jsonl.
func (a *agent) codexTranscript() string {
	now := a.clk.Now().UTC()
	return filepath.Join(a.accountDir(), "sessions", now.Format("2006"), now.Format("01"), now.Format("02"),
		"rollout-"+now.Format("2006-01-02T15-04-05")+"-"+a.sessionID+".jsonl")
}

// codexLine appends one event_msg line to the transcript.
func (a *agent) codexLine(payload map[string]any) {
	line := map[string]any{"timestamp": a.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z"), "type": "event_msg", "payload": payload}
	b, err := json.Marshal(line)
	if err != nil {
		return
	}
	path := a.transcript
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(append(b, '\n'))
	_ = f.Close()
}

// codexRecordedLine is the payload of the recorded transcript line of a
// type, the template the fake fills.
func (a *agent) codexRecordedLine(typ string, reached bool) map[string]any {
	body, ok := a.recording("rollout")
	if !ok {
		return nil
	}
	var lines []struct {
		Payload map[string]any `json:"payload"`
	}
	if json.Unmarshal(body, &lines) != nil {
		return nil
	}
	for _, l := range lines {
		if l.Payload["type"] != typ {
			continue
		}
		if typ == "token_count" {
			rl, _ := l.Payload["rate_limits"].(map[string]any)
			if (rl["rate_limit_reached_type"] != nil) != reached {
				continue
			}
		}
		return l.Payload
	}
	return nil
}

// codexTurnStart and codexTurnEnd write the turn in the transcript: its
// start, the use (cumulative totals of the session) and its end. With
// FAKEAGENT_CODEX_USAGE_LIMIT=1 every turn ends at the usage limit, as the
// real one: rate_limit_reached_type filled, task_complete with the error and
// no Stop hook. It reports whether the turn failed.
func (a *agent) codexTurnStart(turn string) {
	a.codexLine(map[string]any{"type": "task_started", "turn_id": turn})
}

func (a *agent) codexTurnEnd(turn string) (failed bool) {
	limited := a.env["FAKEAGENT_CODEX_USAGE_LIMIT"] == "1"
	if tc := a.codexRecordedLine("token_count", limited); tc != nil {
		a.mu.Lock()
		n := int64(a.turn)
		a.mu.Unlock()
		if info, ok := tc["info"].(map[string]any); ok {
			info["total_token_usage"] = map[string]any{
				"input_tokens": 1000 * n, "cached_input_tokens": 600 * n, "cache_write_input_tokens": 100 * n,
				"output_tokens": 100 * n, "reasoning_output_tokens": 50 * n, "total_tokens": 1100 * n,
			}
		}
		a.codexLine(tc)
	}
	if limited {
		a.print("You’ve hit your usage limit.\r\n")
		a.codexLine(map[string]any{"type": "task_complete", "turn_id": turn, "last_agent_message": nil,
			"error": map[string]any{"message": "You’ve hit your usage limit.", "codex_error_info": "usage_limit_exceeded"}})
		return true
	}
	return false
}

// codexEscalation sends a Codex command that asks to leave the sandbox
// (sandbox_permissions: require_escalated) through PermissionRequest, and
// reports whether the tool may run.
func (a *agent) codexEscalation(ctx context.Context, name string, input, vals map[string]any) bool {
	if esc, _ := input["sandbox_permissions"].(string); a.p != Codex || esc != "require_escalated" {
		return true
	}
	pl, ok := a.payload("PermissionRequest", vals)
	if !ok {
		return true
	}
	if blocked, reason := a.fire(ctx, "PermissionRequest", name, pl); blocked {
		a.print(fmt.Sprintf("Tool %s not approved: %s\r\n", name, reason))
		return false
	}
	return true
}

func (a *agent) codexTurnComplete(turn string) {
	a.codexLine(map[string]any{"type": "task_complete", "turn_id": turn, "last_agent_message": a.lastMsg, "error": nil})
}
