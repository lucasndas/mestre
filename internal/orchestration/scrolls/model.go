package scrolls

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
)

// DefaultMaxSize is the largest a scroll may be: 256 KiB.
const DefaultMaxSize = 256 << 10

// MaxNameLength is how long a name taken from the first line may be.
const MaxNameLength = 80

// Untitled is the name of a scroll whose first line is empty.
const Untitled = "Sem título"

// Note is a scroll: the index of the file and its place.
type Note struct {
	// Key is "<workspace>/<table>/<id>", the identity the connections use;
	// ID is "p0001", the name of the file, which repeats from table to table.
	Key, Workspace, Table, ID string
	Name                      string
	// FixedName is true when the name came from --name and does not follow
	// the first line.
	FixedName bool
	// Tome is the key of the tome that holds it, TomeName its name ("" none).
	Tome, TomeName string
	Revision, Size int64
	// Author is who made the last change.
	Author, AuthorID     string
	CreatedAt, UpdatedAt time.Time
}

// Tome is a group of scrolls of a table.
type Tome struct {
	Key, Workspace, Table, ID string
	Name                      string
	CreatedAt                 time.Time
}

// ActorKind says who acts.
type ActorKind string

// The actors.
const (
	ByCharacter ActorKind = "character"
	ByUser      ActorKind = "user"
	ByEngine    ActorKind = "engine"
)

// Actor is who asks for an operation: a character (only the scrolls visible
// to it), a user (the Administrators and above write, the others read), or the
// engine itself.
type Actor struct {
	Kind  ActorKind
	ID    string
	Name  string
	Level characters.UserLevel
}

// Engine is the actor of the engine.
var Engine = Actor{Kind: ByEngine, Name: "Mestre"}

// author is the name kept as the author of a change.
func (a Actor) author() string {
	if a.Name != "" {
		return a.Name
	}
	if a.ID != "" {
		return a.ID
	}
	return "Mestre"
}

func (a Actor) canWrite() bool {
	switch a.Kind {
	case ByCharacter, ByEngine:
		return true
	case ByUser:
		return a.Level == characters.LevelOwner || a.Level == characters.LevelAdministrator
	}
	return false
}

// Errors of the scrolls.
var (
	// ErrNotVisible: no such scroll, or not connected to the caller; the
	// message has the name that was asked for.
	ErrNotVisible = errors.New("scroll not found or not visible")
	// ErrNoTable: scrolls exist only inside tables.
	ErrNoTable = errors.New("scrolls exist only inside the tables")
	// ErrTooLarge: the content passes the largest size.
	ErrTooLarge = errors.New("scroll too large")
	// ErrNameTaken: a scroll of the table already has the fixed name.
	ErrNameTaken = errors.New("name already used in the table")
	// ErrRevision: another change came first.
	ErrRevision = errors.New("the scroll changed since the revision that was expected")
	// ErrEditNotFound: the piece to replace is not in the scroll.
	ErrEditNotFound = errors.New("the piece to replace was not found")
	// ErrEditAmbiguous: the piece to replace is in the scroll more than once.
	ErrEditAmbiguous = errors.New("the piece to replace appears more than once")
	// ErrOtherTable: a scroll of another table cannot be stacked here.
	ErrOtherTable = errors.New("the scroll is in another table")
	// ErrLevelDenied: who asked may not do it.
	ErrLevelDenied = errors.New("the level of the caller does not allow it")
	// ErrApproval: deleting a scroll needs the explicit request of an
	// authorized user, which the flow of approval brings (milestone 5).
	ErrApproval = errors.New("deleting a scroll needs the explicit request of an authorized user")
	// ErrBadRequest: the request does not make sense.
	ErrBadRequest = errors.New("the request is not valid")
)

// ConflictError carries the revision a conflict found.
type ConflictError struct {
	Name           string
	Expected, Have int64
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s is at revision %d, and the expected one was %d", e.Name, e.Have, e.Expected)
}

// Unwrap makes errors.Is(err, ErrRevision) true.
func (e *ConflictError) Unwrap() error { return ErrRevision }

// SizeError carries the size that was refused and the limit.
type SizeError struct {
	Name        string
	Size, Limit int64
}

func (e *SizeError) Error() string {
	return fmt.Sprintf("the content of %s would have %d bytes, and the limit is %d", e.Name, e.Size, e.Limit)
}

// Unwrap makes errors.Is(err, ErrTooLarge) true.
func (e *SizeError) Unwrap() error { return ErrTooLarge }

func notVisible(name string) error { return fmt.Errorf("%w: %s", ErrNotVisible, name) }

// deriveName is the name taken from the first line of the content: without
// the `#` and the spaces at the start, up to 80 characters; an empty line is
// "Sem título".
func deriveName(content string) string {
	line, _, _ := strings.Cut(content, "\n")
	line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "# \t"))
	if utf8.RuneCountInString(line) > MaxNameLength {
		line = strings.TrimSpace(string([]rune(line)[:MaxNameLength]))
	}
	if line == "" {
		return Untitled
	}
	return line
}

func nameKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// pathPart is what a table or a workspace name may be to be a folder of the
// scrolls.
var pathPart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func checkPart(what, v string) error {
	if !pathPart.MatchString(v) || strings.Contains(v, "..") {
		return fmt.Errorf("%w: %s %q cannot be a folder", ErrBadRequest, what, v)
	}
	return nil
}

// Rel is the path of the file of a scroll, with slashes, relative to the
// folder where `.mestre` lives (the clone principal on the Citadel, the
// worktree of the table elsewhere).
func Rel(table, id string) string {
	return path.Join(".mestre", "mesas", table, "pergaminhos", id+".md")
}

func keyOf(workspace, table, id string) string { return workspace + "/" + table + "/" + id }

func idOf(n int64) string     { return fmt.Sprintf("p%04d", n) }
func tomeIDOf(n int64) string { return fmt.Sprintf("t%04d", n) }
