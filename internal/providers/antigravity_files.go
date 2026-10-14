package providers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AntigravityHooksPath is the hook file of a worktree
// (docs/provedores/antigravity.md#hooks).
func AntigravityHooksPath(worktree string) string {
	return join(worktree, ".agents", "hooks.json")
}

// hooksKey is the name of the hook group the Mestre owns in hooks.json; the
// other top-level keys are free names and are kept.
const hooksKey = "mestre"

// agyHooks are the five events of Antigravity, in the order of its
// document, with the timeout in seconds the Mestre generates: 30 for the
// ones that decide or give context, 60 for the ones that only inform.
// PreToolUse and PostToolUse are {matcher, hooks} groups; the others plain
// lists of handlers.
var agyHooks = []struct {
	event   string
	timeout int
	grouped bool
}{
	{"PreToolUse", 30, true},
	{"PostToolUse", 60, true},
	{"PreInvocation", 30, false},
	{"PostInvocation", 30, false},
	{"Stop", 60, false},
}

type agyHandler struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

type agyGroup struct {
	Matcher string       `json:"matcher"`
	Hooks   []agyHandler `json:"hooks"`
}

// HookFiles generates the `mestre` hook group of .agents/hooks.json: the
// five events, each calling `mestre hook --provider antigravity --event
// <event>`, merged into the file keeping the other hooks. The command tests
// whether the `mestre` binary exists at the stable path and, if it does not
// (a clone on a machine without the Mestre), prints the neutral answer
// instead of letting the missing command block the agent: Antigravity blocks
// on a command that fails.
func (a *antigravity) HookFiles(req HookFilesRequest) ([]ManagedFile, error) {
	if req.Root == "" || !absolute(req.Root) {
		return nil, fmt.Errorf("antigravity hook files: the worktree %q is not absolute", req.Root)
	}
	group, err := a.hookGroup(req.OS, req.Binary)
	if err != nil {
		return nil, err
	}
	path := AntigravityHooksPath(req.Root)
	return []ManagedFile{{Path: path, Merge: func(cur []byte) ([]byte, error) {
		return mergeKey(cur, path, hooksKey, group)
	}}}, nil
}

// hookGroup is the value of the `mestre` key.
func (a *antigravity) hookGroup(goos, binary string) (*jsonObject, error) {
	if binary == "" || !absolute(binary) {
		return nil, fmt.Errorf("antigravity hook files: the stable path of the mestre binary %q is not absolute (the command tests it, there is no MESTRE_CLI fallback)", binary)
	}
	g := &jsonObject{vals: map[string]json.RawMessage{}, dup: map[string]bool{}}
	if err := g.set("enabled", true); err != nil {
		return nil, err
	}
	for _, h := range agyHooks {
		cmd, err := a.hookCommand(goos, binary, h.event)
		if err != nil {
			return nil, err
		}
		handler := agyHandler{Type: "command", Command: cmd, Timeout: h.timeout}
		var v any = []agyHandler{handler}
		if h.grouped {
			v = []agyGroup{{Matcher: "*", Hooks: []agyHandler{handler}}}
		}
		if err := g.set(h.event, v); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// hookCommand is the command of one event, in the shell Antigravity uses on
// the system (`sh -c`, or `cmd /c` on Windows). The neutral answer is the one
// the dispatcher gives without a character (FormatResponse).
func (a *antigravity) hookCommand(goos, binary, event string) (string, error) {
	neutral := strings.TrimSpace(string(a.FormatResponse(event, Reply{Kind: ReplyNeutral}).Stdout))
	if strings.ContainsAny(binary, "\r\n\x00") {
		return "", fmt.Errorf("antigravity hook files: the path of the mestre binary has a control character")
	}
	args := " hook --provider antigravity --event " + event
	if goos == "windows" {
		// Inside quotes cmd takes everything literally but " and %.
		if strings.ContainsAny(binary, `"%`) {
			return "", fmt.Errorf("antigravity hook files: the path of the mestre binary %q has a character cmd cannot quote", binary)
		}
		return `if exist "` + binary + `" ("` + binary + `"` + args + `) else (echo ` + neutral + `)`, nil
	}
	q := "'" + strings.ReplaceAll(binary, "'", `'\''`) + "'"
	return "if [ -e " + q + " ]; then exec " + q + args + "; else printf '%s\\n' '" + neutral + "'; fi", nil
}

// mergeKey puts value under key in the JSON object of cur, keeping every
// other key and its order. A file that already has exactly that value (once)
// comes back untouched. An invalid file is an error and is never rewritten.
func mergeKey(cur []byte, path, key string, value *jsonObject) ([]byte, error) {
	obj, err := parseJSONObject(cur)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrNativeFile, path, err)
	}
	want, err := value.bytes()
	if err != nil {
		return nil, err
	}
	if have, ok := obj.get(key); ok && !obj.dup[key] && sameJSON(have, want) {
		return cur, nil
	}
	if err := obj.set(key, json.RawMessage(want)); err != nil {
		return nil, err
	}
	return obj.bytes()
}

