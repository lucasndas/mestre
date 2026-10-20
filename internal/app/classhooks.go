package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/providers"
)

// The hooks of the classes in `mestre run` (docs/classes/classes.md#hooks-extras-da-classe,
// docs/desenvolvimento/ligacao-de-producao.md): the synchronous ones answer the
// dispatcher with the pre_tool and the others, the asynchronous ones run after
// the answer, in the terminal of the character and as the character
// (characters.Runtime.HookExec), and write where the class asked (the scroll
// of the quest, the inbox, the log). The class of a character is the one of
// the library of its workspace; the rules are the ones kept on this machine.

// classHooks is the hook.ClassHooks, hook.BlockingSource and hook.AsyncSource
// of the Citadel of a period over the libraries of the workspaces.
type classHooks struct {
	books func(workspace string) *classes.Library
	reg   characters.Registry
	rules classes.RulesSource
	chars func() hook.Characters
}

var (
	_ hook.ClassHooks     = (*classHooks)(nil)
	_ hook.BlockingSource = (*classHooks)(nil)
	_ hook.AsyncSource    = (*classHooks)(nil)
)

// source is the ClassHookSource of the workspace of the character; none for a
// character the registry does not have (it has no class either).
func (c *classHooks) source(ctx context.Context, agentID string) (*hook.ClassHookSource, error) {
	ch, err := c.reg.Get(ctx, agentID)
	if errors.Is(err, characters.ErrNoCharacter) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lib := c.books(ch.Workspace)
	if lib == nil {
		return nil, fmt.Errorf("the workspace %s has no library of classes", ch.Workspace)
	}
	chars := c.chars()
	if chars == nil {
		return nil, errors.New("this machine has no characters")
	}
	return &hook.ClassHookSource{Classes: lib, Rules: c.rules, Characters: chars}, nil
}

func (c *classHooks) Sync(ctx context.Context, ev providers.Event) ([]hook.ClassHook, error) {
	s, err := c.source(ctx, ev.AgentID)
	if s == nil || err != nil {
		return nil, err
	}
	return s.Sync(ctx, ev)
}

func (c *classHooks) HasBlocking(ctx context.Context, agentID string) (bool, error) {
	s, err := c.source(ctx, agentID)
	if s == nil || err != nil {
		return false, err
	}
	return s.HasBlocking(ctx, agentID)
}

func (c *classHooks) Async(ctx context.Context, ev providers.Event) ([]hook.AsyncHook, []string, error) {
	s, err := c.source(ctx, ev.AgentID)
	if s == nil || err != nil {
		return nil, nil, err
	}
	return s.Async(ctx, ev)
}

// periodClassHooks is the hook.ClassHooks of the machine: the hooks of the
// classes of the period of the Citadel, and none while there is no period (a
// Tower, or a Citadel between periods).
type periodClassHooks struct{ e *Engine }

var (
	_ hook.ClassHooks     = periodClassHooks{}
	_ hook.BlockingSource = periodClassHooks{}
)

func (h periodClassHooks) Sync(ctx context.Context, ev providers.Event) ([]hook.ClassHook, error) {
	if p := h.e.period.Load(); p != nil && p.classHooks != nil {
		return p.classHooks.Sync(ctx, ev)
	}
	return nil, nil
}

func (h periodClassHooks) HasBlocking(ctx context.Context, agentID string) (bool, error) {
	if p := h.e.period.Load(); p != nil && p.classHooks != nil {
		return p.classHooks.HasBlocking(ctx, agentID)
	}
	return false, nil
}

// terminalHookExec is the hook.HookExec of the machine: the terminal of the
// character, through the Runtime. A machine without terminals cannot run a
// hook as a character, and the hook does not run.
type terminalHookExec struct{ e *Engine }

func (x terminalHookExec) Exec(ctx context.Context, agentID string, h hook.AsyncHook, event providers.EventKind, stdin []byte) (hook.HookResult, error) {
	if x.e.rt == nil {
		return hook.HookResult{}, errTerminalsNotWired
	}
	return x.e.rt.rt.HookExec().Exec(ctx, agentID, h, event, stdin)
}

// hostClassHooks makes the hooks of the classes of a period: the source over
// the libraries and the runner of the asynchronous ones, which runs while the
// Citadel acts.
func (e *Engine) hostClassHooks(orc *Orchestration, books func(string) *classes.Library, ph *periodHooks) {
	if books == nil || e.mdb == nil {
		return
	}
	ph.classHooks = &classHooks{books: books, reg: orc.Registry, rules: &classes.Store{DB: e.mdb, Clock: e.clk, Agents: e.paths.Agentes},
		chars: func() hook.Characters { return e.deps.Characters }}
	runner := &hook.AsyncRunner{Source: ph.classHooks, Exec: terminalHookExec{e}, Out: orc.HookOutputs(), Clock: e.clk, Logger: e.log}
	ph.async = runner
	orc.Loops = append(orc.Loops, func(ctx context.Context) error {
		runner.Start(ctx)
		<-ctx.Done()
		runner.Wait()
		return nil
	})
}
