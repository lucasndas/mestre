package fakeagent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/testkit"
	"github.com/lucasndas/mestre/pkg/clock"
)

// HookCall is one hook command to run, with the payload on stdin.
type HookCall struct {
	Event   string
	Command string
	Dir     string
	Env     []string
	Stdin   []byte
	Timeout time.Duration
}

// HookResult is what the command returned.
type HookResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// HookRunner runs hook commands: a function call in the fast suite, a real
// process (sh -c or cmd /c) in the full suite. An error means the command
// could not run or timed out.
type HookRunner interface {
	RunHook(ctx context.Context, call HookCall) (HookResult, error)
}

type handler struct {
	event   string
	matcher string
	command string
	timeout time.Duration
	block   bool   // Claude onFailure: "block"
	key     string // Codex trust key
	hash    string // Codex trust hash of this handler
	trusted bool
	dir     string
}

type hookEntry struct {
	Type      string  `json:"type"`
	Command   string  `json:"command"`
	Timeout   float64 `json:"timeout"`
	OnFailure string  `json:"onFailure"`
}

type hookGroup struct {
	Matcher string      `json:"matcher"`
	Hooks   []hookEntry `json:"hooks"`
}

// loadHooks reads the hook files of the provider.
func (a *agent) loadHooks() ([]handler, error) {
	switch a.p {
	case Claude:
		files := []string{
			filepath.Join(a.dir, ".claude", "settings.json"),
			filepath.Join(a.dir, ".claude", "settings.local.json"),
			filepath.Join(a.accountDir(), "settings.json"),
		}
		if a.settingsArg != "" {
			files = append(files, a.resolve(a.settingsArg))
		}
		var hs []handler
		seen := map[string]bool{}
		for _, f := range files {
			m, err := readJSONMap(f)
			if err != nil {
				return nil, err
			}
			for _, h := range groupedHandlers(m["hooks"], a.dir) {
				id := h.event + "\x00" + h.matcher + "\x00" + h.command
				if !seen[id] { // identical handler runs once
					seen[id] = true
					hs = append(hs, h)
				}
			}
		}
		return hs, nil
	case Codex:
		m, err := readJSONMap(filepath.Join(a.accountDir(), "hooks.json"))
		if err != nil {
			return nil, err
		}
		cfg, err := readCodexConfig(filepath.Join(a.accountDir(), "config.toml"))
		if err != nil {
			return nil, err
		}
		hs := groupedHandlers(m["hooks"], a.dir)
		for i := range hs {
			hs[i].trusted = cfg.hookTrust[hs[i].key] == hs[i].hash
		}
		return hs, nil
	default:
		path := filepath.Join(a.dir, ".agents", "hooks.json")
		m, err := readJSONMap(path)
		if err != nil {
			return nil, err
		}
		var hs []handler
		for _, v := range m { // top-level keys are free names
			group, _ := v.(map[string]any)
			if group == nil || group["enabled"] == false {
				continue
			}
			hs = append(hs, agyHandlers(group, filepath.Dir(path))...)
		}
		return hs, nil
	}
}

// groupedHandlers reads {"Event": [{"matcher", "hooks": [...]}]} (Claude
// and Codex format).
func groupedHandlers(v any, dir string) []handler {
	b, _ := json.Marshal(v)
	var events map[string][]hookGroup
	if json.Unmarshal(b, &events) != nil {
		return nil
	}
	var hs []handler
	for ev, groups := range events {
		for gi, g := range groups {
			for hi, e := range g.Hooks {
				if e.Type != "" && e.Type != "command" {
					continue
				}
				canon, _ := json.Marshal(e)
				sum := sha256.Sum256(canon)
				hs = append(hs, handler{
					event: ev, matcher: g.Matcher, command: e.Command, dir: dir,
					timeout: seconds(e.Timeout, 60), block: e.OnFailure == "block",
					key: fmt.Sprintf("user:%s:%d:%d", snake(ev), gi, hi),
					// The fake hashes the handler JSON; the real Codex
					// algorithm is checked by the conformance test (M3).
					hash:    "sha256:" + hex.EncodeToString(sum[:]),
					trusted: true, // Codex overrides with its hook trust
				})
			}
		}
	}
	return hs
}

// agyHandlers reads one Antigravity group: PreToolUse/PostToolUse are
// {matcher, hooks} groups, the other events plain handler lists.
func agyHandlers(group map[string]any, dir string) []handler {
	var hs []handler
	for ev, v := range group {
		b, _ := json.Marshal(v)
		switch ev {
		case "PreToolUse", "PostToolUse":
			var gs []hookGroup
			if json.Unmarshal(b, &gs) == nil {
				for _, g := range gs {
					for _, e := range g.Hooks {
						hs = append(hs, handler{event: ev, matcher: g.Matcher, command: e.Command, timeout: seconds(e.Timeout, 30), dir: dir, trusted: true})
					}
				}
			}
		case "PreInvocation", "PostInvocation", "Stop":
			var es []hookEntry
			if json.Unmarshal(b, &es) == nil {
				for _, e := range es {
					hs = append(hs, handler{event: ev, command: e.Command, timeout: seconds(e.Timeout, 30), dir: dir, trusted: true})
				}
			}
		}
	}
	return hs
}

