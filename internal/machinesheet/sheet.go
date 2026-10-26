package machinesheet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/mestre-ia/mestre/internal/protocol"
)

// The operating systems of the Guild.
const (
	OSLinux   = "linux"
	OSWindows = "windows"
)

// The providers a sheet can list.
const (
	ProviderClaudeCode  = "claude-code"
	ProviderCodex       = "codex"
	ProviderAntigravity = "antigravity"
)

// KnownProviders are the provider names a requirement accepts, sorted.
func KnownProviders() []string {
	return []string{ProviderAntigravity, ProviderClaudeCode, ProviderCodex}
}

// The reasons an enabled Operator of the PC is not ready.
const (
	ReasonNoUser     = "sem_usuario"
	ReasonLocked     = "tela_bloqueada"
	ReasonUAC        = "uac"
	ReasonTaskFailed = "tarefa"
)

// Profile is an account profile of a provider on this machine: its name, the
// version of the CLI, whether it is logged in and whether the conformance of
// that version passed. No secret is ever here.
type Profile struct {
	Name       string `json:"name"`
	Version    string `json:"version,omitempty"`
	LoggedIn   bool   `json:"logged_in"`
	Conformant bool   `json:"conformant"`
	// CLIUnreachable: the conformance of this version found that the `mestre`
	// CLI, called by the shell tool of the provider, does not reach the
	// engine; a character that calls the CLI is not recruited on this profile.
	CLIUnreachable bool `json:"cli_unreachable,omitempty"`
	// Default marks the profile that a character sheet without `account`
	// uses.
	Default bool `json:"default,omitempty"`
}

// Provider is a provider CLI installed on the machine, with its profiles.
type Provider struct {
	Provider string    `json:"provider"`
	Profiles []Profile `json:"profiles"`
}

// Browser is the browser the browser MCPs of the characters use.
type Browser struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// Operator is the state of the Operator of the PC on the machine.
type Operator struct {
	Enabled bool `json:"enabled"`
	Ready   bool `json:"ready"`
	// Reason says why an enabled Operator is not ready.
	Reason string `json:"reason,omitempty"`
}

// GPU is a graphics adapter.
type GPU struct {
	Model    string `json:"model"`
	MemBytes uint64 `json:"mem_bytes,omitempty"`
}

// Resources are the numbers of the machine.
type Resources struct {
	CPUCores      int    `json:"cpu_cores"`
	MemBytes      uint64 `json:"mem_bytes"`
	DiskFreeBytes uint64 `json:"disk_free_bytes"`
	GPUs          []GPU  `json:"gpus"`
}

