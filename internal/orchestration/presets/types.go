// Package presets holds the bases and the character sheets of the Guild
// (docs/orquestracao/bases-e-fichas.md): the presets users define and agents
// only use. A base says how a terminal starts, a sheet who the character is.
// The package validates both when they are saved, resolves a base for the
// system of a machine, exports and imports them as a file, and chooses the
// machines that fit a sheet. The panel (M6), the character registry (M4) and
// the Operator of the PC (M7) use it through Catalog.
package presets

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"go.yaml.in/yaml/v3"
)

// Kind and Version identify the file of an export
// (docs/orquestracao/bases-e-fichas.md#exportar-e-importar).
const (
	Kind    = "mestre/presets"
	Version = 1
)

// The systems a base can run on.
const (
	Linux   = "linux"
	Windows = "windows"
)

// Agent types of a sheet.
const (
	AgentClaudeCode  = "claude-code"
	AgentCodex       = "codex"
	AgentAntigravity = "antigravity"
	AgentShell       = "shell"
)

// Agents are the values of a sheet's `agent`.
func Agents() []string {
	return []string{AgentClaudeCode, AgentCodex, AgentAntigravity, AgentShell}
}

// Lifecycles of a sheet.
const (
	Ephemeral  = "ephemeral"
	Persistent = "persistent"
)

// Defaults of a sheet's optional fields.
const (
	DefaultMaxIdle   = 120 * time.Minute
	DefaultMaxCopies = 3
)

// Base is how a terminal starts.
type Base struct {
	Name    string   `yaml:"name" json:"name"`
	Command string   `yaml:"command" json:"command"`
	Args    []string `yaml:"args,omitempty" json:"args,omitempty"`
	// Shell, when set, runs the command inside that shell (bash, pwsh,
	// powershell, cmd); otherwise it runs directly.
	Shell string `yaml:"shell,omitempty" json:"shell,omitempty"`
	// Cwd is the start folder, with {mesa}, {workspace} and {home}; empty is
	// {mesa}.
	Cwd       string            `yaml:"cwd,omitempty" json:"cwd,omitempty"`
	Env       map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	Platforms []Platform        `yaml:"platforms" json:"platforms"`
	// Machines restricts the base to these machines of the Guild.
	Machines []string       `yaml:"machines,omitempty" json:"machines,omitempty"`
	Requires map[string]any `yaml:"requires,omitempty" json:"requires,omitempty"`
	Ready    Ready          `yaml:"ready" json:"ready"`
}

// Platform is a system the base runs on, with its own command and arguments
// when it needs them.
type Platform struct {
	OS      string   `yaml:"os" json:"os"`
	Command string   `yaml:"command,omitempty" json:"command,omitempty"`
	Args    []string `yaml:"args,omitempty" json:"args,omitempty"`
}

// ReadyMode is how the engine knows the terminal is ready for input.
type ReadyMode string

// The ways of knowing it.
const (
	ReadyHook    ReadyMode = "hook"
	ReadyPattern ReadyMode = "pattern"
	ReadyDelay   ReadyMode = "delay"
)

// Ready is the `ready` field: `hook`, `{pattern: <regex>}` or
// `{delay: <seconds>}`.
type Ready struct {
	Mode    ReadyMode
	Pattern string
	// Delay is in seconds.
	Delay int
}

func readyFromAny(v any) (Ready, error) {
	switch x := v.(type) {
	case string:
		if x == string(ReadyHook) {
			return Ready{Mode: ReadyHook}, nil
		}
	case map[string]any:
		if len(x) == 1 {
			if p, ok := x["pattern"].(string); ok {
				return Ready{Mode: ReadyPattern, Pattern: p}, nil
			}
			if d, ok := x["delay"]; ok {
				if n, ok := whole(d); ok {
					return Ready{Mode: ReadyDelay, Delay: n}, nil
				}
			}
		}
	}
	return Ready{}, fmt.Errorf("`ready` aceita `hook`, `{pattern: <regex>}` ou `{delay: <segundos>}`")
}

func (r Ready) value() any {
	switch r.Mode {
	case ReadyHook:
		return "hook"
	case ReadyPattern:
		return map[string]any{"pattern": r.Pattern}
	case ReadyDelay:
		return map[string]any{"delay": r.Delay}
	}
	return nil
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (r *Ready) UnmarshalYAML(n *yaml.Node) error {
	var v any
	if err := n.Decode(&v); err != nil {
		return err
	}
	got, err := readyFromAny(v)
	*r = got
	return err
}

// MarshalYAML implements yaml.Marshaler.
func (r Ready) MarshalYAML() (any, error) { return r.value(), nil }

// UnmarshalJSON implements json.Unmarshaler.
func (r *Ready) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	got, err := readyFromAny(v)
	*r = got
	return err
}

