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
	"slices"
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

// NotImplementedError is a configuration the fake agent does not model: a
// hook event it never fires, a hook type it cannot run, a recording it does
// not have. It ends the agent instead of being skipped
// (docs/qualidade/dubles.md#regras-e-erros); MemLauncher also fails the test
// with it.
type NotImplementedError struct{ What string }

func (e *NotImplementedError) Error() string { return testkit.NotImplementedPrefix + e.What }

func notImplemented(format string, args ...any) error {
	return &NotImplementedError{What: fmt.Sprintf(format, args...)}
}

// loadHooks reads the hook files of the provider and refuses what the fake
// does not model.
func (a *agent) loadHooks() ([]handler, error) {
	hs, err := a.readHookFiles()
	if err != nil {
		return nil, err
	}
	pr := profiles[a.p]
	for _, h := range hs {
		if !slices.Contains(pr.events, h.event) {
			return nil, notImplemented("%s hook event %s (the fake fires %s)", a.p, h.event, strings.Join(pr.events, ", "))
		}
		if _, ok := a.recording(h.event); !ok {
			return nil, notImplemented("%s hook event %s has no recording (version %q)", a.p, h.event, a.cfg.RecordingVersion)
		}
	}
	return hs, nil
}

func (a *agent) readHookFiles() ([]handler, error) {
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
			layer, err := a.groupedHandlers(m["hooks"], f, a.dir, "")
			if err != nil {
				return nil, err
			}
			for _, h := range layer {
				id := h.event + "\x00" + h.matcher + "\x00" + h.command
				if !seen[id] { // identical handler runs once
					seen[id] = true
					hs = append(hs, h)
				}
			}
		}
		return hs, nil
	case Codex:
		cfg, err := readCodexConfig(filepath.Join(a.accountDir(), "config.toml"))
		if err != nil {
			return nil, err
		}
		// The layers add up: the profile's hooks.json and the project's own
		// .codex/hooks.json (the folder is trusted by now, so the project
		// layer counts).
		layers := []struct{ file, keyPrefix string }{{filepath.Join(a.accountDir(), "hooks.json"), "user"}}
		for _, root := range trustCandidates(a.dir) {
			f := filepath.Join(root, ".codex", "hooks.json")
			layers = append(layers, struct{ file, keyPrefix string }{f, "project:" + f})
		}
		var hs []handler
		for _, l := range layers {
			m, err := readJSONMap(l.file)
			if err != nil {
				return nil, err
			}
			layer, err := a.groupedHandlers(m["hooks"], l.file, a.dir, l.keyPrefix)
			if err != nil {
				return nil, err
			}
			for i := range layer {
				layer[i].trusted = cfg.hookTrust[layer[i].key] == layer[i].hash
			}
			hs = append(hs, layer...)
		}
		return hs, nil
	default:
		path := filepath.Join(a.dir, ".agents", "hooks.json")
		m, err := readJSONMap(path)
		if err != nil {
			return nil, err
		}
		var hs []handler
		for name, v := range m { // top-level keys are free names
			group, ok := v.(map[string]any)
			if !ok {
				return nil, notImplemented("antigravity hooks.json: %q is not a hook group", name)
			}
			if group["enabled"] == false {
				continue
			}
			layer, err := agyHandlers(group, filepath.Dir(path))
			if err != nil {
				return nil, fmt.Errorf("%s: %q: %w", path, name, err)
			}
			hs = append(hs, layer...)
		}
		return hs, nil
	}
}