// PreTrust adds the exact path of the worktree to `trustedWorkspaces` of the
// settings.json of the account profile, once, keeping every other key. The
// Mestre does not rely on the trust of a folder covering its subfolders, so
// the repository root is not added (docs/provedores/antigravity.md#confiança-de-pasta).
// The file is read by the account of the agents, so it is written readable by
// the group and the others. Idempotent; if agy rewrote the file meanwhile
// the edit is done again, up to three times.
func (a *antigravity) PreTrust(req PreTrustRequest) (Trust, error) {
	if req.Profile == "" || !absolute(req.Profile) {
		return Trust{}, fmt.Errorf("antigravity pre-trust: the HOME of the account profile %q is not absolute", req.Profile)
	}
	if req.Worktree == "" || !absolute(req.Worktree) {
		return Trust{}, fmt.Errorf("antigravity pre-trust: the worktree %q is not absolute", req.Worktree)
	}
	req.OS = machineOS(req.OS)
	wt := filepath.Clean(req.Worktree)
	path := AntigravitySettingsPath(req.Profile)
	trustMu.Lock()
	defer trustMu.Unlock()
	out := Trust{Path: path}
	for range trustAttempts {
		cur, before, err := readFileStat(path)
		if err != nil {
			return Trust{}, fmt.Errorf("antigravity pre-trust: %w", err)
		}
		next, changed, err := trustWorktree(cur, path, wt, req.OS)
		if err != nil {
			return Trust{}, err
		}
		if !changed {
			return out, nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return Trust{}, fmt.Errorf("antigravity pre-trust: %w", err)
		}
		if err := replaceFileWith(path, next, before, 0o044); err != nil {
			if errors.Is(err, errRewritten) {
				continue
			}
			return Trust{}, fmt.Errorf("antigravity pre-trust: %w", err)
		}
		out.Trusted, out.Changed = []string{wt}, true
		again, _, err := readFileStat(path)
		if err != nil {
			return Trust{}, fmt.Errorf("antigravity pre-trust: %w", err)
		}
		if _, still, err := trustWorktree(again, path, wt, req.OS); err == nil && !still {
			return out, nil
		}
	}
	return Trust{}, fmt.Errorf("antigravity pre-trust: %s was rewritten while the trust was being written, %d times", path, trustAttempts)
}

// trustWorktree is the new content of the settings.json with wt in
// trustedWorkspaces, and whether it differs from cur (a file that already has
// the path once comes back as it was, formatting included).
func trustWorktree(cur []byte, path, wt, goos string) ([]byte, bool, error) {
	obj, err := parseJSONObject(cur)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %s: %v", ErrNativeFile, path, err)
	}
	var list []string
	if raw, ok := obj.get("trustedWorkspaces"); ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, false, fmt.Errorf("%w: %s: trustedWorkspaces is not a list of paths", ErrNativeFile, path)
		}
	}
	present := false
	for _, x := range list {
		present = present || sameFolder(goos, x, wt)
	}
	if present && !obj.dup["trustedWorkspaces"] {
		return cur, false, nil
	}
	if !present {
		list = append(list, wt)
	}
	if list == nil {
		list = []string{}
	}
	if err := obj.set("trustedWorkspaces", list); err != nil {
		return nil, false, err
	}
	next, err := obj.bytes()
	return next, err == nil, err
}