// Sheet is the machine sheet. The Tower fills everything but the four fields
// the Guild owns (MachineID, Name, Excluded, Online): the Citadel sets them
// from the registry and the connection when it hands the sheet out, and
// ignores them when they arrive.
type Sheet struct {
	// Rev grows with every change the Tower reports. The Citadel drops a
	// machine.sheet whose Rev is not above the one it keeps, so a patch that
	// waited in the outbox cannot undo the newer sheet of a later hello.
	Rev       int64  `json:"rev"`
	MachineID string `json:"machine_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Host      string `json:"host,omitempty"`
	Instance  string `json:"instance,omitempty"`

	OS        string `json:"os"`
	OSVersion string `json:"os_version,omitempty"`
	Arch      string `json:"arch"`
	// GUI is true when a user session with a desktop exists on the machine.
	GUI bool `json:"gui"`
	// IPs are the addresses of the machine on the local network.
	IPs []string `json:"ips,omitempty"`

	Providers   []Provider        `json:"providers"`
	Browser     *Browser          `json:"browser,omitempty"`
	Operador    Operator          `json:"operador"`
	Resources   Resources         `json:"resources"`
	Transcricao bool              `json:"transcricao"`
	Tools       map[string]string `json:"tools,omitempty"`

	Excluded bool `json:"excluded"`
	Online   bool `json:"online"`
}

// MaxBytes is the most a sheet takes, as the Citadel keeps it. A sheet is a few
// kilobytes; the limit keeps the row of the machine, which goes in the change
// log, far from the 1 MiB of a frame, over which a record can only go by full
// copy.
const MaxBytes = 64 << 10

// ErrInvalid: the sheet lacks a required field or one has a wrong type.
var ErrInvalid = errors.New("machinesheet: invalid sheet")

// ErrStale: the patch is not newer than the sheet kept.
var ErrStale = errors.New("machinesheet: stale revision")

// guildOwned are the fields only the Guild sets.
var guildOwned = []string{"machine_id", "name", "excluded", "online"}

// Clone returns a deep copy.
func (s Sheet) Clone() Sheet {
	c := s
	c.IPs = slices.Clone(s.IPs)
	if s.Providers != nil {
		c.Providers = make([]Provider, len(s.Providers))
		for i, p := range s.Providers {
			c.Providers[i] = Provider{Provider: p.Provider, Profiles: slices.Clone(p.Profiles)}
		}
	}
	if s.Browser != nil {
		b := *s.Browser
		c.Browser = &b
	}
	c.Resources.GPUs = slices.Clone(s.Resources.GPUs)
	c.Tools = maps.Clone(s.Tools)
	return c
}

// normalized gives the sheet the shape that is compared and sent: lists that
// are empty and never null, in a fixed order.
func (s Sheet) normalized() Sheet {
	c := s.Clone()
	if c.Providers == nil {
		c.Providers = []Provider{}
	}
	slices.SortFunc(c.Providers, func(a, b Provider) int { return strings.Compare(a.Provider, b.Provider) })
	for i := range c.Providers {
		if c.Providers[i].Profiles == nil {
			c.Providers[i].Profiles = []Profile{}
		}
		slices.SortFunc(c.Providers[i].Profiles, func(a, b Profile) int { return strings.Compare(a.Name, b.Name) })
	}
	if c.Resources.GPUs == nil {
		c.Resources.GPUs = []GPU{}
	}
	slices.Sort(c.IPs)
	if !c.Operador.Enabled {
		c.Operador = Operator{}
	} else if c.Operador.Ready {
		c.Operador.Reason = ""
	}
	if len(c.Tools) == 0 {
		c.Tools = nil
	}
	return c
}

// Object is the sheet as the payload of the protocol carries it.
func (s Sheet) Object() protocol.Object {
	b, err := json.Marshal(s.normalized())
	if err != nil {
		panic(err) // a Sheet always encodes
	}
	var obj protocol.Object
	if err := json.Unmarshal(b, &obj); err != nil {
		panic(err)
	}
	return obj
}

// Diff is the machine.sheet payload that takes a Citadel holding prev to cur:
// only the fields whose value changed, a removed field as null, and the Rev
// of cur. changed is false when nothing but the Rev differs, and then there
// is nothing to send. A zero prev yields the whole sheet.
func Diff(prev, cur Sheet) (patch protocol.Object, changed bool) {
	p, c := prev.Object(), cur.Object()
	out := protocol.Object{}
	for k, v := range c {
		if k == "rev" || slices.Contains(guildOwned, k) {
			continue
		}
		if old, ok := p[k]; !ok || !bytes.Equal(old, v) {
			out[k] = v
		}
	}
	for k := range p {
		if k == "rev" || slices.Contains(guildOwned, k) {
			continue
		}
		if _, ok := c[k]; !ok {
			out[k] = json.RawMessage("null")
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	out["rev"] = c["rev"]
	return out, true
}

// Equal says whether two sheets say the same, apart from the Rev and the
// fields of the Guild.
func Equal(a, b Sheet) bool {
	_, changed := Diff(a, b)
	return !changed
}

// Merge applies a patch to the sheet kept, or, with replace, takes the
// payload as the whole sheet (the hello). The fields of the Guild in the
// payload are ignored, null removes a field, and fields this version does not
// know are dropped. The result must be a valid sheet. A patch needs a Rev
// above the kept one (ErrStale otherwise).
func Merge(kept Sheet, patch protocol.Object, replace bool) (Sheet, error) {
	base := protocol.Object{}
	if !replace {
		base = kept.Object()
	}
	var rev int64
	if raw, ok := patch["rev"]; ok && !isNull(raw) {
		if err := json.Unmarshal(raw, &rev); err != nil {
			return Sheet{}, fmt.Errorf("%w: rev: %v", ErrInvalid, err)
		}
	} else if !replace {
		return Sheet{}, fmt.Errorf("%w: rev is required in a patch", ErrInvalid)
	}
	if !replace && rev <= kept.Rev {
		return Sheet{}, fmt.Errorf("%w: patch %d, kept %d", ErrStale, rev, kept.Rev)
	}
	for k, v := range patch {
		switch {
		case slices.Contains(guildOwned, k):
		case isNull(v):
			delete(base, k)
		default:
			base[k] = v
		}
	}
	for _, k := range []string{"os", "arch", "providers"} {
		if _, ok := base[k]; !ok {
			return Sheet{}, fmt.Errorf("%w: %s is required", ErrInvalid, k)
		}
	}
	b, err := json.Marshal(base)
	if err != nil {
		return Sheet{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if len(b) > MaxBytes {
		return Sheet{}, fmt.Errorf("%w: the sheet has %d bytes, the limit is %d", ErrInvalid, len(b), MaxBytes)
	}
	var out Sheet
	if err := json.Unmarshal(b, &out); err != nil {
		return Sheet{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := out.Validate(); err != nil {
		return Sheet{}, err
	}
	out.MachineID, out.Name, out.Excluded, out.Online = "", "", false, false
	return out.normalized(), nil
}

func isNull(raw json.RawMessage) bool { return string(bytes.TrimSpace(raw)) == "null" }

// Validate checks what the Guild relies on: the system, the architecture and
// the providers.
func (s Sheet) Validate() error {
	if s.OS != OSLinux && s.OS != OSWindows {
		return fmt.Errorf("%w: os %q is not linux or windows", ErrInvalid, s.OS)
	}
	if s.Arch == "" {
		return fmt.Errorf("%w: arch is required", ErrInvalid)
	}
	if s.Providers == nil {
		return fmt.Errorf("%w: providers is required", ErrInvalid)
	}
	for _, p := range s.Providers {
		if p.Provider == "" {
			return fmt.Errorf("%w: a provider has no name", ErrInvalid)
		}
	}
	return nil
}

// Provider returns the entry of an installed provider.
func (s Sheet) Provider(name string) (Provider, bool) {
	for _, p := range s.Providers {
		if p.Provider == name {
			return p, true
		}
	}
	return Provider{}, false
}

// ProfileFor returns a profile of the provider: the one named, or, when name
// is empty, the profile marked as default (the only one, if none is marked).
func (p Provider) ProfileFor(name string) (Profile, bool) {
	if name != "" {
		for _, pr := range p.Profiles {
			if pr.Name == name {
				return pr, true
			}
		}
		return Profile{}, false
	}
	for _, pr := range p.Profiles {
		if pr.Default {
			return pr, true
		}
	}
	if len(p.Profiles) == 1 {
		return p.Profiles[0], true
	}
	return Profile{}, false
}
