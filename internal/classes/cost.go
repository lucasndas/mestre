package classes

import (
	"context"
	"errors"
	"fmt"
)

// Unit is the unit of a usage limit (docs/plataforma/limites-de-uso.md#limites).
type Unit string

// The units.
const (
	UnitUSD    Unit = "usd"
	UnitTokens Unit = "tokens"
	UnitHours  Unit = "hours"
)

// Scope is what a limit applies to, as the cost ceiling sees it.
type Scope string

// The scopes the cost ceiling caps: a table (and a mission, which is part of
// one), and a day of the workspace. The total of a workspace has no ceiling
// of its own.
const (
	ScopeMission   Scope = "per_mission"
	ScopeTable     Scope = "per_table"
	ScopeDay       Scope = "per_day"
	ScopeWorkspace Scope = "per_workspace"
)

func (l *CostLimits) of(s Scope) *float64 {
	if l == nil {
		return nil
	}
	switch s {
	case ScopeTable, ScopeMission:
		return l.PerTable
	case ScopeDay:
		return l.PerDay
	}
	return nil
}

// Cap is the cost ceiling of a limit: nil when the unit or the scope has
// none.
func (c *Ceiling) Cap(u Unit, s Scope) *float64 {
	if c == nil {
		return nil
	}
	return c.Cost.units()[u].of(s)
}

// ErrAboveCostCeiling is a limit configured above the cost ceiling: it is
// refused when saved.
var ErrAboveCostCeiling = errors.New("limit above the cost ceiling of the workspace")

// CheckLimit refuses a limit configured above the cost ceiling.
func (c *Ceiling) CheckLimit(u Unit, s Scope, value float64) error {
	if cp := c.Cap(u, s); cp != nil && value > *cp {
		return fmt.Errorf("%w: %s %s %v > %v", ErrAboveCostCeiling, u, s, value, *cp)
	}
	return nil
}

// EffectiveLimit is the limit that holds: the configured one (nil: none),
// never above the cost ceiling; with no configured limit, the ceiling
// itself. fromCeiling says the ceiling set it.
func (c *Ceiling) EffectiveLimit(u Unit, s Scope, configured *float64) (limit *float64, fromCeiling bool) {
	cp := c.Cap(u, s)
	switch {
	case cp == nil:
		return configured, false
	case configured == nil || *configured > *cp:
		v := *cp
		return &v, true
	}
	return configured, false
}

// ExtraSpend is a custo_extra request: a mission, table or day of the
// workspace at 100% of its limit, and the extra asked
// (docs/plataforma/limites-de-uso.md#80-e-100).
type ExtraSpend struct {
	Request   int64
	Workspace string
	Table     string
	Unit      Unit
	Scope     Scope
	Used      float64
	Limit     float64
	Extra     float64
}

// PassesCeiling reports whether the limit raised by the extra goes above
// the cost ceiling.
func (c *Ceiling) PassesCeiling(r ExtraSpend) bool {
	cp := c.Cap(r.Unit, r.Scope)
	return cp != nil && r.Limit+r.Extra > *cp
}

// MinExtraSpendLevel is the lowest level that may approve the request: the
// Grand Master within the ceiling (the approval policy then decides); above
// it, an Administrator, never the Grand Master
// (docs/aprovacoes/politica.md).
func (c *Ceiling) MinExtraSpendLevel(r ExtraSpend) Level {
	if c.PassesCeiling(r) {
		return LevelAdmin
	}
	return LevelGrandMaster
}

// ErrHumanRequired is an approval the Grand Master may not give: it goes to
// the users (human_required of `mestre approval decide`).
var ErrHumanRequired = errors.New("human_required")

// ErrLevelTooLow is an approval by a user below the level the request
// needs.
var ErrLevelTooLow = errors.New("approval needs a higher level")

// UserAsker takes a request to the users of at least the given level: the
// approval flow (docs/aprovacoes/fluxo.md) implements it.
type UserAsker interface {
	AskUsers(ctx context.Context, r ExtraSpend, min Level, why string) error
}

// CostGate is the cost ceiling's check on an approval of extra spend. The
// approval flow calls it before it accepts a decision.
type CostGate struct {
	Ceiling interface {
		Current(ctx context.Context) (*Ceiling, error)
	}
	Users UserAsker
}

// CheckApproval checks that the approver may give this extra. The Grand
// Master above the ceiling is refused with ErrHumanRequired and the request
// goes to the Administrators and the Owner; a user below Administrator,
// with ErrLevelTooLow.
func (g *CostGate) CheckApproval(ctx context.Context, r ExtraSpend, approver Level) error {
	c, err := g.Ceiling.Current(ctx)
	if err != nil {
		return fmt.Errorf("cost ceiling: %w", err)
	}
	min := c.MinExtraSpendLevel(r)
	if min == LevelGrandMaster || (approver.rank() >= min.rank() && approver != LevelGrandMaster) {
		return nil
	}
	why := fmt.Sprintf("o extra de %v %s passa o teto de custo do workspace (%v)", r.Extra, r.Unit, *c.Cap(r.Unit, r.Scope))
	if approver == LevelGrandMaster {
		if err := g.Users.AskUsers(ctx, r, min, why); err != nil {
			return fmt.Errorf("cost ceiling: %w", err)
		}
		return fmt.Errorf("%w: %s", ErrHumanRequired, why)
	}
	return fmt.Errorf("%w: %s; aprova um Administrador ou o Dono", ErrLevelTooLow, why)
}
