package classes

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/mestre-ia/mestre/internal/providers"
)

// HookEvents are the events of the Mestre a class hooks into, never the
// native events of a provider (docs/provedores/adaptador.md#mapa-de-eventos).
var HookEvents = []providers.EventKind{
	providers.EventSessionStart, providers.EventTurnStart, providers.EventPreTool, providers.EventPostTool,
	providers.EventTurnEnd, providers.EventTurnFailure, providers.EventPreCompact, providers.EventSessionEnd,
}

// HookMode is how a hook runs: inside the call of `mestre hook` or later, in
// the engine.
type HookMode string

// The modes.
const (
	ModeSync  HookMode = "sync"
	ModeAsync HookMode = "async"
)

// HookOutput is where the output of a hook goes.
type HookOutput string

// The outputs: the log of the character, the scroll of the quest, or the
// character's inbox.
const (
	OutputLog    HookOutput = "log"
	OutputScroll HookOutput = "scroll"
	OutputAgent  HookOutput = "agent"
)

// MaxHookTimeout is the longest time limit a hook may declare, in seconds.
const MaxHookTimeout = 3600

// ProviderMargin is how much below the provider's own time limit a
// synchronous hook is cut (docs/classes/classes.md#hooks-extras-da-classe).
const ProviderMargin = 2 // seconds

// HookWhen narrows a hook of a tool event to some tool calls.
type HookWhen struct {
	Kind  []providers.ToolKind `yaml:"kind"`
	Paths []string             `yaml:"paths"`
}

// HookDef is one extra hook of a class.
type HookDef struct {
	Event      providers.EventKind `yaml:"-"`
	Name       string              `yaml:"name"`
	When       *HookWhen           `yaml:"when"`
	Run        string              `yaml:"run"`
	RunWindows string              `yaml:"run_windows"`
	Mode       HookMode            `yaml:"mode"`
	// Timeout is in seconds and required.
	Timeout  int        `yaml:"timeout"`
	Output   HookOutput `yaml:"output"`
	Blocking bool       `yaml:"blocking"`
}

// Command is the line to run on goos: run_windows on Windows when there is
// one.
func (h HookDef) Command(goos string) string {
	if goos == "windows" && h.RunWindows != "" {
		return h.RunWindows
	}
	return h.Run
}

// Hooks are the extra hooks of a class, by event of the Mestre, in the order
// the file lists them.
type Hooks map[providers.EventKind][]HookDef

// Matches reports whether the hook applies to this event: a tool event with
// `when` needs the kind and a path to match (paths relative to the worktree
// when the worktree is known).
func (h HookDef) Matches(ev providers.Event, worktree string) bool {
	if h.When == nil {
		return true
	}
	if ev.Tool == nil {
		return false
	}
	if len(h.When.Kind) > 0 && !slices.Contains(h.When.Kind, ev.Tool.Kind) {
		return false
	}
	if len(h.When.Paths) == 0 {
		return true
	}
	for _, p := range ev.Tool.Paths {
		rel := relativeTo(worktree, p)
		for _, g := range h.When.Paths {
			if matchGlob(g, rel) {
				return true
			}
		}
	}
	return false
}

