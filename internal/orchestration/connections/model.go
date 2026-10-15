package connections

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Kind is what an endpoint of a connection is.
type Kind string

// The endpoints. A tome stands for all the scrolls it holds, the ones that
// join it later included (docs/orquestracao/pergaminhos-e-tomos.md#tomos).
const (
	KindCharacter Kind = "character"
	KindScroll    Kind = "scroll"
	KindTome      Kind = "tome"
)

// Ref is one endpoint: the id of a character, of a scroll or of a tome.
type Ref struct {
	Kind Kind
	ID   string
}

func (r Ref) key() string { return string(r.Kind) + "\x00" + r.ID }

func (r Ref) String() string { return string(r.Kind) + ":" + r.ID }

// Char, Scroll and Tome make the endpoints.
func Char(id string) Ref { return Ref{KindCharacter, id} }

// Scroll makes the endpoint of a scroll.
func Scroll(id string) Ref { return Ref{KindScroll, id} }

// Tome makes the endpoint of a tome.
func Tome(id string) Ref { return Ref{KindTome, id} }

// The reasons of a connection (docs/orquestracao/conexoes.md#automáticas). A
// connection exists while it has at least one.
const (
	// ReasonMaster: the table master and the Grand Master.
	ReasonMaster = "mestre"
	// ReasonTable: a character of the table and its table master.
	ReasonTable = "mesa"
	// ReasonCreator: a character and who recruited or spawned it.
	ReasonCreator = "criador"
	// ReasonManual: made by hand, by a master or a user.
	ReasonManual = "manual"

	questPrefix = "missao:"
)

// QuestReason is the reason of the connections a quest made.
func QuestReason(quest string) string { return questPrefix + quest }

// Reason is why a connection exists.
type Reason struct {
	Name string
	At   time.Time
	// By is who asked for a manual connection (a character or a user id).
	By string
}

// Connection is a pair of endpoints with the reasons that keep it.
type Connection struct {
	ID               string
	Workspace, Table string
	A, B             Ref
	Reasons          []Reason
	CreatedAt        time.Time
}

// Has says whether the connection has the reason.
func (c Connection) Has(reason string) bool {
	return slices.ContainsFunc(c.Reasons, func(r Reason) bool { return r.Name == reason })
}

// ReasonNames lists the reasons, in the order they were added.
func (c Connection) ReasonNames() []string {
	out := make([]string, len(c.Reasons))
	for i, r := range c.Reasons {
		out[i] = r.Name
	}
	return out
}

// Other is the endpoint that is not x.
func (c Connection) Other(x Ref) Ref {
	if c.A == x {
		return c.B
	}
	return c.A
}

// Touches says whether x is one of the endpoints.
func (c Connection) Touches(x Ref) bool { return c.A == x || c.B == x }

// ordered puts the pair in the one order the table keeps it in.
func ordered(a, b Ref) (Ref, Ref) {
	if a.key() <= b.key() {
		return a, b
	}
	return b, a
}

// Errors of the connections.
var (
	// ErrNotFound: no such character, scroll or tome (in the table).
	ErrNotFound = errors.New("not found")
	// ErrSame: a connection of something with itself.
	ErrSame = errors.New("an item cannot be connected to itself")
	// ErrDifferentTables: the items are in different tables.
	ErrDifferentTables = errors.New("the items are in different tables")
	// ErrGrandMaster: the Grand Master is connected only by the engine.
	ErrGrandMaster = errors.New("the Grand Master is connected only by the engine")
	// ErrLevelDenied: who asked may not do it.
	ErrLevelDenied = errors.New("the level of the caller does not allow it")
	// ErrTableRequired: the Grand Master has to say the table.
	ErrTableRequired = errors.New("the table is required")
	// ErrNoConnection: there is no connection between the two.
	ErrNoConnection = errors.New("not connected")
)

// NotConnectedError: the target is in the table of the caller but is not
// connected to it (exit 3, not_connected).
type NotConnectedError struct{ Name string }

func (e *NotConnectedError) Error() string { return e.Name + " não está conectado a você" }

// AutomaticError: a connection kept only by reasons of the engine cannot be
// removed by a person while they exist; the reasons are what the canvas shows.
type AutomaticError struct{ Reasons []string }

func (e *AutomaticError) Error() string {
	return "a conexão é automática e existe enquanto o motivo existir: " + strings.Join(e.Reasons, ", ")
}

func notFound(name string) error { return fmt.Errorf("%w: %s", ErrNotFound, name) }
