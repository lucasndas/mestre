package app

import (
	"context"

	"github.com/mestre-ia/mestre/internal/decrees"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/tablerules"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
	"github.com/mestre-ia/mestre/internal/panel"
)

// PanelRulesSource is what an IntegrationRun may also be, for the screens of the
// decrees, delegations, supervision of the chain and workspace rules of the panel.
type PanelRulesSource interface {
	PanelRules() panel.RulesBackend
}

// PanelRules implements PanelRulesSource. The decrees are the service itself; the rest
// is read from the orchestration when a screen asks, since it is attached after the panel.
func (r *stdRun) PanelRules() panel.RulesBackend { return panelRules{Service: r.decrees, r: r} }

type panelRules struct {
	*decrees.Service
	r *stdRun
}

func (p panelRules) orc() (*Orchestration, error) {
	if o := p.r.orc.Load(); o != nil {
		return o, nil
	}
	return nil, errNoOrchestration
}

func (p panelRules) rules() (*tablerules.Service, error) {
	o, err := p.orc()
	if err != nil {
		return nil, err
	}
	if o.TableRules == nil {
		return nil, errNoOrchestration
	}
	return o.TableRules, nil
}

func (p panelRules) supervisor() (*chain.Supervisor, error) {
	o, err := p.orc()
	if err != nil {
		return nil, err
	}
	if o.Supervisor == nil {
		return nil, errNoOrchestration
	}
	return o.Supervisor, nil
}

// LiveRules implements panel.TableRuleBook.
func (p panelRules) LiveRules(ctx context.Context, workspace string) ([]tablerules.Rule, error) {
	s, err := p.rules()
	if err != nil {
		return nil, err
	}
	return s.Store().Live(ctx, workspace)
}

// Rule implements panel.TableRuleBook.
func (p panelRules) Rule(ctx context.Context, id int64) (tablerules.Rule, error) {
	s, err := p.rules()
	if err != nil {
		return tablerules.Rule{}, err
	}
	return s.Get(ctx, id)
}

// RuleUses implements panel.TableRuleBook.
func (p panelRules) RuleUses(ctx context.Context, id int64) ([]tablerules.Use, error) {
	s, err := p.rules()
	if err != nil {
		return nil, err
	}
	return s.Uses(ctx, id)
}

// RevokeRule implements panel.TableRuleBook: the panel conferred the level of the person.
func (p panelRules) RevokeRule(ctx context.Context, id int64, userID string) error {
	s, err := p.rules()
	if err != nil {
		return err
	}
	return s.Revoke(ctx, id, tablerules.Revoker{Administrator: true, ID: userID})
}

// OpenTables implements panel.ChainBook: the tables of the workspace that are not over.
func (p panelRules) OpenTables(ctx context.Context, workspace string) ([]string, error) {
	o, err := p.orc()
	if err != nil {
		return nil, err
	}
	all, err := o.Tables.List(ctx, workspace)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, t := range all {
		switch t.State {
		case tables.StateOpen, tables.StateWorking, tables.StateInReview:
			out = append(out, t.ID)
		}
	}
	return out, nil
}

// Metrics implements panel.ChainBook.
func (p panelRules) Metrics(ctx context.Context, workspace string) ([]chain.QueueMetric, error) {
	s, err := p.supervisor()
	if err != nil {
		return nil, err
	}
	return s.Metrics(ctx, workspace)
}

// PauseOf implements panel.ChainBook.
func (p panelRules) PauseOf(ctx context.Context, workspace, table string) (chain.Pause, bool, error) {
	s, err := p.supervisor()
	if err != nil {
		return chain.Pause{}, false, err
	}
	return s.PauseOf(ctx, workspace, table)
}

// PauseAutonomy implements panel.ChainBook.
func (p panelRules) PauseAutonomy(ctx context.Context, r chain.PauseRequest) (bool, error) {
	s, err := p.supervisor()
	if err != nil {
		return false, err
	}
	return s.PauseAutonomy(ctx, r)
}

// ResumeAutonomy implements panel.ChainBook.
func (p panelRules) ResumeAutonomy(ctx context.Context, r chain.PauseRequest) (bool, error) {
	s, err := p.supervisor()
	if err != nil {
		return false, err
	}
	return s.ResumeAutonomy(ctx, r)
}

// Principal implements panel.RulesBackend.
func (p panelRules) Principal(workspace string) string {
	o, err := p.orc()
	if err != nil || o.Workspaces == nil || !plainName(workspace) {
		return ""
	}
	return o.Workspaces.Layout().Principal(workspace)
}
