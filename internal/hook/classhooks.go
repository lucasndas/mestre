package hook

import (
	"context"
	"fmt"
	"path"
	"runtime"
	"strings"

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
	env.Exceptions, env.Edits = nil, nil
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

// packageScript finds a script of the package in a command line: a word
// under hooks/ ("./hooks/checar.sh", "sh hooks\checar.sh"), the first one as
// `class check` reads it (classes.scriptOf) or any other. Run from the
// worktree it would be the copy the agent can write, not the approved one,
// so until the engine runs the approved copy
// (docs/classes/classes.md#hooks-extras-da-classe) a hook with a script does
// not run: a blocking one denies.
func packageScript(line string) (string, bool) {
	for _, f := range strings.Fields(line) {
		word := strings.ReplaceAll(strings.Trim(f, `"'`), `\`, "/")
		for strings.HasPrefix(word, "./") {
			word = strings.TrimPrefix(word, "./")
		}
		if strings.HasPrefix(strings.ToLower(word), classes.HooksDir+"/") {
			return path.Clean(word), true
		}
	}
	return "", false
}

// refusal is why the pipeline does not let the character run the line: ""
// when it does.
func (sel selected) refusal(ctx context.Context, ev providers.Event, line string) string {
	if script, ok := packageScript(line); ok {
		return fmt.Sprintf("o script %s do pacote só roda da cópia aprovada da classe, que o motor ainda não materializa (M4): o do worktree o agente pode trocar", script)
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
