package providers

import (
	"fmt"
	"strings"
)

// claudeHookEvents are the events the Mestre binds in
// .claude/settings.local.json (docs/provedores/claude-code.md#hooks): the
// matcher * on the tool events, the generated timeout (30 s where the
// decision is made, 60 s in the others) and onFailure "block" where a failed
// hook must stop the tool.
var claudeHookEvents = []struct {
	name    string
	tool    bool
	timeout int
	block   bool
}{
	{"SessionStart", false, 60, false},
	{"UserPromptSubmit", false, 30, false},
	{"PreToolUse", true, 30, true},
	{"PermissionRequest", true, 30, true},
	{"PostToolUse", true, 60, false},
	{"PostToolUseFailure", true, 60, false},
	{"Stop", false, 60, false},
	{"StopFailure", false, 60, false},
	{"Notification", false, 60, false},
	{"PreCompact", false, 60, false},
	{"SessionEnd", false, 60, false},
}

// claudeHookMarker is what every command of the Mestre has, the one that
// tells the Mestre's handlers from the person's.
const claudeHookMarker = "hook --provider " + string(ClaudeCode) + " --event "

// claudeHookCommand is the line of a hook file
// (docs/sessao-zero/mesa.md#não-atrapalhar-quem-clona): it calls `mestre
// hook` only when MESTRE_AGENT_ID exists and the binary is there, and
// otherwise answers neutrally (nothing, exit 0) for Claude Code. Claude Code
// runs a command hook with bash on both systems (sh on Linux; on Windows Git
// Bash, which it requires for a hook without "shell": "powershell"), so the
// line is the same, with the Windows path in forward slashes. The binary is
// the stable path when given, which no variable overrides (a repository
// could set one in its settings); else the MESTRE_CLI of the character,
// pinned in the --settings file of the character (ClaudeHookEnv).
func claudeHookCommand(goos, binary, event string) (string, error) {
	tail := claudeHookMarker + event
	switch goos {
	case "linux", "windows":
	default:
		return "", fmt.Errorf("claude-code hook: system %q is not linux or windows", goos)
	}
	bad := "\"$`\\\n\r"
	if goos == "windows" {
		bad = "\"$`\n\r" // the backslashes become slashes
	}
	if strings.ContainsAny(binary, bad) {
		return "", fmt.Errorf("claude-code hook: binary path %q has characters the shell line cannot carry", binary)
	}
	prefix, bin := "", `"${MESTRE_CLI:-mestre}"`
	switch {
	case binary != "":
		bin = `"` + strings.ReplaceAll(binary, `\`, "/") + `"` // no backslash on Linux (above)
	case goos == "windows": // MESTRE_CLI has backslashes, which command -v does not read as a path
		prefix, bin = `m="${MESTRE_CLI:-mestre}"; m="${m//\\//}"; `, `"$m"`
	}
	return prefix + `[ -n "$MESTRE_AGENT_ID" ] && command -v ` + bin + ` >/dev/null 2>&1 && exec ` + bin + ` ` + tail + `; exit 0`, nil
}

// ClaudeHookEnv is the env key of the --settings file of a character: the
// variables the hook line and `mestre hook` read (MESTRE_AGENT_ID, and
// MESTRE_CLI and MESTRE_ENDPOINT when given, as KEY=VALUE), pinned there.
// The --settings file is above the settings of the repository, whose env
// could otherwise empty MESTRE_AGENT_ID (the line answers neutrally) or
// point the CLI or the endpoint elsewhere.
func ClaudeHookEnv(agentID string, hookEnv []string) map[string]any {
	env := map[string]any{"MESTRE_AGENT_ID": agentID}
	for _, kv := range hookEnv {
		k, v, ok := strings.Cut(kv, "=")
		if ok && (k == "MESTRE_CLI" || k == "MESTRE_ENDPOINT") && v != "" {
			env[k] = v
		}
	}
	return env
}

func isMestreCommand(v any) bool {
	s, _ := v.(string)
	return strings.Contains(s, claudeHookMarker)
}

// HookFiles: the hooks of the Mestre, in the hooks key of
// .claude/settings.local.json of the worktree, next to the person's. With the
// fallback of the statusLine switched on, the statusLine too
// (docs/provedores/claude-code.md#verificar-na-implementação).
func (c *claudeCode) HookFiles(req HookFilesRequest) ([]ManagedFile, error) {
	if !absolute(req.Root) {
		return nil, fmt.Errorf("claude-code hook files: the worktree %q is not absolute", req.Root)
	}
	commands := make(map[string]string, len(claudeHookEvents))
	for _, e := range claudeHookEvents {
		cmd, err := claudeHookCommand(machineOS(req.OS), req.Binary, e.name)
		if err != nil {
			return nil, err
		}
		commands[e.name] = cmd
	}
	statusLine := ""
	if req.Fallbacks.ClaudeStatusLineInLocal {
		var err error
		if statusLine, err = claudeHookCommand(machineOS(req.OS), req.Binary, "StatusLine"); err != nil {
			return nil, err
		}
	}
	path := join(req.Root, ".claude", "settings.local.json")
	return []ManagedFile{{Path: path, Merge: func(cur []byte) ([]byte, error) {
		return mergeClaudeHooks(cur, path, commands, statusLine)
	}}}, nil
}

// mergeClaudeHooks takes the Mestre's handlers out of every event and puts
// the current ones at the end of each list, after the person's. The rest of
// the file is kept; a hooks key of another shape is an error.
func mergeClaudeHooks(cur []byte, path string, commands map[string]string, statusLine string) ([]byte, error) {
	obj, err := parseObject(cur, path)
	if err != nil {
		return nil, err
	}
	hooks := map[string]any{}
	if h, ok := obj["hooks"]; ok {
		if hooks, ok = h.(map[string]any); !ok {
			return nil, fmt.Errorf("%w: %s: hooks is not an object", ErrNativeFile, path)
		}
	}
	for event, v := range hooks { // also what an older version bound
		groups, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("%w: %s: hooks.%s is not a list", ErrNativeFile, path, event)
		}
		hooks[event] = stripMestre(groups)
	}
	for _, e := range claudeHookEvents {
		groups, _ := hooks[e.name].([]any)
		handler := map[string]any{"type": "command", "command": commands[e.name], "timeout": e.timeout}
		if e.block {
			handler["onFailure"] = "block"
		}
		group := map[string]any{"hooks": []any{handler}}
		if e.tool {
			group["matcher"] = "*"
		}
		hooks[e.name] = append(groups, group)
	}
	for event, v := range hooks { // an event left with nothing is not kept
		if g, _ := v.([]any); len(g) == 0 {
			delete(hooks, event)
		}
	}
	obj["hooks"] = hooks
	setClaudeStatusLine(obj, statusLine)
	return marshalObject(obj)
}

