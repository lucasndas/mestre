package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/sessionzero"
)

// The hooks of the classes on a Tower (docs/classes/classes.md#hooks-extras-da-classe). The
// Tower has no library of classes: the Citadel sends the hooks of the approved class with the
// effective rules (classes.Rules.ClassHooks, with the hash of the package), and the machine
// reads them from the rules it keeps. The synchronous hooks answer in the `mestre hook` of
// the Tower, through the same engine; the asynchronous ones run in the terminal of the
// character here (Runtime.HookExec), and what they write for the scroll of the quest or the
// inbox of the character goes to the Citadel as hook.output.

// newTowerAsyncHooks is the runner of the asynchronous hooks of the characters of this Tower,
// over the rules the Citadel sent.
func (e *Engine) newTowerAsyncHooks(rules *classes.Store) *hook.AsyncRunner {
	src := &hook.ClassHookSource{FromRules: true, Rules: rules, Characters: e.deps.Characters}
	return &hook.AsyncRunner{Source: src, Exec: terminalHookExec{e}, Out: towerHookOutputs{e: e}, Clock: e.clk, Logger: e.log}
}

// errHooksNotReceived is why a Tower does not start a character whose class has hooks: it
// does not have them, and a character never runs without the hooks of its class (fail closed).
var errHooksNotReceived = errors.New("the class of the character has hooks and this machine did not receive them with the effective rules: the character is not started without them")

// towerClassHooksReady checks, before the terminal is made, that the Tower holds the hooks of
// the class of the character that starts: the effective rules the Citadel sent before the
// start carry them, from the same package (the same hash) as the class of the request.
func (e *Engine) towerClassHooksReady(ctx context.Context, req sessionzero.CharacterRequest) error {
	if req.Class == nil || len(req.Class.Hooks) == 0 {
		return nil
	}
	if e.mdb == nil {
		return errHooksNotReceived
	}
	rules, err := (&classes.Store{DB: e.mdb, Clock: e.clk, Agents: e.paths.Agentes}).Rules(ctx, req.ID)
	if err != nil || rules.ClassHooks == nil || len(rules.ClassHooks.Hooks) == 0 {
		return errHooksNotReceived
	}
	if req.Class.Hash != "" && rules.ClassHooks.Hash != req.Class.Hash {
		return fmt.Errorf("%w (the package of the hooks is not the one of the class)", errHooksNotReceived)
	}
	return nil
}

// towerHookOutputs is where the asynchronous hooks of a Tower write: the log is the Tower's,
// the scroll and the inbox are the Citadel's.
type towerHookOutputs struct{ e *Engine }

var _ hook.HookOutputs = towerHookOutputs{}

func (o towerHookOutputs) Log(_ context.Context, r hook.AsyncResult) {
	level := slog.LevelInfo
	if r.Err != nil || r.Exit != 0 {
		level = slog.LevelWarn
	}
	o.e.log.Log(context.Background(), level, "app: output of an asynchronous hook of a class", "agent", r.AgentID, "hook", r.Hook, "exit", r.Exit, "err", r.Err)
}

func (o towerHookOutputs) Scroll(ctx context.Context, r hook.AsyncResult) error {
	return o.send(ctx, protocol.HookOutputScroll, r)
}

func (o towerHookOutputs) Agent(ctx context.Context, r hook.AsyncResult) error {
	return o.send(ctx, protocol.HookOutputAgent, r)
}

func (o towerHookOutputs) send(ctx context.Context, kind string, r hook.AsyncResult) error {
	link := o.e.towerLink()
	if link == nil {
		return errors.New("this Tower has no connection to the Citadel")
	}
	msg := protocol.HookOutput{Kind: kind, AgentID: r.AgentID, TableID: r.TableID, TurnID: r.TurnID, Event: string(r.Event), Hook: r.Hook, Output: o.mask(r.Output), Exit: r.Exit}
	if r.Err != nil {
		msg.Error = o.mask(r.Err.Error())
	}
	if len(msg.Output) > protocol.MaxHookOutput {
		msg.Output = msg.Output[:protocol.MaxHookOutput]
	}
	// the message waits in the outbox while the Citadel is away: the queue of the hooks of the
	// character does not wait with it
	go func() {
		if _, err := link.Send(context.WithoutCancel(ctx), protocol.Outgoing{Type: protocol.TypeHookOutput, Payload: msg}); err != nil {
			o.e.log.Warn("app: the output of a hook was not queued for the Citadel", "agent", r.AgentID, "hook", r.Hook, "err", err)
		}
	}()
	return nil
}

// mask hides the secrets of what leaves the machine.
func (o towerHookOutputs) mask(s string) string {
	if o.e.deps.Masker != nil {
		return o.e.deps.Masker.Mask(s)
	}
	return s
}

// handleHookOutput is the handler of hook.output on the Citadel: the output of an
// asynchronous hook of a character of a Tower, from the machine that sent it. The character
// must run there, or any Tower could write in the scroll or the inbox of another's.
func (e *Engine) handleHookOutput(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
	p, ok := m.Payload.(*protocol.HookOutput)
	if !ok {
		return nil, protocol.NewError(protocol.CodeInvalidPayload, "hook.output: %T", m.Payload)
	}
	period := e.period.Load()
	if period == nil || period.hookOut == nil {
		return nil, protocol.NewError(protocol.CodeUnavailable, "hook.output: this Citadel keeps no outputs of hooks")
	}
	if err := e.runsOn(ctx, p.AgentID, m.Peer.ID); err != nil {
		return nil, protocol.NewError(protocol.CodeForbidden, "hook.output: %v", err)
	}
	r := hook.AsyncResult{AgentID: p.AgentID, TableID: p.TableID, TurnID: p.TurnID, Event: providers.EventKind(p.Event), Hook: p.Hook, Output: p.Output, Exit: p.Exit}
	if p.Error != "" {
		r.Err = errors.New(p.Error)
	}
	var err error
	switch p.Kind {
	case protocol.HookOutputScroll:
		err = period.hookOut.Scroll(ctx, r)
	case protocol.HookOutputAgent:
		err = period.hookOut.Agent(ctx, r)
	}
	if err != nil {
		// the output is of no use later: the Citadel says no and the Tower logs it
		return nil, protocol.NewError(protocol.CodeUnavailable, "hook.output: %v", err)
	}
	return nil, nil
}