// relativeTo is p relative to the worktree in slash form; a path outside it
// stays as it is (and then matches no relative glob).
func relativeTo(worktree, p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	w := strings.TrimRight(strings.ReplaceAll(worktree, `\`, "/"), "/")
	if w != "" && strings.EqualFold(p, w) {
		return ""
	}
	if w != "" && len(p) > len(w) && strings.EqualFold(p[:len(w)], w) && p[len(w)] == '/' {
		return p[len(w)+1:]
	}
	return p
}

var hookNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

var toolKinds = []providers.ToolKind{
	providers.KindShell, providers.KindRead, providers.KindWrite, providers.KindEdit, providers.KindSearch,
	providers.KindWeb, providers.KindSubagent, providers.KindMCP, providers.KindInternal, providers.KindOther,
}

// parseHooks reads hooks/hooks.yaml strictly and checks every hook: the
// event is one of the Mestre, the time limit is there, blocking only on a
// synchronous pre_tool, and the scripts of a hook exist in the package
// (a table class has none). The problems are all returned.
func parseHooks(data []byte, scope ClassScope, files Files) (Hooks, []Problem, []Problem) {
	var probs, warns []Problem
	bad := func(where, format string, a ...any) { probs = append(probs, Problem{where, fmt.Sprintf(format, a...)}) }
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	raw := map[string][]HookDef{}
	if err := dec.Decode(&raw); err != nil && !errors.Is(err, io.EOF) {
		return nil, []Problem{{HooksFile, err.Error()}}, nil
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, []Problem{{HooksFile, "mais de um documento YAML"}}, nil
	}
	out := Hooks{}
	names := map[string]bool{}
	referenced := map[string]bool{}
	for _, key := range sortedKeys(raw) {
		ev := providers.EventKind(key)
		if !slices.Contains(HookEvents, ev) {
			bad(HooksFile, "evento %q desconhecido; os eventos do Mestre são %s", key, eventList())
			continue
		}
		for i, h := range raw[key] {
			where := fmt.Sprintf("%s %s[%d]", HooksFile, key, i)
			h.Event = ev
			switch {
			case !hookNameRe.MatchString(h.Name):
				bad(where, "`name` é obrigatório e usa só `[a-z0-9_-]`")
			case names[h.Name]:
				bad(where, "o nome %q já é de outro hook da classe", h.Name)
			}
			names[h.Name] = true
			if h.Name != "" {
				where = fmt.Sprintf("%s %s %q", HooksFile, key, h.Name)
			}
			if strings.TrimSpace(h.Run) == "" {
				bad(where, "`run` é obrigatório")
			}
			for field, line := range map[string]string{"run": h.Run, "run_windows": h.RunWindows} {
				if strings.ContainsAny(line, "\x00\r\n") {
					bad(where, "`%s` é uma única linha de comando", field)
				}
				if len(line) > 4096 {
					bad(where, "`%s` passa de 4096 caracteres", field)
				}
			}
			switch h.Mode {
			case "":
				h.Mode = ModeSync
			case ModeSync, ModeAsync:
			default:
				bad(where, "`mode` aceita `sync` ou `async`, recebeu %q", h.Mode)
			}
			switch h.Output {
			case "":
				h.Output = OutputLog
			case OutputLog, OutputScroll, OutputAgent:
			default:
				bad(where, "`output` aceita `log`, `scroll` ou `agent`, recebeu %q", h.Output)
			}
			if h.Timeout <= 0 || h.Timeout > MaxHookTimeout {
				bad(where, "`timeout` (segundos) é obrigatório, de 1 a %d", MaxHookTimeout)
			}
			if h.Blocking && ev != providers.EventPreTool {
				bad(where, "`blocking` só existe em `pre_tool`: é o hook que existe para bloquear")
			}
			if ev == providers.EventPreTool && h.Mode == ModeAsync {
				bad(where, "um hook `pre_tool` é `sync`: só decide se rodar antes da ferramenta")
			}
			if h.Blocking && h.Mode == ModeAsync {
				bad(where, "`blocking` pede `mode: sync`")
			}
			if h.Mode == ModeSync && (h.Output == OutputScroll || h.Output == OutputAgent) {
				warns = append(warns, Problem{where, fmt.Sprintf("`output: %s` só vale em `mode: async`: num hook sync a saída serve de motivo do exit 2 e mais nada", h.Output)})
			}
			if h.When != nil {
				probs = append(probs, checkWhen(where, ev, h.When)...)
			}
			for _, line := range []string{h.Run, h.RunWindows} {
				script, ok := scriptOf(line)
				if !ok {
					continue
				}
				referenced[script] = true
				if scope == ClassTable {
					bad(where, "o script %q não pode existir numa classe da mesa: só linhas de comando em `run`; scripts ficam para classes globais e da campanha, que passam por aprovação", script)
				} else if _, found := files.Get(script); !found {
					bad(where, "o script %q não está no pacote", script)
				}
			}
			out[ev] = append(out[ev], h)
		}
	}
	if scope == ClassTable {
		for _, f := range files.Under(HooksDir) {
			if f.Path != HooksFile && !referenced[f.Path] {
				bad(f.Path, "classes da mesa não podem ter scripts no pacote: só linhas de comando em `run`; scripts ficam para classes globais e da campanha, que passam por aprovação")
			}
		}
	}
	return out, probs, warns
}

// UnusedScripts are the files of hooks/ that no hook runs.
func unusedScripts(files Files, hooks Hooks) []string {
	used := map[string]bool{}
	for _, hs := range hooks {
		for _, h := range hs {
			for _, line := range []string{h.Run, h.RunWindows} {
				if s, ok := scriptOf(line); ok {
					used[s] = true
				}
			}
		}
	}
	var out []string
	for _, f := range files.Under(HooksDir) {
		if f.Path != HooksFile && !used[f.Path] {
			out = append(out, f.Path)
		}
	}
	return out
}

func checkWhen(where string, ev providers.EventKind, w *HookWhen) []Problem {
	var probs []Problem
	bad := func(format string, a ...any) { probs = append(probs, Problem{where, fmt.Sprintf(format, a...)}) }
	if ev != providers.EventPreTool && ev != providers.EventPostTool {
		bad("`when` só vale em `pre_tool` e `post_tool`, os eventos de ferramenta")
		return probs
	}
	for _, k := range w.Kind {
		if !slices.Contains(toolKinds, k) {
			bad("`when.kind` %q desconhecido; os tipos são %s", k, kindList())
		}
	}
	for _, g := range w.Paths {
		if err := checkFileGlob(g, false); err != nil {
			bad("`when.paths` %q: %v", g, err)
		}
	}
	return probs
}

// scriptOf finds the script of a command line: a first word that is a file
// of hooks/ ("./hooks/checar.sh", "hooks/checar.sh", with / or \), as the
// path inside the package.
func scriptOf(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", false
	}
	word := strings.Fields(line)[0]
	word = strings.Trim(word, `"'`)
	word = strings.ReplaceAll(word, `\`, "/")
	word = strings.TrimPrefix(word, "./")
	if !strings.HasPrefix(word, HooksDir+"/") {
		return "", false
	}
	return path.Clean(word), true
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func eventList() string {
	names := make([]string, len(HookEvents))
	for i, e := range HookEvents {
		names[i] = "`" + string(e) + "`"
	}
	return strings.Join(names, ", ")
}

func kindList() string {
	names := make([]string, len(toolKinds))
	for i, k := range toolKinds {
		names[i] = "`" + string(k) + "`"
	}
	return strings.Join(names, ", ")
}

// ProviderLacks says whether an event of the Mestre never reaches a
// provider's hooks (docs/provedores/adaptador.md#mapa-de-eventos). Of the
// events a class can hook into, only the Antigravity lacks one: pre_compact
// (its session_end is derived by the engine from the end of the process).
func ProviderLacks(p providers.ID, ev providers.EventKind) bool {
	return p == providers.Antigravity && ev == providers.EventPreCompact
}
