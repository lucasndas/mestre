package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// AntigravityAgentPath is the agent file of a character
// (docs/provedores/antigravity.md#camada-do-mestre).
func AntigravityAgentPath(worktree, agentID string) string {
	return join(worktree, ".agents", "agents", "mestre-"+agentID+".md")
}

// AntigravitySettingsPath is the settings.json of an Antigravity account
// profile, under its HOME.
func AntigravitySettingsPath(profileHome string) string {
	return join(profileHome, ".gemini", "antigravity-cli", "settings.json")
}

// Native translates the policy for Antigravity: --sandbox with no internet,
// on Windows only (AntigravityNoNativeNetwork); the tools field of the agent file with only the native tools the rules do
// not deny whole; and, in the settings.json of the profile, which every
// character of the profile shares, only the fixed rules in
// permissions.deny (action(target) with a wildcard) and toolPermission in
// request-review. There is no rule per path.
func (a *antigravity) Native(p NativePolicy) (NativeControls, error) {
	if err := p.check(); err != nil {
		return NativeControls{}, err
	}
	if p.ProfileHome == "" || !absolute(p.ProfileHome) {
		return NativeControls{}, fmt.Errorf("native controls of %s: antigravity needs the HOME of the account profile", p.AgentID)
	}
	var args []string
	if p.Offline && !AntigravityNoNativeNetwork(p) {
		args = append(args, "--sandbox")
	}
	tools := AntigravityTools(p)
	agent := AntigravityAgentPath(p.Worktree, p.AgentID)
	settings := AntigravitySettingsPath(p.ProfileHome)
	var ours []string
	for _, cmd := range p.FixedDenyCommands {
		ours = append(ours, "command("+starless(cmd)+")")
	}
	return NativeControls{Args: args, Files: []NativeFile{
		{Path: agent, Merge: func(cur []byte) ([]byte, error) { return agentTools(cur, p.AgentID, tools) }},
		{Path: settings, Merge: func(cur []byte) ([]byte, error) {
			return profileSettings(cur, settings, ours, !p.Fallbacks.AntigravityNoDeny)
		}},
	}}, nil
}

// AntigravityNoNativeNetwork reports whether an Antigravity character of
// this policy runs without the native network barrier its class asks for: no
// internet, and no --sandbox. On Linux the --sandbox of agy 1.3.5 needs
// unprivileged user namespaces, which Ubuntu restricts for mestre-agente:
// it runs no command at all (conformance test, 2026-10-14). By decision of
// the Owner, as with Codex on Linux, the network stays on and the internet
// is blocked only by the rules of the dispatcher, which fail closed in agy.
// On Windows the --sandbox (AppContainer) stays, unless the conformance
// test turns it off (AntigravityNoSandbox). The panel shows it on the sheet
// and the class check warns (docs/classes/reforco-nativo.md).
func AntigravityNoNativeNetwork(p NativePolicy) bool {
	return p.Offline && (machineOS(p.OS) != "windows" || p.Fallbacks.AntigravityNoSandbox)
}

// agyNotInRegistry are names the hooks may carry that the tools field of an
// agent file must not list: agy 1.3.5 refuses to start any turn with "tool
// not found in registry" (conformance test, 2026-10-14).
var agyNotInRegistry = []string{"list_permissions"}

