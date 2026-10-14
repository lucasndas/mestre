package providers

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/lucasndas/mestre/pkg/clock"
)

// ToolKind is the provider-independent type of a tool (tool.kind).
type ToolKind string

// Tool kinds of docs/provedores/adaptador.md#ferramentas-nome-nativo--tipo-toolkind.
const (
	KindShell    ToolKind = "shell"
	KindRead     ToolKind = "read"
	KindWrite    ToolKind = "write"
	KindEdit     ToolKind = "edit"
	KindSearch   ToolKind = "search"
	KindWeb      ToolKind = "web"
	KindSubagent ToolKind = "subagent"
	KindMCP      ToolKind = "mcp"
	KindInternal ToolKind = "internal"
	KindOther    ToolKind = "other"
)

// toolTable maps native tool names to kinds.
type toolTable struct {
	names    map[string]ToolKind
	prefixes []struct {
		prefix string
		kind   ToolKind
	}
}

func (t toolTable) kind(name string) ToolKind {
	if k, ok := t.names[name]; ok {
		return k
	}
	for _, p := range t.prefixes {
		if strings.HasPrefix(name, p.prefix) {
			return p.kind
		}
	}
	return KindOther
}

// SubagentTools are the native names of the tools of kind subagent of a
// provider, sorted: what the plan B of the conformance test denies in the
// character when the hooks do not hold inside a subagent.
func SubagentTools(id ID) []string {
	var table toolTable
	switch id {
	case ClaudeCode:
		table = claudeTools
	case Codex:
		table = codexTools
	case Antigravity:
		table = antigravityTools
	}
	var out []string
	for name, kind := range table.names {
		if kind == KindSubagent {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// toolFields is what an adapter read from the tool input.
type toolFields struct {
	command string
	paths   []string
	url     string
}

// finishTool applies the fallback of the map: a kind whose decision needs a
// field the adapter could not read (the command of a shell, the paths of a
// read, write, edit or search) becomes `other`, decided by the class
// default, so a rule on paths never sees a tool without them.
func finishTool(name string, kind ToolKind, f toolFields, input any) *Tool {
	switch kind {
	case KindShell:
		if f.command == "" {
			kind = KindOther
		}
	case KindRead, KindWrite, KindEdit, KindSearch:
		if len(f.paths) == 0 {
			kind = KindOther
		}
	}
	return &Tool{NativeName: name, Kind: kind, Command: f.command, Paths: f.paths, URL: f.url, RawInput: raw(input)}
}

// Options configures the adapters.
type Options struct {
	// Resolve resolves symbolic links of an existing path; the default is
	// filepath.EvalSymlinks. Tests give a fake.
	Resolve func(string) (string, error)
	// CodexTurnFailure reads the Codex transcript at Stop and reports a
	// failed turn (Codex has no failure event; the reading of the
	// transcript belongs to docs/provedores/codex.md). Nil: every Stop is
	// turn_end.
	CodexTurnFailure func(stop Event) (reason StopReason, detail string, failed bool)
	// ClaudeMultilineViaFile makes the Claude Code adapter refuse a text of
	// several lines (ErrTooLong, so the engine sends it through the file of
	// the long messages): the "Se falhar" of the multi-line paste, switched
	// on by the conformance test (docs/provedores/claude-code.md).
	ClaudeMultilineViaFile bool
	// CodexMultilineViaFile is the same for Codex
	// (docs/provedores/codex.md#verificar-na-implementação).
	CodexMultilineViaFile bool
	// Exec runs the commands of ReadUsage (the usage of Antigravity).
	Exec ExecFunc
	// Clock times the commands of ReadUsage; nil is the real clock.
	Clock clock.Clock
	// AntigravityNoPaste is the "Se falhar" of the bracketed paste of
	// Antigravity (docs/provedores/antigravity.md#verificar-na-implementação):
	// a text with line breaks is not typed (ErrTooLong), it goes through the
	// file of the long messages.
	AntigravityNoPaste bool
}

// NormalizePath normalizes a path the way the adapters do with the paths of
// a tool before any decision: absolute against cwd, without "..", links
// resolved and, on Windows, without case differences. The permission
// decision uses it for the paths of command arguments (Env.Normalize of
// internal/classes).
func (o Options) NormalizePath(p, cwd string) string { return o.paths().normalize(p, cwd) }

func (o Options) paths() pathNormalizer {
	r := o.Resolve
	if r == nil {
		r = filepath.EvalSymlinks
	}
	return pathNormalizer{resolve: r, fold: runtime.GOOS == "windows"}
}

// pathNormalizer makes paths absolute, without "..", with links resolved
// and, on Windows, without case differences, before any decision.
type pathNormalizer struct {
	resolve func(string) (string, error)
	fold    bool
}

func (n pathNormalizer) normalize(p, cwd string) string {
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) && cwd != "" {
		// Not filepath.Join: it would clean "link/.." away before the link
		// is resolved.
		p = cwd + string(filepath.Separator) + p
	}
	if filepath.IsAbs(p) {
		p = n.resolvePhysical(p)
	} else {
		p = n.resolveExisting(filepath.Clean(p))
	}
	if n.fold {
		p = strings.ToLower(p)
	}
	return p
}

// resolvePhysical resolves an absolute path the way the system walks it:
// the links of each existing component before the ".." after it, so that
// link/../x is x next to the folder the link points to, not next to the
// link. What does not exist yet (the file a write creates) is kept as
// written.
func (n pathNormalizer) resolvePhysical(p string) string {
	if r, err := n.resolve(p); err == nil {
		return filepath.Clean(r)
	}
	vol := filepath.VolumeName(p)
	cur := vol + string(filepath.Separator)
	missing := false
	for _, c := range strings.FieldsFunc(p[len(vol):], func(r rune) bool { return r < 0x80 && os.IsPathSeparator(uint8(r)) }) {
		switch c {
		case ".":
			continue
		case "..":
			// cur has no link left in it: its parent is the lexical one.
			cur, missing = filepath.Dir(cur), false
			continue
		}
		next := filepath.Join(cur, c)
		if missing {
			cur = next
			continue
		}
		r, err := n.resolve(next)
		switch {
		case err == nil:
			cur = filepath.Clean(r)
		case errors.Is(err, fs.ErrNotExist):
			cur, missing = next, true
		default:
			cur = next
		}
	}
	return cur
}

// resolveExisting resolves the links of the longest existing prefix: the
// file a write creates does not exist yet, its folder may be a link.
func (n pathNormalizer) resolveExisting(p string) string {
	r, err := n.resolve(p)
	if err == nil {
		return filepath.Clean(r)
	}
	parent := filepath.Dir(p)
	if parent == p || !errors.Is(err, fs.ErrNotExist) {
		return p
	}
	return filepath.Join(n.resolveExisting(parent), filepath.Base(p))
}

func (n pathNormalizer) all(cwd string, ps ...string) []string {
	var out []string
	for _, p := range ps {
		if q := n.normalize(p, cwd); q != "" {
			out = append(out, q)
		}
	}
	return out
}