// stripMestre removes the handlers of the Mestre from the groups of an event;
// a group that only had those goes too, the person's groups stay as they are.
func stripMestre(groups []any) []any {
	out := make([]any, 0, len(groups))
	for _, g := range groups {
		group, ok := g.(map[string]any)
		list, isList := group["hooks"].([]any)
		if !ok || !isList {
			out = append(out, g)
			continue
		}
		kept := make([]any, 0, len(list))
		for _, h := range list {
			if hm, ok := h.(map[string]any); ok && isMestreCommand(hm["command"]) {
				continue
			}
			kept = append(kept, h)
		}
		switch {
		case len(kept) == len(list):
			out = append(out, g)
		case len(kept) > 0:
			group["hooks"] = kept
			out = append(out, group)
		}
	}
	return out
}

// setClaudeStatusLine sets the statusLine of the Mestre in a settings object,
// or takes it out when command is empty. A statusLine of the person is never
// touched.
func setClaudeStatusLine(obj map[string]any, command string) {
	if cur, has := obj["statusLine"]; has && !isMestreStatusLine(cur) {
		return
	}
	if command == "" {
		delete(obj, "statusLine")
		return
	}
	obj["statusLine"] = claudeStatusLine(command)
}

func claudeStatusLine(command string) map[string]any {
	return map[string]any{"type": "command", "command": command}
}

func isMestreStatusLine(v any) bool {
	m, _ := v.(map[string]any)
	return isMestreCommand(m["command"])
}
