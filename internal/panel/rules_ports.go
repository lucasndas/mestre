package panel

import (
	"context"
	"time"

	"github.com/mestre-ia/mestre/internal/decrees"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/tablerules"
)

// RulesBackend is what the screens of the rules and of the chain need of the
// engine. The decrees are *decrees.Service as it is; the rest is a thin adapter
// the host makes over the orchestration (which exists only after the panel), so
// every method answers ErrRulesUnavailable until then.
type RulesBackend interface {
	DecreeBook
	TableRuleBook
	ChainBook
	// Principal is the folder of the main clone of a workspace ("" when it has none).
	Principal(workspace string) string
}

// DecreeBook is the decrees (*decrees.Service). The panel does not repeat any of its
// rules: the level, the workspaces, the PIN and the refusals are the service's.
type DecreeBook interface {
	ListWorkspace(ctx context.Context, workspace string, all bool) ([]decrees.View, error)
	Get(ctx context.Context, number int64) (decrees.Decree, error)
	Uses(ctx context.Context, number int64) ([]decrees.Use, error)
	Propose(ctx context.Context, p decrees.Proposal) (decrees.Pending, error)
	Confirm(ctx context.Context, proposal, userID, pinRequest string) (decrees.Decree, error)
	Revoke(ctx context.Context, number int64, userID, pinRequest string) error
	NeedsPINToRevoke(ctx context.Context, number int64) (bool, error)
	Renew(ctx context.Context, number int64, userID string) (decrees.Pending, error)
	RevokeAllDelegations(ctx context.Context, userID string) (decrees.RevokeReport, error)
	Subs(ctx context.Context, workspace string, live bool) ([]decrees.Sub, error)
	SubUses(ctx context.Context, id int64) (int, error)
	RevokeSub(ctx context.Context, id int64, by string, isPerson bool) error
}

// TableRuleBook is the temporary table rules.
type TableRuleBook interface {
	LiveRules(ctx context.Context, workspace string) ([]tablerules.Rule, error)
	Rule(ctx context.Context, id int64) (tablerules.Rule, error)
	RuleUses(ctx context.Context, id int64) ([]tablerules.Use, error)
	// RevokeRule ends a rule in the name of a person of Administrator or above.
	RevokeRule(ctx context.Context, id int64, userID string) error
}

// ChainBook is the supervision of the chain (*chain.Supervisor) and the tables
// that are not over.
type ChainBook interface {
	OpenTables(ctx context.Context, workspace string) ([]string, error)
	Metrics(ctx context.Context, workspace string) ([]chain.QueueMetric, error)
	PauseOf(ctx context.Context, workspace, table string) (chain.Pause, bool, error)
	PauseAutonomy(ctx context.Context, r chain.PauseRequest) (bool, error)
	ResumeAutonomy(ctx context.Context, r chain.PauseRequest) (bool, error)
}

// rulesHolder lets the backend be set after the panel is made.
type rulesHolder struct{ b RulesBackend }

// SetRules gives the panel the backend of the screens of the rules and of the
// chain; until then they answer that they are not available.
func (p *Panel) SetRules(b RulesBackend) { p.rules.Store(&rulesHolder{b}) }

func (p *Panel) rulesBackend() RulesBackend {
	if h := p.rules.Load(); h != nil {
		return h.b
	}
	return nil
}

// seconds is a duration in whole seconds.
func seconds(d time.Duration) int64 { return int64(d / time.Second) }
