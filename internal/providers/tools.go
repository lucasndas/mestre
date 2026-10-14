package providers

import (
	"errors"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
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
}

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
		p = filepath.Join(cwd, p)
	}
	p = n.resolveExisting(filepath.Clean(p))
	if n.fold {
		p = strings.ToLower(p)
	}
	return p
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
