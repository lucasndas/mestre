package tablerules

import (
	"errors"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/providers"
)

// State is the state of a rule.
type State string

// The states of a rule.
const (
	Active    State = "active"
	Expired   State = "expired"
	Revoked   State = "revoked"
	Suspended State = "suspended"
)

// Ended says the rule is over for good.
func (s State) Ended() bool { return s == Expired || s == Revoked }

// Rule is a temporary table rule.
type Rule struct {
	ID        int64
	Workspace string
	Table     string
	// Class is the class of who asked: the rule is for the characters of this
	// class in this table.
	Class   string
	Request int64
	State   State
	// Level is the level of the author; Author the character that made it.
	Level  chain.LevelKind
	Author string
	// Risk is the highest risk the rule covers (baixo or medio).
	Risk classes.Risk
	// Pattern and ToolKind are what a rule of a pattern covers; Action what a
	// rule of the exact action covers.
	Pattern  string
	ToolKind providers.ToolKind
	Action   *classes.Action
	// Until is when it ends by itself; Mission the quest whose end ends it.
	Until     time.Time
	Mission   string
	CreatedAt time.Time
	EndedAt   time.Time
}

// body is the JSON of what a rule covers.
type body struct {
	Pattern  string             `json:"pattern,omitempty"`
	ToolKind providers.ToolKind `json:"tool_kind,omitempty"`
	Action   *classes.Action    `json:"action,omitempty"`
}

// Change is a line of the history of states of a rule.
type Change struct {
	At     time.Time
	State  State
	Reason string
	By     string
}

// Use is something a rule let through.
type Use struct {
	At        time.Time
	Character string
	Action    string
}

// The errors of the service.
var (
	// ErrNoRule: no such rule.
	ErrNoRule = errors.New("no such temporary table rule")
	// ErrNotAllowed: who asks may not revoke the rule.
	ErrNotAllowed = errors.New("who asks may not revoke this rule")
	// ErrEnded: the rule is already over.
	ErrEnded = errors.New("the temporary table rule is already over")
)

// decreeLevel is the level the effective rules give an author: the agent
// superior below the Mestre da mesa has no level of its own there and counts as
// the lowest one, since all of them only turn an ask into allow.
func decreeLevel(k chain.LevelKind) classes.Level {
	if k == chain.LevelGrandMaster {
		return classes.LevelGrandMaster
	}
	return classes.LevelTableMaster
}
