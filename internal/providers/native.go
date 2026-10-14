package providers

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// NativePolicy is what the native controls of a provider can hold of the
// effective rules of one character (docs/classes/reforco-nativo.md): the
// second barrier, read by the provider at its start. internal/classes
// builds it from the effective rules; the full decision stays with the
// dispatcher. ask and tools (mcp, other) never become native rules.
type NativePolicy struct {
	AgentID string
	// OS is the system of the machine ("linux", "windows").
	OS string
	// Worktree is the character's worktree, absolute.
	Worktree string
	// ProfileHome is the HOME of the account profile (Antigravity).
	ProfileHome string

	// Write are the globs, relative to the worktree, the character may write.
	Write []string
	// NoWrite is true when nothing at all may be written (no write glob,
	// and no decree or exception frees one).
	NoWrite bool
	// DenyFiles are the globs nothing reads or writes: relative to the
	// worktree, or absolute (slash form).
	DenyFiles []string
	// ProtectedWrite are the globs the fixed rules close to writing, at any
	// depth of the worktree ("**/.git/**").
	ProtectedWrite []string

	// AllowCommands and DenyCommands are command patterns where * matches
	// any text.
	AllowCommands, DenyCommands []string
	// FixedDenyCommands are the commands the fixed rules deny always (git
	// push, git remote), never freed natively.
	FixedDenyCommands []string

	// Offline is true with no internet at all.
	Offline bool
	// AnyDomain is internet with no list of domains: the hosted search may
	// run. With a list, a web tool without a URL is denied.
	AnyDomain bool
	// Domains the web tools may reach.
	Domains []string

	// ToolDenied reports whether the effective rules deny every call of a
	// tool of kind subagent or other, by its native name (nil: none).
	ToolDenied func(name string) bool

	Fallbacks NativeFallbacks
}

// NativeFallbacks are the "Se falhar" paths of the items of
// docs/classes/reforco-nativo.md#verificar-na-implementação, switched on by
// the conformance test (docs/provedores/conformidade.md) when the default
// fails. The zero value is the default of each item.
type NativeFallbacks struct {
	// ClaudePaths is how the path rules of Claude Code are written.
	ClaudePaths ClaudePathForm
	// AntigravityNoDeny leaves the fixed rules of Antigravity to the hook
	// alone: no permissions.deny of the Mestre in the profile's settings.json.
	AntigravityNoDeny bool
}

// ClaudePathForm is the form of the path rules of Claude Code.
type ClaudePathForm string

// The forms: absolute (the default, //<worktree>/<glob>), relative to the
// folder Claude Code runs in, or none (path rules only in the dispatcher).
const (
	ClaudePathsAbsolute ClaudePathForm = ""
	ClaudePathsRelative ClaudePathForm = "relative"
	ClaudePathsOff      ClaudePathForm = "off"
)

// NativeControls are the native controls of one character: the arguments
// that go in LaunchRequest.ProviderArgs and the files to write before the
// start.
type NativeControls struct {
	Args  []string
	Files []NativeFile
}

// NativeFile is a file of the native controls. Merge builds the new content
// from the current one (nil when the file does not exist) and keeps what is
// not the Mestre's; an error means the file cannot be written (an invalid
// settings.json of the profile): the character does not start.
type NativeFile struct {
	Path  string
	Merge func(current []byte) ([]byte, error)
}

// NativeTranslator is the part of an adapter that translates the native
// policy (docs/classes/reforco-nativo.md#tradução-por-provedor). The three
// providers implement it; the null adapter has no native controls.
type NativeTranslator interface {
	Native(p NativePolicy) (NativeControls, error)
}

// NativeOf is the translator of an adapter, if it has one.
func NativeOf(a Adapter) (NativeTranslator, bool) {
	t, ok := a.(NativeTranslator)
	return t, ok
}

// ErrNativeFile is a native file the engine cannot write.
var ErrNativeFile = errors.New("native control file cannot be written")

func (p NativePolicy) check() error {
	if p.AgentID == "" || strings.ContainsAny(p.AgentID, `/\. `) {
		return fmt.Errorf("native controls: invalid agent id %q", p.AgentID)
	}
	if p.Worktree == "" || !absolute(p.Worktree) {
		return fmt.Errorf("native controls of %s: the worktree %q is not absolute", p.AgentID, p.Worktree)
	}
	return nil
}

// absolute reports whether p is absolute on Linux or on Windows.
func absolute(p string) bool {
	s := filepath.ToSlash(strings.ReplaceAll(p, `\`, "/"))
	return strings.HasPrefix(s, "/") || len(s) >= 3 && s[1] == ':' && s[2] == '/'
}

// join is a path under the worktree in the system's form.
func join(base string, rel ...string) string {
	return filepath.Join(append([]string{base}, rel...)...)
}

// starless turns " *" at the end into "*": the form of the providers whose
// wildcard needs no space ("git push *" → "git push*").
func starless(p string) string {
	if b, ok := strings.CutSuffix(p, " *"); ok {
		return b + "*"
	}
	return p
}
