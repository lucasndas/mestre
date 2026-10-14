package providers

import (
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// ClaudeSettingsPath is the --settings file of a character
// (docs/provedores/claude-code.md#flags).
func ClaudeSettingsPath(worktree, agentID string) string {
	return join(worktree, ".mestre", "agents", agentID, "claude-settings.json")
}

// Native translates the policy into the --settings file of the character
// (permissions.allow and permissions.deny) and --disallowedTools. The path
// rules are absolute (//<worktree>/<glob>), so they do not depend on the
// folder the settings file is read from; on Windows the path takes the
// POSIX form Claude Code matches (C:\x → //c/x).
func (c *claudeCode) Native(p NativePolicy) (NativeControls, error) {
	if err := p.check(); err != nil {
		return NativeControls{}, err
	}
	path := func(tool, glob string) (string, bool) {
		abs := absolute(glob)
		switch {
		case abs:
			return tool + "(" + claudeAbs(glob) + ")", true
		case p.Fallbacks.ClaudePaths == ClaudePathsOff:
			return "", false
		case p.Fallbacks.ClaudePaths == ClaudePathsRelative:
			return tool + "(" + strings.TrimPrefix(glob, "/") + ")", true
		}
		return tool + "(" + claudeAbs(p.Worktree) + "/" + strings.TrimPrefix(glob, "/") + ")", true
	}
	allow, deny := []string{}, []string{}
	add := func(l *[]string, r string, ok bool) {
		if ok && !slices.Contains(*l, r) {
			*l = append(*l, r)
		}
	}
	bash := func(l *[]string, pattern string, bare bool) {
		if pattern == "*" {
			add(l, "Bash", true)
			return
		}
		add(l, "Bash("+pattern+")", true)
		// Our "curl *" also takes "curl" alone; a deny says both.
		if b, ok := strings.CutSuffix(pattern, " *"); ok && bare {
			add(l, "Bash("+b+")", true)
		}
	}

	if !p.NoWrite {
		for _, g := range p.Write {
			r, ok := path("Edit", g)
			add(&allow, r, ok)
			r, ok = path("Write", g)
			add(&allow, r, ok)
		}
	}
	for _, cmd := range p.AllowCommands {
		bash(&allow, cmd, false)
	}
	if !p.Offline {
		for _, d := range p.Domains {
			add(&allow, "WebFetch(domain:"+d+")", true)
		}
	}

	for _, cmd := range p.FixedDenyCommands {
		bash(&deny, cmd, true)
	}
	for _, g := range p.ProtectedWrite {
		r, ok := path("Edit", g)
		add(&deny, r, ok)
	}
	for _, g := range p.DenyFiles {
		r, ok := path("Read", g)
		add(&deny, r, ok)
		r, ok = path("Edit", g)
		add(&deny, r, ok)
	}
	for _, cmd := range p.DenyCommands {
		bash(&deny, cmd, true)
	}

	settings := ClaudeSettingsPath(p.Worktree, p.AgentID)
	args := []string{"--settings", settings}
	var off []string
	if p.NoWrite {
		off = append(off, "Edit", "Write", "NotebookEdit")
	}
	switch {
	case p.Offline:
		off = append(off, "WebFetch", "WebSearch")
	case !p.AnyDomain:
		off = append(off, "WebSearch") // a web tool without a URL is denied with a list of domains
	}
	if len(off) > 0 {
		args = append(args, "--disallowedTools")
		args = append(args, off...)
	}
	perms := map[string]any{"allow": allow, "deny": deny, "disableBypassPermissionsMode": "disable"}
	// The statusLine (the usage of the character) lives in the same file,
	// unless the fallback moved it to the hook file.
	statusLine := ""
	if !p.Fallbacks.ClaudeStatusLineInLocal {
		var err error
		if statusLine, err = claudeHookCommand(machineOS(p.OS), "", "StatusLine"); err != nil {
			return NativeControls{}, fmt.Errorf("native controls of %s: %w", p.AgentID, err)
		}
	}
	return NativeControls{Args: args, Files: []NativeFile{{
		Path: settings,
		Merge: func(cur []byte) ([]byte, error) {
			obj, err := parseObject(cur, settings)
			if err != nil {
				return nil, err
			}
			obj["permissions"] = perms
			// The auto memory of Claude Code is kept per mirror and profile,
			// so every character of a workspace would share and contaminate
			// it; the Mestre has its own memory (scrolls and sheets).
			obj["autoMemoryEnabled"] = false
			setClaudeStatusLine(obj, statusLine)
			return marshalObject(obj)
		},
	}}}, nil
}

// machineOS is the system of the machine; empty means the one running.
func machineOS(goos string) string {
	if goos == "" {
		return runtime.GOOS
	}
	return goos
}

// claudeAbs is an absolute path in the form of the path rules of Claude
// Code: //<path>, with C:\x as //c/x.
func claudeAbs(p string) string {
	s := strings.TrimRight(filepath.ToSlash(strings.ReplaceAll(p, `\`, "/")), "/")
	if len(s) >= 2 && s[1] == ':' {
		s = "/" + strings.ToLower(s[:1]) + s[2:]
	}
	return "/" + s
}