// groupedHandlers reads {"Event": [{"matcher", "hooks": [...]}]} (Claude
// and Codex format). keyPrefix names the Codex layer in the trust key. Only
// Claude honours onFailure: Codex fails open with no option
// (docs/provedores/codex.md#hooks).
func (a *agent) groupedHandlers(v any, file, dir, keyPrefix string) ([]handler, error) {
	if v == nil {
		return nil, nil
	}
	b, _ := json.Marshal(v)
	var events map[string][]hookGroup
	if err := json.Unmarshal(b, &events); err != nil {
		return nil, fmt.Errorf("%s: hooks: %w", file, err)
	}
	if keyPrefix == "" {
		keyPrefix = "user"
	}
	var trust map[string][2]string // Codex: the key and the hash Codex gives each handler
	if a.p == Codex {
		whole, _ := json.Marshal(map[string]any{"hooks": v})
		var err error
		if trust, err = codexTrust(file, whole); err != nil {
			return nil, err
		}
	}
	var hs []handler
	for ev, groups := range events {
		for gi, g := range groups {
			for hi, e := range g.Hooks {
				if e.Type != "" && e.Type != "command" {
					return nil, notImplemented("%s hook type %q in %s", a.p, e.Type, file)
				}
				canon, _ := json.Marshal(e)
				sum := sha256.Sum256(canon)
				h := handler{
					event: ev, matcher: g.Matcher, command: e.Command, dir: dir,
					timeout: seconds(e.Timeout, 60), block: a.p == Claude && e.OnFailure == "block",
					key:     fmt.Sprintf("%s:%s:%d:%d", keyPrefix, snake(ev), gi, hi),
					hash:    "sha256:" + hex.EncodeToString(sum[:]),
					trusted: true, // Codex overrides with its hook trust
				}
				if kh, ok := trust[fmt.Sprintf("%s\x00%d\x00%d", ev, gi, hi)]; ok {
					h.key, h.hash = kh[0], kh[1] // as Codex keys and hashes it (codex.go)
				}
				hs = append(hs, h)
			}
		}
	}
	return hs, nil
}

// agyHandlers reads one Antigravity group: PreToolUse/PostToolUse are
// {matcher, hooks} groups, the other events plain handler lists. A key that
// is neither "enabled" nor an event of the provider is an error.
func agyHandlers(group map[string]any, dir string) ([]handler, error) {
	var hs []handler
	for ev, v := range group {
		b, _ := json.Marshal(v)
		switch ev {
		case "enabled":
		case "PreToolUse", "PostToolUse":
			var gs []hookGroup
			if err := json.Unmarshal(b, &gs); err != nil {
				return nil, fmt.Errorf("%s: %w", ev, err)
			}
			for _, g := range gs {
				for _, e := range g.Hooks {
					if e.Type != "" && e.Type != "command" {
						return nil, notImplemented("antigravity hook type %q", e.Type)
					}
					hs = append(hs, handler{event: ev, matcher: g.Matcher, command: e.Command, timeout: seconds(e.Timeout, 30), dir: dir, trusted: true})
				}
			}
		case "PreInvocation", "PostInvocation", "Stop":
			var es []hookEntry
			if err := json.Unmarshal(b, &es); err != nil {
				return nil, fmt.Errorf("%s: %w", ev, err)
			}
			for _, e := range es {
				if e.Type != "" && e.Type != "command" {
					return nil, notImplemented("antigravity hook type %q", e.Type)
				}
				hs = append(hs, handler{event: ev, command: e.Command, timeout: seconds(e.Timeout, 30), dir: dir, trusted: true})
			}
		default:
			return nil, notImplemented("antigravity hook event %s", ev)
		}
	}
	return hs, nil
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
		switch event {
		case "PreToolUse":
			if b, why := a.decide(h, res, err); b {
				blocked, reason = true, why
			}
		case "PermissionRequest":
			if b, why := decidePermission(res, err); b {
				blocked, reason = true, why
			}
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
			if h.block && a.p == Claude { // Codex fails open, with no option
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

// decidePermission is the answer to a PermissionRequest: only an explicit
// decision.behavior "allow" lets the command leave the sandbox; anything
// else is denied (the fake has no native approval screen, and the adapter
// answers that screen with deny when no decision was recorded).
func decidePermission(res HookResult, err error) (bool, string) {
	if err != nil || res.ExitCode != 0 {
		return true, strings.TrimSpace(string(res.Stderr))
	}
	var out struct {
		HookSpecificOutput struct {
			Decision struct {
				Behavior string `json:"behavior"`
				Message  string `json:"message"`
			} `json:"decision"`
		} `json:"hookSpecificOutput"`
	}
	if json.Unmarshal(bytes.TrimSpace(res.Stdout), &out) != nil || out.HookSpecificOutput.Decision.Behavior != "allow" {
		return true, out.HookSpecificOutput.Decision.Message
	}
	return false, ""
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
