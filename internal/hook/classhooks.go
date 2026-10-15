package hook

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"unicode"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/providers"
)

// ClassSource gives the class of a character: Library (classes.Library). Use
// is the version in use at the start of a turn, and looks at the files on
// disk for a change made outside the Mestre; Approved is the same version
// without looking at the disk, for the other events.
type ClassSource interface {
	Use(ctx context.Context, name, table string) (*classes.Class, error)
	Approved(ctx context.Context, name, table string) (*classes.Class, error)
}

// ClassHookSource gives the dispatcher the hooks of the class of the
// character (it is the ClassHooks of the engine) and the engine the
// asynchronous ones. The class is the one the effective rules of the
// character name, always its approved version.
type ClassHookSource struct {
	Classes ClassSource
	// Rules are the effective rules of the characters: the class of the
	// character and what its `run` lines are judged by.
	Rules classes.RulesSource
	// Characters gives the worktree and the system of the character.
	Characters Characters
	// ProviderSeconds is the time limit the hook files give `mestre hook` for
	// a provider; nil is classes.DefaultProviderHookSeconds for all.
	ProviderSeconds func(providers.ID) int
	// EventSeconds is the same limit for one event, where the provider gives
	// some events less than the rest (Codex gives SessionEnd and Interrupt
	// 3 s, not 30); a result of zero or less falls back to ProviderSeconds.
	// The adapters do not expose their per-event limits yet: the wiring of
	// M4 gives them here, from the same table that writes the hook files.
	EventSeconds func(providers.ID, providers.EventKind) int
}

var _ ClassHooks = (*ClassHookSource)(nil)

// AsyncHook is an asynchronous hook of the class for one event, ready to
// run: the command for this system, the folder and the declared time limit.
type AsyncHook struct {
	Name    string
	Run     string
	Dir     string
	Timeout int
	Output  classes.HookOutput
}

// selected are the hooks of the class that apply to the event, with the
// context the commands run in.
type selected struct {
	class *classes.Class
	rules *classes.Rules
	env   classes.Env
	hooks []classes.HookDef
}

func (s *ClassHookSource) pick(ctx context.Context, ev providers.Event, mode classes.HookMode) (selected, error) {
	var sel selected
	if s.Classes == nil || s.Rules == nil || s.Characters == nil || ev.AgentID == "" {
		return sel, nil
	}
	rules, err := s.Rules.Rules(ctx, ev.AgentID)
	if err != nil {
		return sel, err
	}
	if rules.Class == "" {
		return sel, nil // a character with no class (the Grand Master, a Master of a table)
	}
	// A class changed outside the Mestre is noticed when the character
	// starts a turn (or a session); the other events do not read the disk.
	get := s.Classes.Approved
	if ev.Event == providers.EventTurnStart || ev.Event == providers.EventSessionStart {
		get = s.Classes.Use
	}
	env, err := s.Characters.Env(ctx, ev.AgentID)
	if err != nil {
		return sel, err
	}
	// The table of the class is the registry's, not the one the event
	// claims (the MESTRE_MESA of the caller).
	class, err := get(ctx, rules.Class, env.TableID)
	if err != nil {
		return sel, err
	}
	// The `run` of a hook is judged like a command of the character, but
	// judging it never uses up an exception.
	env.Exceptions, env.Edits, env.TableRules = nil, nil, nil
	sel = selected{class: class, rules: rules, env: env}
	for _, h := range class.Hooks[ev.Event] {
		if h.Mode == mode && h.Matches(ev, env.Worktree) {
			sel.hooks = append(sel.hooks, h)
		}
	}
	return sel, nil
}

var _ BlockingSource = (*ClassHookSource)(nil)

// HasBlocking implements BlockingSource: whether the approved class of the
// character has a blocking pre_tool hook, whatever its `when`.
func (s *ClassHookSource) HasBlocking(ctx context.Context, agentID string) (bool, error) {
	if s.Classes == nil || s.Rules == nil || s.Characters == nil {
		return false, nil
	}
	rules, err := s.Rules.Rules(ctx, agentID)
	if err != nil {
		return true, err
	}
	if rules.Class == "" {
		return false, nil
	}
	env, err := s.Characters.Env(ctx, agentID)
	if err != nil {
		return true, err
	}
	class, err := s.Classes.Approved(ctx, rules.Class, env.TableID)
	if err != nil {
		return true, err
	}
	for _, h := range class.Hooks[providers.EventPreTool] {
		if h.Blocking {
			return true, nil
		}
	}
	return false, nil
}

func (sel selected) goos() string {
	if sel.env.OS != "" {
		return sel.env.OS
	}
	return runtime.GOOS
}

// Sync implements ClassHooks: the synchronous hooks of the class that apply
// to the event, in the order of the file. A hook whose `run` the permission
// pipeline refuses is sent as refused; a blocking one then denies, like a
// failure.
func (s *ClassHookSource) Sync(ctx context.Context, ev providers.Event) ([]ClassHook, error) {
	sel, err := s.pick(ctx, ev, classes.ModeSync)
	if err != nil || len(sel.hooks) == 0 {
		return nil, err
	}
	limit := classes.DefaultProviderHookSeconds
	if s.ProviderSeconds != nil {
		limit = s.ProviderSeconds(ev.Provider)
	}
	if s.EventSeconds != nil {
		if n := s.EventSeconds(ev.Provider, ev.Event); n > 0 {
			limit = n
		}
	}
	// One budget for all the hooks of the call: the provider gives up on
	// `mestre hook` at its limit (and Codex then goes on as if allowed).
	budget := classes.SyncTimeout(classes.MaxHookTimeout, limit).Milliseconds()
	out := make([]ClassHook, 0, len(sel.hooks))
	for _, h := range sel.hooks {
		run := h.Command(sel.goos())
		ch := ClassHook{
			Name: h.Name, Run: run, Dir: sel.env.Worktree, Blocking: h.Blocking,
			TimeoutMS: classes.SyncTimeout(h.Timeout, limit).Milliseconds(), BudgetMS: budget,
		}
		ch.Refused = sel.refusal(ctx, ev, run)
		out = append(out, ch)
	}
	return out, nil
}