// MarshalJSON implements json.Marshaler.
func (r Ready) MarshalJSON() ([]byte, error) { return json.Marshal(r.value()) }

// Sheet is who a character is.
type Sheet struct {
	Name string `yaml:"name" json:"name"`
	Base string `yaml:"base" json:"base"`
	// Agent is claude-code, codex, antigravity or shell.
	Agent string `yaml:"agent" json:"agent"`
	// Class is the default class; `recruit --class` replaces it.
	Class string `yaml:"class,omitempty" json:"class,omitempty"`
	// Account is the account profile; empty is the default one of the
	// provider on the machine.
	Account          string         `yaml:"account,omitempty" json:"account,omitempty"`
	MestreSkills     *bool          `yaml:"mestre_skills,omitempty" json:"mestre_skills,omitempty"`
	ExtraSkills      []string       `yaml:"extra_skills,omitempty" json:"extra_skills,omitempty"`
	PreferredMachine string         `yaml:"preferred_machine,omitempty" json:"preferred_machine,omitempty"`
	Requires         map[string]any `yaml:"requires,omitempty" json:"requires,omitempty"`
	Lifecycle        string         `yaml:"lifecycle,omitempty" json:"lifecycle,omitempty"`
	// MaxIdle is a duration such as "240m"; empty is DefaultMaxIdle.
	MaxIdle   string `yaml:"max_idle,omitempty" json:"max_idle,omitempty"`
	MaxCopies *int   `yaml:"max_copies,omitempty" json:"max_copies,omitempty"`
	// Restrictions have the format of permissions.yaml and only restrict
	// the class.
	Restrictions map[string]any `yaml:"restrictions,omitempty" json:"restrictions,omitempty"`
}

// HasSkills is whether the character gets the Mestre's skills (default true).
func (s Sheet) HasSkills() bool { return s.MestreSkills == nil || *s.MestreSkills }

// LifecycleOrDefault is the lifecycle, ephemeral when empty.
func (s Sheet) LifecycleOrDefault() string {
	if s.Lifecycle == "" {
		return Ephemeral
	}
	return s.Lifecycle
}

// Idle is the longest idle time of a persistent character.
func (s Sheet) Idle() time.Duration {
	if d, err := time.ParseDuration(s.MaxIdle); err == nil && d > 0 {
		return d
	}
	return DefaultMaxIdle
}

// Copies is the number of simultaneous copies.
func (s Sheet) Copies() int {
	if s.MaxCopies == nil {
		return DefaultMaxCopies
	}
	return *s.MaxCopies
}

// Document is the file of an export and of an import: YAML or JSON with the
// same structure.
type Document struct {
	Kind    string  `yaml:"kind" json:"kind"`
	Version int     `yaml:"version" json:"version"`
	Bases   []Base  `yaml:"bases,omitempty" json:"bases,omitempty"`
	Sheets  []Sheet `yaml:"sheets,omitempty" json:"sheets,omitempty"`
}

// whole reads an integer as YAML and JSON decode it.
func whole(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case uint64:
		return int(n), true
	case float64:
		if n == float64(int(n)) {
			return int(n), true
		}
	}
	return 0, false
}

// normalize turns the integral float64 that JSON leaves in a generic tree
// into int, so a value read back from the database exports as `8589934592`
// and not as `8.589934592e+09`. It returns a copy.
func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = normalize(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalize(e)
		}
		return out
	case float64:
		if n, ok := whole(x); ok {
			return n
		}
	}
	return v
}

func normalizeMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	return normalize(m).(map[string]any)
}

// clone copies a base so a caller cannot change the stored one.
func (b Base) clone() Base {
	b.Args = slices.Clone(b.Args)
	b.Machines = slices.Clone(b.Machines)
	b.Platforms = slices.Clone(b.Platforms)
	for i := range b.Platforms {
		b.Platforms[i].Args = slices.Clone(b.Platforms[i].Args)
	}
	if b.Env != nil {
		env := make(map[string]string, len(b.Env))
		for k, v := range b.Env {
			env[k] = v
		}
		b.Env = env
	}
	b.Requires = normalizeMap(b.Requires)
	return b
}

func (s Sheet) clone() Sheet {
	s.ExtraSkills = slices.Clone(s.ExtraSkills)
	s.Requires = normalizeMap(s.Requires)
	s.Restrictions = normalizeMap(s.Restrictions)
	if s.MestreSkills != nil {
		v := *s.MestreSkills
		s.MestreSkills = &v
	}
	if s.MaxCopies != nil {
		v := *s.MaxCopies
		s.MaxCopies = &v
	}
	return s
}
