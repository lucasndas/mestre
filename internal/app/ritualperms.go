package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/rituals"
	"github.com/lucasndas/mestre/internal/providers"
)

// ritualPermissions decides the command of a pre-run like a command of the
// target's own (docs/classes/permissoes.md): the same effective rules and the
// same denials. What would ask for approval is refused, since a pre-run has
// nobody to answer.
type ritualPermissions struct{ cc *CitadelClasses }

var _ rituals.Permissions = ritualPermissions{}

// Allow implements rituals.Permissions.
func (p ritualPermissions) Allow(ctx context.Context, target characters.Character, command string) (rituals.Decision, error) {
	rules, err := p.cc.Rules(ctx, target.ID)
	if err != nil {
		return rituals.Decision{}, err
	}
	env, err := p.cc.Env(ctx, target.ID)
	if err != nil {
		return rituals.Decision{}, err
	}
	ev := providers.Event{Event: providers.EventPreTool, AgentID: target.ID, TableID: target.Table, Cwd: target.Dir,
		Tool: &providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: command}}
	d := classes.Decide(ctx, env, rules, ev)
	if d.Verdict == classes.Allow {
		return rituals.Decision{Allow: true}, nil
	}
	return rituals.Decision{Reason: d.Reason}, nil
}

// preRunGate decides the command of a pre-run on this machine with the hook
// engine of the machine: the effective rules and the environment of the
// character as they are here (links resolved, the layout and the system of this
// machine), the same decision as for a command of its own.
func (e *Engine) preRunGate(ctx context.Context, c characters.PreRunCheck) error {
	hk := e.hk.Load()
	if hk == nil {
		return errors.New("the hook engine of this machine is not up: nothing ran")
	}
	ok, why := hk.Allows(ctx, providers.Event{Event: providers.EventPreTool, AgentID: c.Agent, TableID: c.Table, Cwd: c.Dir,
		Tool: &providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: c.Command}})
	if !ok {
		return fmt.Errorf("denied by the permissions of the character on this machine: %s", why)
	}
	return nil
}
