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

// Native translates the policy for Antigravity: --sandbox with no internet;
// the tools field of the agent file with only the native tools the rules do
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
	if p.Offline {
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

// AntigravityTools are the native tools of Antigravity the policy does not
// deny whole, sorted: no write tools without write, no web tools without
// internet, no search without a URL when the domains are listed, and no
// subagent or other tool the rules deny every call of.
func AntigravityTools(p NativePolicy) []string {
	var out []string
	for name, kind := range antigravityTools.names {
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

// agentTools sets the tools field in the frontmatter of the agent file and
// keeps the rest; a file that does not exist yet gets the frontmatter the
// Mestre layer then completes.
func agentTools(cur []byte, agentID string, tools []string) ([]byte, error) {
	quoted := make([]string, len(tools))
	for i, t := range tools {
		quoted[i] = strconv.Quote(t)
	}
	line := "tools: [" + strings.Join(quoted, ", ") + "]"
	text := strings.ReplaceAll(string(cur), "\r\n", "\n")
	front, body, ok := frontmatter(text)
	if !ok {
		front, body = agentFront(agentID), text
	}
	var out []string
	set := false
	for i := 0; i < len(front); i++ {
		l := front[i]
		if strings.HasPrefix(l, "tools:") {
			// A block list goes on the next lines; drop them with the key.
			for i+1 < len(front) && (strings.HasPrefix(front[i+1], " ") || strings.HasPrefix(front[i+1], "-")) {
				i++
			}
			if !set {
				out = append(out, line)
				set = true
			}
			continue
		}
		out = append(out, l)
	}
	if !set {
		out = append(out, line)
	}
	return []byte("---\n" + strings.Join(out, "\n") + "\n---\n" + body), nil
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

// profileSettings puts the fixed rules of the Mestre in permissions.deny
// (or takes them out, with the fallback) and toolPermission in
// request-review, keeping everything else of the profile's settings.json.
func profileSettings(cur []byte, path string, ours []string, deny bool) ([]byte, error) {
	obj := map[string]any{}
	if len(bytes.TrimSpace(cur)) > 0 {
		if err := json.Unmarshal(cur, &obj); err != nil || obj == nil {
			return nil, fmt.Errorf("%w: %s is not a JSON object: %v", ErrNativeFile, path, err)
		}
	}
	perms := map[string]any{}
	if v, ok := obj["permissions"]; ok {
		m, isObj := v.(map[string]any)
		if !isObj {
			return nil, fmt.Errorf("%w: %s: permissions is not an object", ErrNativeFile, path)
		}
		perms = m
	}
	var list []string
	if v, ok := perms["deny"]; ok {
		arr, isArr := v.([]any)
		if !isArr {
			return nil, fmt.Errorf("%w: %s: permissions.deny is not a list", ErrNativeFile, path)
		}
		for _, e := range arr {
			s, isStr := e.(string)
			if !isStr {
				return nil, fmt.Errorf("%w: %s: permissions.deny has a non-text entry", ErrNativeFile, path)
			}
			if !slices.Contains(ours, s) {
				list = append(list, s)
			}
		}
	}
	if deny {
		list = append(list, ours...)
	}
	if list == nil {
		list = []string{}
	}
	perms["deny"] = list
	obj["permissions"] = perms
	obj["toolPermission"] = "request-review"
	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
