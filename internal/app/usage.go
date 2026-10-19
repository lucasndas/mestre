package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/usage"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/providers"
)

// usageReason is why the limits of use pause what they pause: only a pause with
// this reason is lifted by the approval of the extra.
const usageReason = "limite de uso"

// usageSink gives the owner of the type custo_extra in the chain (the limits of
// use) what became of a request.
type usageSink struct{ p **usage.Service }

func (s usageSink) Decided(ctx context.Context, r chain.Result) error {
	if *s.p == nil {
		return nil
	}
	return (*s.p).Decided(ctx, usage.Decision{Request: r.Request.ID, Approved: r.Approved, Reason: r.Reason})
}

// usageExtras opens the custo_extra request of a limit in the chain.
type usageExtras struct{ p **chain.Service }

func (e usageExtras) OpenExtra(ctx context.Context, r usage.ExtraRequest) (int64, error) {
	if *e.p == nil {
		return 0, errors.New("app: the chain of approval is not built")
	}
	return (*e.p).Request(ctx, chain.OpenRequest{Type: classes.RequestExtraCost, Workspace: r.Workspace, Table: r.Table, About: r.About, Risk: r.Risk, Target: r.Target})
}

// usageHolds holds, for the inbox, the characters whose provider window is at
// the start threshold.
type usageHolds struct {
	p    **usage.Service
	then inbox.Holds
}

func (h usageHolds) Held(ctx context.Context, id string) (bool, error) {
	if h.then != nil {
		if held, err := h.then.Held(ctx, id); err != nil || held {
			return held, err
		}
	}
	if *h.p == nil {
		return false, nil
	}
	return (*h.p).Held(ctx, id)
}

// usageDirectory tells the limits of use who a character is, from the registry,
// the sheets and the quests.
type usageDirectory struct {
	reg     characters.Registry
	presets characters.Presets
	quests  **quests.Service
}

func (d usageDirectory) Character(ctx context.Context, id string) (usage.Subject, bool, error) {
	c, err := d.reg.Get(ctx, id)
	if err != nil {
		if errors.Is(err, characters.ErrNoCharacter) {
			return usage.Subject{}, false, nil
		}
		return usage.Subject{}, false, err
	}
	s := usage.Subject{ID: c.ID, Name: c.Name, Sheet: c.Sheet, Class: c.Class, Role: c.Role, Workspace: c.Workspace, Table: c.Table, TableName: c.Table,
		Quest: c.Quest, Step: c.Step, Profile: c.Account}
	if c.Sheet != "" {
		snap, err := d.presets.Snapshot(ctx, c.Sheet)
		if err != nil {
			return usage.Subject{}, false, fmt.Errorf("the sheet %q of %s: %w", c.Sheet, c.Name, err)
		}
		s.Provider = providerOf(snap.Sheet.Agent)
	}
	if c.Quest != "" && *d.quests != nil {
		s.QuestName, _ = (*d.quests).QuestName(ctx, c.Quest)
	}
	return s, true, nil
}

// providerOf maps the agent of a sheet to the provider.
func providerOf(agent string) providers.ID {
	switch id := providers.ID(agent); id {
	case providers.ClaudeCode, providers.Codex, providers.Antigravity:
		return id
	}
	return providers.Null
}

