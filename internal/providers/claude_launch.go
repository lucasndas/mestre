package providers

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// claudeFlag is a flag of the command line of Claude Code with the values
// that follow it.
type claudeFlag struct {
	name   string
	values []string
}

// claudeValueFlags take exactly one value; every other flag with a value
// takes the arguments up to the next flag (--add-dir, --disallowedTools…).
var claudeValueFlags = []string{"--append-system-prompt-file", "--settings", "--permission-mode", "--session-id", "--resume", "-r", "--model", "--system-prompt", "--system-prompt-file", "--setting-sources"}

// parseClaudeFlags splits arguments into flags and their values; `--flag=v`
// is the flag with the value v.
func parseClaudeFlags(args []string) []claudeFlag {
	var out []claudeFlag
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			continue // a value that no flag of ours owns
		}
		f := claudeFlag{name: a}
		if name, v, ok := strings.Cut(a, "="); ok && strings.HasPrefix(a, "--") {
			f.name, f.values = name, []string{v}
			out = append(out, f)
			continue
		}
		if slices.Contains(claudeValueFlags, a) {
			if i+1 < len(args) {
				i++
				f.values = []string{args[i]}
			}
		} else {
			for i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				f.values = append(f.values, args[i])
			}
		}
		out = append(out, f)
	}
	return out
}

// claudeForbidden are the flags the base and the engine never pass
// (docs/provedores/claude-code.md#flags), and why.
var claudeForbidden = map[string]string{
	"--dangerously-skip-permissions":       "it skips the permissions",
	"--allow-dangerously-skip-permissions": "it allows to skip the permissions",
	"--bare":                               "it skips hooks, skills and instructions",
	"--system-prompt":                      "it replaces the default system prompt",
	"--system-prompt-file":                 "it replaces the default system prompt",
	"--system-prompt-snapshot":             "the Mestre reuses the saved prompt of a resumed session",
	"--setting-sources":                    "it can drop the local hooks",
	"-p":                                   "the base is interactive",
	"--print":                              "the base is interactive",
	"--session-id":                         "the engine generates the session id",
	"--resume":                             "the engine resumes by id",
	"-r":                                   "the engine resumes by id",
}

// checkClaudeLaunch checks what the command of a Claude Code character must
// have and must not have: the Mestre layer, the --settings file of the
// character, the account profile and the only permission mode allowed.
func checkClaudeLaunch(req LaunchRequest, env []string) error {
	if req.AgentID == "" {
		return fmt.Errorf("claude-code: no agent id (MESTRE_AGENT_ID)")
	}
	// Commander reads a short flag with more after it as a cluster or a
	// flag with its value (-dc is -d -c, -r1234 is --resume 1234), which
	// the check by name would not see: a start has only short flags alone.
	for _, a := range slices.Concat(req.Base.Args, req.ProviderArgs) {
		if len(a) > 2 && a[0] == '-' && a[1] != '-' {
			return fmt.Errorf("claude-code: %q joins a short flag to more text; a start gives each short flag alone", a)
		}
	}
	for _, f := range parseClaudeFlags(req.Base.Args) {
		switch f.name {
		case "--append-system-prompt-file", "--settings", "--add-dir":
			return fmt.Errorf("claude-code: the base passes %s; the engine does, for the character only", f.name)
		}
	}
	var layer, settings string
	for _, src := range [][]string{req.Base.Args, req.ProviderArgs} {
		for _, f := range parseClaudeFlags(src) {
			if why, bad := claudeForbidden[f.name]; bad {
				return fmt.Errorf("claude-code: %s is forbidden: %s", f.name, why)
			}
			switch f.name {
			case "--permission-mode":
				if len(f.values) != 1 || f.values[0] != "default" {
					return fmt.Errorf("claude-code: --permission-mode %v is forbidden: default is the only mode allowed", f.values)
				}
			case "--append-system-prompt-file":
				if layer != "" || len(f.values) != 1 {
					return fmt.Errorf("claude-code: --append-system-prompt-file given twice or without a file")
				}
				layer = f.values[0]
			case "--settings":
				if settings != "" || len(f.values) != 1 {
					return fmt.Errorf("claude-code: --settings given twice or without a file")
				}
				settings = f.values[0]
			}
		}
	}
	if !pathEndsIn(layer, ".mestre", "agents", req.AgentID, "MESTRE.md") {
		return fmt.Errorf("claude-code: the Mestre layer of %s is --append-system-prompt-file .mestre/agents/%s/MESTRE.md; got %q", req.AgentID, req.AgentID, layer)
	}
	if !pathEndsIn(settings, ".mestre", "agents", req.AgentID, "claude-settings.json") {
		return fmt.Errorf("claude-code: the native rules of %s are --settings .mestre/agents/%s/claude-settings.json; got %q", req.AgentID, req.AgentID, settings)
	}
	for _, f := range parseClaudeFlags(req.ProviderArgs) {
		if f.name != "--add-dir" {
			continue
		}
		if len(f.values) != 1 || filepath.Clean(filepath.FromSlash(f.values[0])) != filepath.Clean(filepath.FromSlash(slashDir(layer))) {
			return fmt.Errorf("claude-code: --add-dir %v: the only folder allowed is .mestre/agents/%s of the character", f.values, req.AgentID)
		}
	}
	profile := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "CLAUDE_CONFIG_DIR="); ok {
			profile = v
		}
	}
	if profile == "" {
		return fmt.Errorf("claude-code: no account profile (CLAUDE_CONFIG_DIR)")
	}
	return nil
}

// pathEndsIn reports whether the last elements of p are tail, whatever the
// separator.
func pathEndsIn(p string, tail ...string) bool {
	parts := strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' })
	return len(parts) >= len(tail) && slices.Equal(parts[len(parts)-len(tail):], tail)
}

// slashDir is the folder of p, whatever the separator.
func slashDir(p string) string {
	i := strings.LastIndexAny(p, `/\`)
	if i < 0 {
		return "."
	}
	return p[:i]
}

// DeliverMestreLayer: --append-system-prompt-file, which works in the
// interactive mode and adds to the system prompt, above the AGENTS.md; and,
// for the skills of the character, --add-dir with the folder of the
// character (docs/provedores/claude-code.md#camada-do-mestre). It writes no
// file.
func (c *claudeCode) DeliverMestreLayer(req LayerRequest) (Layer, error) {
	if id := lastButOne(req.Path); id == "" || !pathEndsIn(req.Path, ".mestre", "agents", id, "MESTRE.md") {
		return Layer{}, fmt.Errorf("claude-code mestre layer: %q is not .mestre/agents/<id>/MESTRE.md", req.Path)
	}
	args := []string{"--append-system-prompt-file", req.Path}
	if req.CharacterSkills {
		args = append(args, "--add-dir", slashDir(req.Path))
	}
	return Layer{Args: args}, nil
}

// lastButOne is the name of the folder that holds the file p.
func lastButOne(p string) string {
	parts := strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2]
}
