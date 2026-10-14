package providers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// The Mestre hooks of Codex live in the user layer of the account profile,
// $CODEX_HOME/hooks.json (docs/provedores/codex.md#hooks): the profile is the
// Mestre's alone, so the hooks touch no personal use of Codex and no file of
// the worktree. The file has fixed content (it only calls `mestre hook` by
// the stable path of the binary), so the trust given once in /hooks, which
// Codex ties to the hash of each hook, holds for every worktree.

// codexHook is one event the Mestre binds, with the time limit the hook
// gets (seconds).
type codexHook struct {
	event   string
	label   string // the snake_case name in the trust key
	timeout int
	matcher bool // Codex ignores the matcher of UserPromptSubmit, Stop and Interrupt
}

var codexHooks = []codexHook{
	{"SessionStart", "session_start", 30, true},
	{"UserPromptSubmit", "user_prompt_submit", 30, false},
	{"PreToolUse", "pre_tool_use", 30, true},
	{"PermissionRequest", "permission_request", 30, true},
	{"PostToolUse", "post_tool_use", 60, true},
	{"Stop", "stop", 60, false},
	{"PreCompact", "pre_compact", 60, true},
	{"SessionEnd", "session_end", 3, true}, // 3 s is the most Codex allows
	{"Interrupt", "interrupt", 3, false},
}

// codexHookFile is the name of the hooks file in the profile.
const codexHookFile = "hooks.json"

type codexHandler struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

type codexGroup struct {
	Matcher string         `json:"matcher,omitempty"`
	Hooks   []codexHandler `json:"hooks"`
}

// HookFiles generates $CODEX_HOME/hooks.json, the same bytes for every
// profile of a machine. The whole file is the managed part: Codex keys the
// trust by the position of each hook in the file, so nothing else may sit
// in it. With the fallback of the trust item, the config.toml of the
// profile also gets the trust entry (trusted_hash) of each hook.
func (c *codex) HookFiles(req HookFilesRequest) ([]ManagedFile, error) {
	if req.Root == "" || !absolute(req.Root) {
		return nil, fmt.Errorf("codex hooks: the profile folder %q is not absolute", req.Root)
	}
	content, err := codexHooksJSON(machineOS(req.OS), req.Binary)
	if err != nil {
		return nil, err
	}
	hooksPath := filepath.Join(req.Root, codexHookFile)
	files := []ManagedFile{{Path: hooksPath, Merge: func([]byte) ([]byte, error) { return content, nil }}}
	if req.Fallbacks.CodexHookTrust {
		files = append(files, ManagedFile{Path: codexConfig(req.Root), Merge: func(cur []byte) ([]byte, error) {
			return codexTrustHooks(cur, hooksPath, machineOS(req.OS), req.Binary)
		}})
	}
	return files, nil
}

// codexHooksJSON is the content of hooks.json.
func codexHooksJSON(goos, bin string) ([]byte, error) {
	hooks := map[string][]codexGroup{}
	for _, h := range codexHooks {
		cmd, err := codexHookCommand(goos, bin, h.event)
		if err != nil {
			return nil, err
		}
		g := codexGroup{Hooks: []codexHandler{{Type: "command", Command: cmd, Timeout: h.timeout}}}
		if h.matcher {
			g.Matcher = "*"
		}
		hooks[h.event] = []codexGroup{g}
	}
	b, err := json.MarshalIndent(map[string]any{"hooks": hooks}, "", "  ") // map keys come out sorted
	if err != nil {
		return nil, fmt.Errorf("codex hooks: %w", err)
	}
	return append(b, '\n'), nil
}

// codexHookCommand is the line of one hook: the stable path of `mestre` and
// the arguments. Codex runs it with the shell it detects for the user
// (codex-rs, commit 2f05c0a: core/src/session/mod.rs, build_hooks_config):
// on Linux `$SHELL -c` (sh, bash or zsh), where the line is the path and
// the arguments, between double quotes when the path has a blank; on
// Windows PowerShell (`-NoProfile -Command`), where a quoted path is a
// string, not a command: there the line calls it with & and ends with the
// exit code of the call, which PowerShell 5.1 would otherwise turn into 1
// (and 2, the block, would be lost: Codex fails open). A path with a
// character any of those shells would read is refused.
func codexHookCommand(goos, bin, event string) (string, error) {
	if bin == "" || !absolute(bin) { // the content is fixed: no variable of the shell
		return "", fmt.Errorf("codex hooks: the mestre binary %q is not an absolute path", bin)
	}
	if strings.ContainsAny(bin, "\"'`$%!&|;<>()^*?[]{}\n\r\t") {
		return "", fmt.Errorf("codex hooks: the mestre binary %q has a character the shell would read", bin)
	}
	// PowerShell reads the typographic quotes as quotes (one would close
	// the "…" of the path) and the dashes as the - of a parameter.
	if strings.ContainsAny(bin, powerShellQuotesAndDashes) {
		return "", fmt.Errorf("codex hooks: the mestre binary %q has a typographic quote or dash, which PowerShell reads", bin)
	}
	args := " hook --provider codex --event " + event
	if goos == "windows" {
		return `& "` + bin + `"` + args + "; exit $LASTEXITCODE", nil
	}
	if strings.Contains(bin, " ") {
		bin = `"` + bin + `"`
	}
	return bin + args, nil
}

// powerShellQuotesAndDashes are the characters PowerShell takes as ' and "
// (‘ ’ ‚ ‛ “ ” „) and as the - of a parameter (– — ―).
const powerShellQuotesAndDashes = "‘’‚‛“”„–—―"

// codexTrustHooks sets [hooks.state."<file>:<event>:0:0"] trusted_hash for
// each Mestre hook, keeping the rest of config.toml.
func codexTrustHooks(cur []byte, hooksPath, goos, bin string) ([]byte, error) {
	out := cur
	for _, h := range codexHooks {
		hash, err := codexHookHash(h, goos, bin)
		if err != nil {
			return nil, err
		}
		v, _ := tomlString(hash) // hex is always UTF-8
		if out, err = setTOMLKey(out, []string{"hooks", "state", codexHookKey(hooksPath, h)}, "trusted_hash", v); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// codexHookKey is the key Codex gives a hook of the user layer: the path of
// the file, the event and the positions of the group and of the handler.
func codexHookKey(hooksPath string, h codexHook) string {
	return hooksPath + ":" + h.label + ":0:0"
}

// codexHookHash is the hash Codex ties the trust to (hooks/src/engine/
// discovery.rs, hook_hash): SHA-256 of the compact JSON, keys sorted, of
// the event label, the effective matcher and the normalized handler (async
// false, the timeout). The conformance test checks it at each version.
func codexHookHash(h codexHook, goos, bin string) (string, error) {
	cmd, err := codexHookCommand(goos, bin, h.event)
	if err != nil {
		return "", err
	}
	// encoding/json sorts map keys and writes no blanks: the canonical form.
	handler := map[string]any{"async": false, "command": cmd, "timeout": h.timeout, "type": "command"}
	identity := map[string]any{"event_name": h.label, "hooks": []any{handler}}
	if h.matcher {
		identity["matcher"] = "*"
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false) // serde_json does not escape <, > and &
	if err := enc.Encode(identity); err != nil {
		return "", errors.New("codex hooks: hash input does not encode")
	}
	sum := sha256.Sum256([]byte(strings.TrimSuffix(b.String(), "\n")))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
