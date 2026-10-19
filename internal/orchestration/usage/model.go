package usage

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/providers"
)

// Origin is where a piece of work came from.
type Origin string

// The origins. The last two carry no quest, table or workspace: they count
// only in the total of the day and in the use of the account.
const (
	OriginStep        Origin = "etapa"
	OriginConformance Origin = "conformidade"
	OriginClassTest   Origin = "teste_de_classe"
)

// Valid says whether o is one of the three.
func (o Origin) Valid() bool {
	return o == OriginStep || o == OriginConformance || o == OriginClassTest
}

// Scope is what a limit covers.
type Scope string

// The scopes.
const (
	ScopeQuest     Scope = "mission"
	ScopeTable     Scope = "table"
	ScopeWorkspace Scope = "workspace"
	// ScopeDay is the day of a workspace; ScopeDayTotal the day of the whole
	// installation.
	ScopeDay      Scope = "day"
	ScopeDayTotal Scope = "day_total"
)

// Valid says whether s is a scope.
func (s Scope) Valid() bool {
	switch s {
	case ScopeQuest, ScopeTable, ScopeWorkspace, ScopeDay, ScopeDayTotal:
		return true
	}
	return false
}

// perDay says whether the limit starts again every day.
func (s Scope) perDay() bool { return s == ScopeDay || s == ScopeDayTotal }

// ceilingScope is the scope the cost ceiling knows the limit by.
func (s Scope) ceilingScope() classes.Scope {
	switch s {
	case ScopeQuest:
		return classes.ScopeMission
	case ScopeTable:
		return classes.ScopeTable
	case ScopeDay:
		return classes.ScopeDay
	}
	return classes.ScopeWorkspace
}

// TableKey is the key of the limit and of the consumption of a table.
func TableKey(workspace, table string) string { return workspace + "/" + table }

// Record is one piece of use. Time and turns are always given; the tokens
// and the cost only when the provider gave them.
type Record struct {
	Character string
	Step      string
	Quest     string
	Workspace string
	Table     string
	// Profile is the account profile ("" is the default one of the provider).
	Profile  string
	Provider providers.ID
	Model    string
	// At is when it happened (default: now).
	At time.Time
	// Source is where it came from (statusline, token_count, usage_command,
	// turn, conformance…).
	Source string
	Origin Origin

	// QuestName and TableName are what the warnings call them (not kept).
	QuestName, TableName string

	Tokens Tokens
	// CostUSD is nil when the provider gave no cost.
	CostUSD   *float64
	Active    time.Duration
	Turns     int
	Estimated bool
}

// Tokens are the tokens of a piece of use.
type Tokens struct {
	Input, Output, CacheRead, CacheWrite, Reasoning int64
	// Known says the provider gave tokens (zero is then a real zero).
	Known bool
}

// Total is every kind summed: what a limit in tokens counts.
func (t Tokens) Total() int64 { return t.Input + t.Output + t.CacheRead + t.CacheWrite + t.Reasoning }

func (r Record) validate() error {
	switch {
	case r.Provider == "":
		return errors.New("usage: a record needs the provider")
	case !r.Origin.Valid():
		return fmt.Errorf("usage: unknown origin %q", r.Origin)
	case r.Origin != OriginStep && (r.Quest != "" || r.Table != "" || r.Workspace != ""):
		return fmt.Errorf("usage: a record of origin %s carries no quest, table or workspace", r.Origin)
	case r.Active < 0 || r.Turns < 0:
		return errors.New("usage: negative time or turns")
	case r.CostUSD != nil && (math.IsNaN(*r.CostUSD) || math.IsInf(*r.CostUSD, 0) || *r.CostUSD < 0):
		return errors.New("usage: the cost is not a finite non-negative number")
	}
	return nil
}

// Totals is the sum of some rows.
type Totals struct {
	// CostUSD sums the rows that have a cost; HasCost says any had.
	CostUSD float64
	HasCost bool
	// Tokens sums the rows that have tokens; HasTokens says any had.
	Tokens    int64
	HasTokens bool
	Active    time.Duration
	Turns     int
}

// Used is the use in a unit: the rows with no data in it do not count.
func (t Totals) Used(u classes.Unit) float64 {
	switch u {
	case classes.UnitUSD:
		return t.CostUSD
	case classes.UnitTokens:
		return float64(t.Tokens)
	case classes.UnitHours:
		return t.Active.Hours()
	}
	return 0
}

// Has says whether any row had data in the unit: without it the panel shows
// the gap next to the limit.
func (t Totals) Has(u classes.Unit) bool {
	switch u {
	case classes.UnitUSD:
		return t.HasCost
	case classes.UnitTokens:
		return t.HasTokens
	}
	return t.Active > 0 || t.Turns > 0
}

// Limit is the limit of a scope.
type Limit struct {
	Scope Scope
	Key   string
	Unit  classes.Unit
	Value float64
}

// LimitView is a limit with how much is used and left in the period.
type LimitView struct {
	Limit
	Period string
	// Effective is the limit plus what was approved above it.
	Effective float64
	Used      float64
	// HasData says the use has data in the unit of the limit (false: the
	// panel shows the gap).
	HasData bool
	Percent float64
}

// Subject is a character as the use needs to know it.
type Subject struct {
	ID, Name, Sheet, Class string
	Role                   characters.Role
	Workspace, Table       string
	// TableName is what the people call the table.
	TableName   string
	Quest, Step string
	// QuestName is what the people call the quest.
	QuestName string
	Provider  providers.ID
	Profile   string
}

// Verdict is whether a new piece of work may start for a provider and an
// account (docs/plataforma/limites-de-uso.md#limiar-de-início).
type Verdict struct {
	// Hold says the start waits.
	Hold bool
	// Until is when it may start (zero: not known).
	Until time.Time
	// Window and Percent are the window that holds it.
	Window  string
	Percent float64
	// Unconfirmed says the use of some window is not confirmed (the reading
	// is old and below the threshold): the work starts with a warning.
	Unconfirmed bool
}

// Ages of the readings: the use only grows inside a window.
const (
	// StaleAfter is how old a reading below the threshold may be before it
	// proves nothing.
	StaleAfter = 15 * time.Minute
	// DefaultThreshold is the default start threshold, in percent.
	DefaultThreshold = 95.0
)

// Errors.
var (
	// ErrForbidden is a level that may not do it.
	ErrForbidden = errors.New("usage: the level does not allow it")
	// ErrInvalid is a limit that makes no sense.
	ErrInvalid = errors.New("usage: invalid limit")
)