// AntigravityTools are the native tools of Antigravity the policy does not
// deny whole, sorted: no write tools without write, no web tools without
// internet, no search without a URL when the domains are listed, and no
// subagent or other tool the rules deny every call of.
func AntigravityTools(p NativePolicy) []string {
	var out []string
	for name, kind := range antigravityTools.names {
		if slices.Contains(agyNotInRegistry, name) {
			continue
		}
		denied := false
		switch kind {
		case KindWrite, KindEdit:
			denied = p.NoWrite
		case KindWeb:
			denied = p.Offline || (name == "search_web" && !p.AnyDomain)
		case KindSubagent, KindOther:
			denied = p.ToolDenied != nil && p.ToolDenied(name)
		}
		if !denied {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// agentTools writes the frontmatter of the agent file from scratch: the one
// the Mestre generates (agentFront) and the tools field. The file is in the
// worktree, which the agent writes, so no key it planted there (mcpServers,
// permissionMode, another model, a wider tools list) is ever kept; only the
// body, which is the Mestre layer's, stays for the layer to rewrite.
func agentTools(cur []byte, agentID string, tools []string) ([]byte, error) {
	_, body, ok := frontmatter(strings.ReplaceAll(string(cur), "\r\n", "\n"))
	if !ok {
		body = ""
	}
	front := append(agentFront(agentID), toolsLine(tools))
	return []byte("---\n" + strings.Join(front, "\n") + "\n---\n" + body), nil
}

// toolsLine is the tools field as the native controls write it.
func toolsLine(tools []string) string {
	quoted := make([]string, len(tools))
	for i, t := range tools {
		quoted[i] = strconv.Quote(t)
	}
	return "tools: [" + strings.Join(quoted, ", ") + "]"
}

// keptToolsLine is the tools field of an old frontmatter as the layer keeps
// it: the line the native controls wrote (one flow list of known native
// tools), or, for anything else (a block list, an unknown name, a line the
// agent edited), an empty list, which lets no tool run until the native
// controls write the field again. ok is false when there was no field.
func keptToolsLine(front []string) (line string, ok bool) {
	for _, l := range front {
		if !strings.HasPrefix(l, "tools:") {
			continue
		}
		inner, flow := strings.CutPrefix(strings.TrimSpace(strings.TrimPrefix(l, "tools:")), "[")
		inner, closed := strings.CutSuffix(inner, "]")
		var names []string
		good := flow && closed
		for item := range strings.SplitSeq(inner, ",") {
			if !good || strings.TrimSpace(item) == "" {
				continue
			}
			name, err := strconv.Unquote(strings.TrimSpace(item))
			_, known := antigravityTools.names[name]
			good = err == nil && known
			names = append(names, name)
		}
		if !good || toolsLine(names) != l {
			return toolsLine(nil), true
		}
		return l, true
	}
	return "", false
}

// frontmatter splits a "---" frontmatter from the body.
func frontmatter(text string) (front []string, body string, ok bool) {
	rest, found := strings.CutPrefix(text, "---\n")
	if !found {
		return nil, "", false
	}
	end := strings.Index(rest, "\n---\n")
	switch {
	case end >= 0:
		body = rest[end+len("\n---\n"):]
	case strings.HasSuffix(rest, "\n---"):
		end = len(rest) - len("\n---")
	default:
		return nil, "", false
	}
	if end == 0 {
		return nil, body, true
	}
	return strings.Split(rest[:end], "\n"), body, true
}

// agyKeptSettings are the top-level keys of the profile's settings.json the
// person or agy may keep: the trust of the worktrees (PreTrust) and what only
// changes how the CLI looks. Every other key is dropped at each write: the
// profile folder is written by the account of the agents, and a key planted
// there (permissions.allow, allowNonWorkspaceAccess, enableTerminalSandbox,
// agentMode, artifactReviewPolicy, autoExecPolicy, hooks, mcpServers,
// statusLine) would widen what every character of the profile may do
// (docs/classes/reforco-nativo.md#tradução-por-provedor).
var agyKeptSettings = []string{"trustedWorkspaces", "theme", "vimInsertFirst", "pickerGrouping", "editorMode", "model", "releaseChannel", "telemetryEnabled"}

// profileSettings writes the profile's settings.json from a whitelist:
// agyKeptSettings as they are, of permissions only deny and ask (they only
// restrict), the fixed rules of the Mestre in permissions.deny (or none, with
// the fallback) and toolPermission in request-review.
func profileSettings(cur []byte, path string, ours []string, deny bool) ([]byte, error) {
	old := map[string]any{}
	if len(bytes.TrimSpace(cur)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(cur))
		dec.UseNumber()
		if err := dec.Decode(&old); err != nil || old == nil {
			return nil, fmt.Errorf("%w: %s is not a JSON object: %v", ErrNativeFile, path, err)
		}
	}
	obj := map[string]any{}
	for _, k := range agyKeptSettings {
		if v, ok := old[k]; ok {
			obj[k] = v
		}
	}
	perms := map[string]any{}
	if v, ok := old["permissions"]; ok {
		m, isObj := v.(map[string]any)
		if !isObj {
			return nil, fmt.Errorf("%w: %s: permissions is not an object", ErrNativeFile, path)
		}
		perms = m
	}
	texts := func(key string) ([]string, error) {
		v, ok := perms[key]
		if !ok {
			return nil, nil
		}
		arr, isArr := v.([]any)
		if !isArr {
			return nil, fmt.Errorf("%w: %s: permissions.%s is not a list", ErrNativeFile, path, key)
		}
		var out []string
		for _, e := range arr {
			s, isStr := e.(string)
			if !isStr {
				return nil, fmt.Errorf("%w: %s: permissions.%s has a non-text entry", ErrNativeFile, path, key)
			}
			out = append(out, s)
		}
		return out, nil
	}
	theirs, err := texts("deny")
	if err != nil {
		return nil, err
	}
	list := []string{}
	for _, s := range theirs {
		if !slices.Contains(ours, s) {
			list = append(list, s)
		}
	}
	if deny {
		list = append(list, ours...)
	}
	kept := map[string]any{"deny": list}
	if ask, err := texts("ask"); err == nil && ask != nil {
		kept["ask"] = ask
	}
	obj["permissions"] = kept
	obj["toolPermission"] = "request-review"
	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
