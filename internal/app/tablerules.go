package app

import (
	"context"
	"errors"

	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/tablerules"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
)

// tempRulesRef is the temporary table rules seen by the chain, which is made
// before the quests and the tables they depend on.
type tempRulesRef struct{ p **tablerules.Service }

func (r tempRulesRef) Create(ctx context.Context, t chain.TempRule) error {
	if *r.p == nil {
		return errors.New("the temporary table rules are not built")
	}
	return (*r.p).Create(ctx, t)
}

// ruleMissions is the missions of the temporary table rules: the quest a
// character works on, and whether a quest ended.
type ruleMissions struct{ s *quests.Service }

func (m ruleMissions) Active(ctx context.Context, character string) (string, error) {
	st, ok, err := m.s.Store().ActiveOf(ctx, character)
	if err != nil || !ok {
		return "", err
	}
	return st.Quest, nil
}

func (m ruleMissions) Over(ctx context.Context, quest string) (bool, error) {
	q, err := m.s.Store().Quest(ctx, quest)
	if errors.Is(err, quests.ErrNoQuest) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return q.State == quests.Completed || q.State == quests.Cancelled, nil
}

// ruleTables says a table is over when it is closing or closed, or being
// cancelled or cancelled.
type ruleTables struct{ s *tables.Service }

func (t ruleTables) Over(ctx context.Context, workspace, table string) (bool, error) {
	info, err := t.s.Table(ctx, workspace, table)
	if err != nil {
		return false, err
	}
	switch info.State {
	case tables.StateClosing, tables.StateClosed, tables.StateCancelling, tables.StateCancelled:
		return true, nil
	}
	return false, nil
}

// newTableRules makes the temporary table rules over the pieces of the chain.
func newTableRules(o OrchestrationOptions, chars *characters.Service, reg characters.Registry, ib chain.Inbox, policy chain.Policy, qs *quests.Service, tbl *tables.Service) (*tablerules.Service, error) {
	return tablerules.NewService(tablerules.Options{Store: &tablerules.Store{DB: o.Guild}, Clock: o.Clock, Characters: chain.CharactersFrom(chars, reg, o.Clock),
		Policy: policy, Classes: o.ChainClasses, Supervision: o.ChainSupervision, Missions: ruleMissions{qs}, Tables: ruleTables{tbl}, Inbox: ib, Logger: o.Logger})
}