func seconds(v, def float64) time.Duration {
	if v <= 0 {
		v = def
	}
	return time.Duration(v * float64(time.Second))
}

var upperRe = regexp.MustCompile(`([a-z0-9])([A-Z])`)

func snake(s string) string { return strings.ToLower(upperRe.ReplaceAllString(s, "${1}_${2}")) }

func (h handler) matches(tool string) bool {
	if h.matcher == "" || h.matcher == "*" || tool == "" {
		return true
	}
	re, err := regexp.Compile("^(?:" + h.matcher + ")$")
	return err == nil && re.MatchString(tool)
}

// fire runs the handlers of event and reports whether the provider would
// block the action, with the reason.
func (a *agent) fire(ctx context.Context, event, tool string, payload []byte) (bool, string) {
	blocked, reason := false, ""
	for _, h := range a.hooks {
		if h.event != event || !h.matches(tool) || !h.trusted {
			continue
		}
		hctx, cancel := clock.WithTimeout(ctx, a.clk, h.timeout)
		res, err := a.cfg.Hooks.RunHook(hctx, HookCall{
			Event: event, Command: h.command, Dir: h.dir, Env: a.cfg.Env, Stdin: payload, Timeout: h.timeout,
		})
		if err == nil && hctx.Err() != nil {
			err = context.Cause(hctx)
		}
		cancel()
		a.mu.Lock()
		a.hookLog = append(a.hookLog, event)
		a.mu.Unlock()
		if event != "PreToolUse" {
			continue
		}
		if b, why := a.decide(h, res, err); b {
			blocked, reason = true, why
		}
	}
	return blocked, reason
}

// decide applies the provider semantics to one PreToolUse answer
// (docs/provedores/adaptador.md and the provider documents).
func (a *agent) decide(h handler, res HookResult, err error) (bool, string) {
	out := bytes.TrimSpace(res.Stdout)
	var obj map[string]any
	isJSON := json.Unmarshal(out, &obj) == nil
	switch a.p {
	case Antigravity: // fail closed: only an explicit allow lets the tool run
		switch {
		case err != nil:
			return true, "hook failed: " + err.Error()
		case res.ExitCode != 0:
			return true, fmt.Sprintf("hook exited with %d", res.ExitCode)
		case !isJSON:
			return true, "hook output is not JSON"
		case obj["decision"] != "allow":
			return true, fmt.Sprintf("hook decision %v", obj["decision"])
		}
		return false, ""
	default: // Claude and Codex: exit 2 or an explicit deny blocks, failure lets it run
		if err != nil || (res.ExitCode != 0 && res.ExitCode != 2) {
			if h.block {
				return true, "hook failed and onFailure is block"
			}
			return false, ""
		}
		if res.ExitCode == 2 {
			return true, strings.TrimSpace(string(res.Stderr))
		}
		if hso, ok := obj["hookSpecificOutput"].(map[string]any); ok && hso["permissionDecision"] == "deny" {
			r, _ := hso["permissionDecisionReason"].(string)
			return true, r
		}
		return false, ""
	}
}

// untrustedHooks lists the Codex handlers that need review.
func (a *agent) untrustedHooks() []handler {
	var out []handler
	for _, h := range a.hooks {
		if !h.trusted {
			out = append(out, h)
		}
	}
	return out
}

func (a *agent) trustAllHooks() error {
	var b strings.Builder
	for i := range a.hooks {
		if !a.hooks[i].trusted {
			fmt.Fprintf(&b, "[hooks.state.%q]\ntrusted_hash = %q\n", a.hooks[i].key, a.hooks[i].hash)
			a.hooks[i].trusted = true
		}
	}
	return appendCodexConfig(filepath.Join(a.accountDir(), "config.toml"), b.String())
}

// payload fills the recorded payload of event with the live values. Only
// keys present in the recording are set, so the format stays the recorded
// one.
func (a *agent) payload(event string, vals map[string]any) ([]byte, bool) {
	body, ok := a.recording(event)
	if !ok {
		return nil, false
	}
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return nil, false
	}
	for k, v := range vals {
		if _, ok := m[k]; ok {
			m[k] = v
		}
	}
	out, err := json.Marshal(m)
	return out, err == nil
}

func (a *agent) recording(event string) (json.RawMessage, bool) {
	r, err := testkit.LoadRecording(a.recs, profiles[a.p].recordings, a.cfg.RecordingVersion, event)
	if err != nil {
		return nil, false // no recording: the provider has no such event
	}
	return r.Body, true
}

func (a *agent) resolve(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(a.dir, p)
}