// Async gives the asynchronous hooks of the class for the event, with the
// time limit the class declared. One whose `run` the pipeline refuses is not
// given (the refusal is the first element of the second result).
func (s *ClassHookSource) Async(ctx context.Context, ev providers.Event) (run []AsyncHook, refused []string, err error) {
	sel, err := s.pick(ctx, ev, classes.ModeAsync)
	if err != nil {
		return nil, nil, err
	}
	for _, h := range sel.hooks {
		cmd := h.Command(sel.goos())
		if why := sel.refusal(ctx, ev, cmd); why != "" {
			refused = append(refused, fmt.Sprintf("%s: %s", h.Name, why))
			continue
		}
		run = append(run, AsyncHook{Name: h.Name, Run: cmd, Dir: sel.env.Worktree, Timeout: h.Timeout, Output: h.Output})
	}
	return run, refused, nil
}

// unquote removes what a shell drops from a word before it looks for a file:
// quotes, backslashes, backticks (the PowerShell escape) and the caret of
// cmd. `h""ooks`, `h\ooks` and `h^ooks` all name hooks.
var unquote = strings.NewReplacer(`"`, "", `'`, "", `\`, "", "`", "", "^", "")

// dynamicChars are what a shell expands into a name before it opens the
// file: variables and substitutions (`$`, `%`, `!`), the home (`~`), globs
// and brace lists (`*?[]{}`).
const dynamicChars = "$%!~*?[]{}`"

// dirChangers are the words that change the folder the rest of the line
// runs in: `cd hooks && ./x` has no path with hooks/ in one word.
var dirChangers = map[string]bool{"cd": true, "chdir": true, "pushd": true, "popd": true, "sl": true, "set-location": true}

// dirChangeFlags are the options that do the same (`make -C hooks`).
var dirChangeFlags = map[string]bool{"-C": true, "--directory": true, "--chdir": true, "--cwd": true}

// packageScript says why a command line may run a script of the package
// (hooks/…), or "" when it cannot. Run from the worktree it would be the
// copy the agent can write, not the approved one, so until the engine runs
// the approved copy (docs/classes/classes.md#hooks-extras-da-classe) a hook
// that can reach one does not run: a blocking one denies.
//
// The line is not parsed as a shell would (that is the permission
// pipeline's job, which does not know the folder is the package's): the rule
// is conservative, refusing every line that
//   - mentions hooks in any form once quotes, backslashes, backticks and
//     carets are removed, in any case: `./hooks/x`, `sh hooks\x`,
//     `cd hooks && ./x`, `true;hooks/x`, `(hooks/x)`, `$PWD/hooks/x`,
//     `.//hooks/x`, `/the/worktree/hooks/x`, `h""ooks/x`; or
//   - holds a word that is built at run time (`$x`, `%x%`, `~`, a glob, a
//     brace list or a substitution) and is a path, or sits in a line that
//     changes folder: `$d/x`, `?ooks/x`, `{h,}ooks/x`, `$(echo ho)oks/x`,
//     `cd h$e*`.
//
// The cost is a hook of a class that names a path like that, which the
// `class check` of M4 will make rare; the benefit is that no spelling of the
// folder slips through.
func packageScript(line string) string {
	if strings.Contains(strings.ToLower(unquote.Replace(line)), classes.HooksDir) {
		return "a linha cita " + classes.HooksDir + "/ (em qualquer forma)"
	}
	words := strings.Fields(line)
	changesDir := false
	for _, w := range words {
		if dirChangeFlags[w] {
			changesDir = true
		}
	}
	for _, t := range strings.FieldsFunc(strings.ToLower(line), func(r rune) bool { return !unicode.IsLetter(r) && r != '-' }) {
		if dirChangers[t] {
			changesDir = true
		}
	}
	// A substitution can end in the middle of a word ($(echo ho)oks/x), so
	// the words around it count too.
	substitutes := strings.Contains(line, "$(") || strings.Contains(line, "`") || strings.Contains(line, "<(") || strings.Contains(line, ">(")
	for _, w := range words {
		if strings.Contains(w, "://") {
			continue
		}
		isPath := strings.ContainsAny(w, `/\`)
		if !strings.ContainsAny(w, dynamicChars) && (!substitutes || !isPath) {
			continue
		}
		if changesDir || isPath {
			return "a linha monta um caminho em tempo de execução (" + w + ")"
		}
	}
	return ""
}

// refusal is why the pipeline does not let the character run the line: ""
// when it does.
func (sel selected) refusal(ctx context.Context, ev providers.Event, line string) string {
	if why := packageScript(line); why != "" {
		return "um script do pacote só roda da cópia aprovada da classe, que o motor ainda não materializa (M4), e o do worktree o agente pode trocar: " + why
	}
	d := classes.Decide(ctx, sel.env, sel.rules, providers.Event{
		Version: providers.EventVersion, Provider: ev.Provider, Event: providers.EventPreTool,
		AgentID: ev.AgentID, TableID: ev.TableID, Cwd: sel.env.Worktree, ReceivedAt: ev.ReceivedAt,
		Tool: &providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: line},
	})
	switch d.Verdict {
	case classes.Allow:
		return ""
	case classes.Ask:
		return "o `run` pediria aprovação pelas permissões da classe: " + d.Reason
	}
	return "o `run` é negado pelas permissões da classe: " + d.Reason
}
