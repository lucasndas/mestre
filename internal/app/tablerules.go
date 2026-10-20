package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/tablerules"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
)

// tempRulesRef is the temporary table rules seen by the chain, which is made
// before the quests and the tables they depend on.
type tempRulesRef struct {
	p   **tablerules.Service
	pub *RulesPublisher
	log *slog.Logger
}

// Create makes the rule and sends the new effective rules of the characters
// of the class. A failure to send leaves the rule made: the document the
// machine keeps only lacks the pattern, which keeps asking (the safe side),
// and the next publication carries it.
func (r tempRulesRef) Create(ctx context.Context, t chain.TempRule) error {
	if *r.p == nil {
		return errors.New("the temporary table rules are not built")
	}
	if err := (*r.p).Create(ctx, t); err != nil {
		return err
	}
	if r.pub != nil {
		if err := r.pub.PublishTable(ctx, t.Workspace, t.Table, t.Class); err != nil && r.log != nil {
			r.log.Warn("app: the effective rules were not sent after a temporary table rule", "table", t.Table, "err", err)
		}
	}
	return nil
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
func newTableRules(o OrchestrationOptions, chars *characters.Service, reg characters.Registry, ib chain.Inbox, policy chain.Policy, qs *quests.Service, tbl *tables.Service, sup chain.Supervision) (*tablerules.Service, error) {
	return tablerules.NewService(tablerules.Options{Store: &tablerules.Store{DB: o.Guild}, Clock: o.Clock, Characters: chain.CharactersFrom(chars, reg, o.Clock),
		Policy: policy, Classes: o.ChainClasses, Supervision: sup, Missions: ruleMissions{qs}, Tables: ruleTables{tbl}, Inbox: ib, Logger: o.Logger})
}

// supTables is the tables as the supervision sees them: whether one is over,
// and no such table.
type supTables struct{ p **tables.Service }

func (t supTables) Over(ctx context.Context, workspace, table string) (bool, error) {
	if *t.p == nil {
		return false, nil
	}
	over, err := ruleTables{*t.p}.Over(ctx, workspace, table)
	if errors.Is(err, tables.ErrNoTable) {
		return false, chain.ErrNoTable
	}
	return over, err
}

// Stalled implements chain.TableStalls.
func (t supTables) Stalled(ctx context.Context, after time.Duration) ([]chain.StalledTable, error) {
	if *t.p == nil {
		return nil, nil
	}
	list, err := (*t.p).Stalled(ctx, after)
	out := make([]chain.StalledTable, 0, len(list))
	for _, s := range list {
		out = append(out, chain.StalledTable{Workspace: s.Table.Workspace, Table: s.Table.ID, Since: s.Since})
	}
	return out, err
}

// StallWarned implements chain.TableStalls.
func (t supTables) StallWarned(ctx context.Context, workspace, table string, since time.Time) error {
	return (*t.p).StallWarned(ctx, workspace, table, since)
}

// PauseTable implements chain.TableStalls: the pause of the table itself.
func (t supTables) PauseTable(ctx context.Context, workspace, table, reason, by string) (bool, error) {
	cur, err := (*t.p).Get(ctx, workspace, table)
	if err != nil {
		if errors.Is(err, tables.ErrNoTable) {
			return false, chain.ErrNoTable
		}
		return false, err
	}
	if cur.Paused {
		return false, nil
	}
	if err := (*t.p).Pause(ctx, workspace, table, tables.PauseRequest{Reason: reason, By: by}); err != nil {
		return false, err
	}
	return true, nil
}

var _ chain.TableStalls = supTables{}

// rerouteRef is the chain seen by the supervision, which is made before it.
type rerouteRef struct{ p **chain.Service }

func (r rerouteRef) RerouteTable(ctx context.Context, workspace, table, why string) error {
	if *r.p == nil {
		return nil
	}
	return (*r.p).RerouteTable(ctx, workspace, table, why)
}

// ruleListerRef gives the supervision the temporary rules a request made.
type ruleListerRef struct{ p **tablerules.Service }

func (r ruleListerRef) RulesOfRequest(ctx context.Context, workspace, table string, request int64) ([]int64, error) {
	if *r.p == nil {
		return nil, nil
	}
	rules, err := (*r.p).List(ctx, workspace, table)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, rule := range rules {
		if rule.Request == request {
			ids = append(ids, rule.ID)
		}
	}
	return ids, nil
}

// oversightRef is the supervision seen by the tables, which are made before it.
type oversightRef struct{ p **chain.Supervisor }

func (r oversightRef) Status(ctx context.Context, workspace, table string) (bool, string, string, error) {
	if *r.p == nil {
		return false, "", "", nil
	}
	return (*r.p).Status(ctx, workspace, table)
}
