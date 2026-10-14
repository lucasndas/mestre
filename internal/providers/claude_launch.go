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

// claudeOption is one option a start of Claude Code may have
// (docs/provedores/claude-code.md#flags). The check is a list of what is
// allowed, as for Codex and Antigravity: any other option, a positional (a
// prompt, a subcommand such as mcp or update) and "--" are refused, since
// Commander would read them in a way the check does not (after "--" every
// argument, the engine's --settings included, is text of the prompt).
type claudeOption struct {
	// value: exactly one value (--name v or --name=v); list: one or more,
	// up to the next option (--disallowedTools); optional: a value only
	// with "=" (--debug=api), none alone.
	value, list, optional bool
	// base: the base may give it; engine: the engine's arguments may.
	base, engine bool
	// values, when set, are the only values the option takes.
	values []string
}

// claudeEfforts are the levels of --effort in the Claude Code reference
// (https://code.claude.com/docs/en/cli-reference).
var claudeEfforts = []string{"low", "medium", "high", "xhigh", "max", "ultracode"}

var claudeOptions = map[string]claudeOption{
	// The model and how the CLI shows things: the base's.
	"--model":          {value: true, base: true},
	"--fallback-model": {value: true, base: true},
	"--effort":         {value: true, base: true, values: claudeEfforts},
	"--autocompact":    {value: true, base: true},
	"--name":           {value: true, base: true},
	"-n":               {value: true, base: true},
	"--verbose":        {base: true},
	"--debug":          {optional: true, base: true},
	"-d":               {base: true},
	"--no-chrome":      {base: true},
	// Only on the base of the Operador do PC, which the bases check when
	// saved (docs/orquestracao/bases-e-fichas.md#base-especial-operador-do-pc).
	"--chrome": {base: true},
	// default is the only mode, from either.
	"--permission-mode": {value: true, base: true, engine: true},
	// The Mestre layer, the native rules and the skills of the character:
	// the engine's (DeliverMestreLayer, Native).
	"--append-system-prompt-file": {value: true, engine: true},
	"--settings":                  {value: true, engine: true},
	"--add-dir":                   {value: true, engine: true},
	"--disallowedTools":           {list: true, engine: true},
}

// claudeForbidden are flags the documents name as forbidden
// (docs/provedores/claude-code.md#flags), and why, for the error; any
// option out of claudeOptions is refused anyway.
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
	"--continue":                           "it resumes the most recent session of the folder; the Mestre resumes by id",
	"-c":                                   "it resumes the most recent session of the folder; the Mestre resumes by id",
}

// parseClaudeFlags reads arguments with the grammar of claudeOptions. An
// argument that is not an option of the list, a short flag joined to more
// text (Commander reads -dc as -d -c and -r1234 as --resume 1234), a
// positional or "--" is an error. engine tells whose arguments they are.
func parseClaudeFlags(args []string, engine bool) ([]claudeFlag, error) {
	who := "the base"
	if engine {
		who = "the engine"
	}
	var out []claudeFlag
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--" || a == "-":
			return nil, fmt.Errorf("claude-code: %s passes %q: every argument after it, the engine's included, would be text of the prompt", who, a)
		case !strings.HasPrefix(a, "-"):
			return nil, fmt.Errorf("claude-code: %s passes %q, which is not an option; a start has no prompt and no subcommand", who, a)
		case len(a) > 2 && a[1] != '-':
			return nil, fmt.Errorf("claude-code: %q joins a short flag to more text; a start gives each short flag alone", a)
		}
		name, v, inline := strings.Cut(a, "=")
		opt, ok := claudeOptions[name]
		switch {
		case !ok:
			if why, bad := claudeForbidden[name]; bad {
				return nil, fmt.Errorf("claude-code: %s is forbidden: %s", name, why)
			}
			return nil, fmt.Errorf("claude-code: %s passes %s, an option a start may not have (docs/provedores/claude-code.md#flags)", who, name)
		case engine && !opt.engine:
			return nil, fmt.Errorf("claude-code: %s is an option of the base, not of the engine", name)
		case !engine && !opt.base:
			return nil, fmt.Errorf("claude-code: the base passes %s; the engine does, for the character only", name)
		}
		f := claudeFlag{name: name}
		switch {
		case inline && !opt.value && !opt.optional:
			return nil, fmt.Errorf("claude-code: %s takes no value", name)
		case inline:
			f.values = []string{v}
		case opt.value:
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				f.values = []string{args[i]}
			}
		case opt.list:
			for i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				f.values = append(f.values, args[i])
			}
		}
		if (opt.value || opt.list) && (len(f.values) == 0 || f.values[0] == "") {
			return nil, fmt.Errorf("claude-code: %s without a value", name)
		}
		if opt.values != nil && !slices.Contains(opt.values, f.values[0]) {
			return nil, fmt.Errorf("claude-code: %s %s: the values are %s", name, f.values[0], strings.Join(opt.values, ", "))
		}
		out = append(out, f)
	}
	return out, nil
}

// checkClaudeLaunch checks what the command of a Claude Code character must
// have and must not have: only the options of claudeOptions, the Mestre
// layer, the --settings file of the character, the account profile and the
// only permission mode allowed.
func checkClaudeLaunch(req LaunchRequest, env []string) error {
	if req.AgentID == "" {
		return fmt.Errorf("claude-code: no agent id (MESTRE_AGENT_ID)")
	}
	base, err := parseClaudeFlags(req.Base.Args, false)
	if err != nil {
		return err
	}
	eng, err := parseClaudeFlags(req.ProviderArgs, true)
	if err != nil {
		return err
	}
	var layer, settings string
	for _, f := range slices.Concat(base, eng) {
		switch f.name {
		case "--permission-mode":
			if f.values[0] != "default" {
				return fmt.Errorf("claude-code: --permission-mode %v is forbidden: default is the only mode allowed", f.values)
			}
		case "--append-system-prompt-file":
			if layer != "" {
				return fmt.Errorf("claude-code: --append-system-prompt-file given twice")
			}
			layer = f.values[0]
		case "--settings":
			if settings != "" {
				return fmt.Errorf("claude-code: --settings given twice")
			}
			settings = f.values[0]
		}
	}
	if !pathEndsIn(layer, ".mestre", "agents", req.AgentID, "MESTRE.md") {
		return fmt.Errorf("claude-code: the Mestre layer of %s is --append-system-prompt-file .mestre/agents/%s/MESTRE.md; got %q", req.AgentID, req.AgentID, layer)
	}
	if !pathEndsIn(settings, ".mestre", "agents", req.AgentID, "claude-settings.json") {
		return fmt.Errorf("claude-code: the native rules of %s are --settings .mestre/agents/%s/claude-settings.json; got %q", req.AgentID, req.AgentID, settings)
	}
	for _, f := range eng {
		if f.name == "--add-dir" && filepath.Clean(filepath.FromSlash(f.values[0])) != filepath.Clean(filepath.FromSlash(slashDir(layer))) {
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