func (d usageDirectory) Alternatives(ctx context.Context, s usage.Subject) ([]string, error) {
	sheets, err := d.presets.Sheets(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, sh := range sheets {
		if sh.Class != s.Class || sh.Name == s.Sheet {
			continue
		}
		if providerOf(sh.Agent) != s.Provider || sh.Account != s.Profile {
			out = append(out, sh.Name)
		}
	}
	return out, nil
}

func (d usageDirectory) PendingSteps(ctx context.Context, workspace, table string) (int, error) {
	if *d.quests == nil {
		return 0, nil
	}
	list, err := (*d.quests).List(ctx, workspace, table)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, q := range list {
		if q.State != quests.Running && q.State != quests.Paused {
			continue
		}
		steps, err := (*d.quests).Store().Steps(ctx, q.ID)
		if err != nil {
			return n, err
		}
		for _, st := range steps {
			if st.State == quests.StepPending || st.State == quests.StepPreparing {
				n++
			}
		}
	}
	return n, nil
}

// usageCeilings gives the cost ceiling of a workspace from its library of classes.
type usageCeilings struct {
	libs func(workspace string) *classes.Library
}

func (c usageCeilings) Ceiling(ctx context.Context, workspace string) (*classes.Ceiling, error) {
	if c.libs != nil {
		if lib := c.libs(workspace); lib != nil && lib.Ceiling != nil {
			return lib.Ceiling.Current(ctx)
		}
	}
	return classes.DefaultCeiling(), nil
}

// usagePauser pauses the quests, the tables and the workspaces the limits cover.
type usagePauser struct {
	quests **quests.Service
	tables **tables.Service
	ws     *workspaces.Service
}

func (p usagePauser) PauseQuest(ctx context.Context, id, reason string) error {
	_, err := (*p.quests).PauseFor(ctx, id, reason)
	return err
}

func (p usagePauser) ResumeQuest(ctx context.Context, id string) error {
	q, err := (*p.quests).Store().Quest(ctx, id)
	if err != nil {
		return err
	}
	if q.State != quests.Paused || q.PausedReason != usageReason {
		return nil // someone else paused it, or it was resumed: not ours to lift
	}
	_, err = (*p.quests).Resume(ctx, id)
	return err
}

func (p usagePauser) PauseTable(ctx context.Context, workspace, table, reason string) error {
	return (*p.tables).Pause(ctx, workspace, table, tables.PauseRequest{Reason: reason, By: "usage_limit"})
}

func (p usagePauser) ResumeTable(ctx context.Context, workspace, table string) error {
	t, err := (*p.tables).Get(ctx, workspace, table)
	if err != nil {
		return err
	}
	if !t.Paused || t.PausedBy != "usage_limit" {
		return nil
	}
	return (*p.tables).Resume(ctx, workspace, table)
}

func (p usagePauser) PauseWorkspace(ctx context.Context, workspace string) (bool, error) {
	w, err := p.ws.Get(ctx, workspace)
	if err != nil {
		return false, err
	}
	if w.State != workspaces.StateActive {
		return false, nil
	}
	return true, p.ws.Pause(ctx, workspace)
}

func (p usagePauser) ResumeWorkspace(ctx context.Context, workspace string) error {
	w, err := p.ws.Get(ctx, workspace)
	if err != nil {
		return err
	}
	if w.State != workspaces.StatePaused {
		return nil
	}
	return p.ws.Resume(ctx, workspace)
}

func (p usagePauser) Workspaces(ctx context.Context) ([]string, error) {
	all, err := p.ws.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(all))
	for _, w := range all {
		out = append(out, w.Slug)
	}
	return out, nil
}

// usageCharacters changes the state of a character the provider cut.
type usageCharacters struct {
	svc **characters.Service
	reg characters.Registry
	ib  **inbox.Service
}

func (c usageCharacters) Block(ctx context.Context, id string) error {
	ch, err := c.reg.Get(ctx, id)
	if err != nil {
		return err
	}
	switch ch.State {
	case characters.Busy, characters.Idle, characters.Waiting:
		return (*c.svc).Report(ctx, characters.Report{Character: id, State: characters.Blocked, Reason: characters.ReasonQuota})
	}
	return nil // blocked already, or not running: nothing to block
}

func (c usageCharacters) Unblock(ctx context.Context, id string) error {
	ch, err := c.reg.Get(ctx, id)
	if err != nil {
		return err
	}
	if ch.State != characters.Blocked || ch.Reason != characters.ReasonQuota {
		return nil // it moved on by itself: a turn started, or it ended
	}
	if err := (*c.svc).Report(ctx, characters.Report{Character: id, State: characters.Idle, Reason: "quota is back"}); err != nil {
		return err
	}
	return (*c.ib).PumpAll(ctx)
}

// quotaPump delivers what waited for a window to be renewed.
type quotaPump struct{ ib **inbox.Service }

func (p quotaPump) PumpAll(ctx context.Context) error { return (*p.ib).PumpAll(ctx) }
