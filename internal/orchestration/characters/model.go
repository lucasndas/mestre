package characters

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/lucasndas/mestre/internal/orchestration/presets"
	"github.com/lucasndas/mestre/internal/sessionzero"
)

// Kind says whether the row is an agent or a simple terminal.
type Kind string

// The kinds.
const (
	KindCharacter Kind = "character"
	KindTerminal  Kind = "terminal"
)

// Role is the level of a character, as the session zero names it.
type Role = sessionzero.Role

// The levels.
const (
	RoleCharacter   = sessionzero.RoleCharacter
	RoleGrandMaster = sessionzero.RoleGrandMaster
	RoleTableMaster = sessionzero.RoleTableMaster
)

// Reserved names (docs/orquestracao/personagens.md#origem-só-de-fichas-e-bases).
const (
	NameGrandMaster = "Grão-Mestre"
	NameTableMaster = "Mestre"
)

// Lifecycle is the lifecycle the sheet defines.
type Lifecycle string

// The lifecycles.
const (
	Ephemeral  Lifecycle = presets.Ephemeral
	Persistent Lifecycle = presets.Persistent
)

// CreatorKind says what created a character.
type CreatorKind string

// The creators.
const (
	// CreatedByEngine: the engine itself: the Grand Master of a workspace,
	// a character of a step of a quest.
	CreatedByEngine CreatorKind = "engine"
	// CreatedByCharacter: a character that recruited or spawned it (the
	// Grand Master on `table open`, a table master with `recruit`).
	CreatedByCharacter CreatorKind = "character"
	// CreatedByUser: a user, on the canvas.
	CreatedByUser CreatorKind = "user"
)

// UserLevel is the level of a user (docs/telegram/usuarios-e-niveis.md).
type UserLevel string

// The levels of the users that matter here: only the Owner and the
// Administrators create characters.
const (
	LevelOwner         UserLevel = "owner"
	LevelAdministrator UserLevel = "administrator"
	LevelApprover      UserLevel = "approver"
	LevelObserver      UserLevel = "observer"
)

// Creator is who asks for a character. For a character, Role is its level,
// Table its table and Name its name, as the registry knows them (the Service
// fills them in); a user has a Level.
type Creator struct {
	Kind CreatorKind `json:"kind"`
	// ID is the id of the character or of the user; empty for the engine.
	ID    string    `json:"id,omitempty"`
	Level UserLevel `json:"level,omitempty"`
}

// Character is a row of the registry: a character or a simple terminal.
type Character struct {
	ID, Name   string
	Kind       Kind
	Role       Role
	Workspace  string
	Table      string
	Machine    string
	Sheet      string
	Base       string
	Class      string
	Account    string
	TerminalID string
	// PTY is the mestre-pty of the terminal (copied without its secret).
	PTY PTYInfo
	// Dir is the worktree of the character on its machine.
	Dir string

	State  State
	Reason string
	// Reduced is why the monitoring of the character is reduced, if it is.
	Reduced string
	Pinned  bool
	// Lifecycle and MaxIdle come from the sheet it was born from.
	Lifecycle Lifecycle
	MaxIdle   time.Duration
	// CreatedBy is who created it: the superior of the chain of approval
	// comes from it.
	CreatedBy Creator
	// Quest is the quest the character was created for ("" for a direct
	// recruit).
	Quest             string
	ProviderSessionID string
	// TaskOpen is true from the task or step delivered to it until the done
	// that answers it.
	TaskOpen bool
	// IdleSince is when the current stretch of idle began (zero: not idle).
	IdleSince time.Time
	// StopRequestedAt is when a safe end was asked for and still waits for
	// the next safe point (zero: none).
	StopRequestedAt time.Time
	StopRequestedBy string
	StopReason      string

	CreatedAt, StartedAt, LastSignalAt, StoppedAt time.Time
}

// Status is the state with its reason.
func (c Character) Status() Status { return Status{c.State, c.Reason} }

// PTYInfo is the mestre-pty of a terminal: its PID with the identity of the
// start of the process (so a reused PID is never taken for it), and its
// version.
type PTYInfo struct {
	PID     int
	Started string
	Version string
}

// Counts says whether the character counts in the limits: the Grand Master
// and the table masters count nothing and are never held back; the simple
// terminals count.
func (c Character) Counts() bool { return c.Role == RoleCharacter && c.State.Alive() }

// Errors of the creation and of the end.
var (
	// ErrNoCharacter: no such character (or not visible to who asked).
	ErrNoCharacter = errors.New("no such character")
	// ErrInvalidName: the name does not follow the rules of the document.
	ErrInvalidName = errors.New("invalid name")
	// ErrNameTaken: the table already has a living character with the name.
	ErrNameTaken = errors.New("name already used in the table")
	// ErrLevelDenied: who asks may not create that.
	ErrLevelDenied = errors.New("the creator may not create this")
	// ErrTableClosed: the table does not take new characters.
	ErrTableClosed = errors.New("the table is closing or over")
	// ErrBadSheet: the sheet or the base does not exist or does not fit.
	ErrBadSheet = errors.New("no such sheet or base")
	// ErrPinned: a pinned character is never ended by the engine or by dismiss.
	ErrPinned = errors.New("the character is pinned")
	// ErrNotVisible: the character is in another table.
	ErrNotVisible = errors.New("the character is in another table")
	// ErrCannotStop: only a table master or an Administrator ends a stuck or
	// interrupted character.
	ErrCannotStop = errors.New("only a table master or an Administrator ends a stuck or interrupted character")
)

// ValidName checks a name: 1 to 32 characters (letters, digits, space, `-`
// and `_`), and not a reserved one.
func ValidName(name string) error {
	n := utf8.RuneCountInString(name)
	if n < 1 || n > 32 {
		return fmt.Errorf("%w: use 1 to 32 characters", ErrInvalidName)
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != ' ' && r != '-' && r != '_' {
			return fmt.Errorf("%w: only letters, digits, space, - and _", ErrInvalidName)
		}
	}
	if strings.TrimSpace(name) != name || strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: no space at the ends", ErrInvalidName)
	}
	if nameKey(name) == nameKey(NameGrandMaster) || nameKey(name) == nameKey(NameTableMaster) {
		return fmt.Errorf("%w: %q is reserved", ErrInvalidName, name)
	}
	return nil
}

// validNameFor checks the name asked for a character of a level: the Grand
// Master and the table masters have the reserved names, and nothing else.
func validNameFor(role Role, name string) error {
	switch {
	case name == "":
		return nil
	case role == RoleGrandMaster && name == NameGrandMaster, role == RoleTableMaster && name == NameTableMaster:
		return nil
	case role != RoleCharacter:
		return fmt.Errorf("%w: a %s is called %q", ErrInvalidName, role, map[Role]string{RoleGrandMaster: NameGrandMaster, RoleTableMaster: NameTableMaster}[role])
	}
	return ValidName(name)
}

// nameKey is the name without difference of case: names are unique in the
// table that way.
func nameKey(name string) string { return strings.ToLower(name) }

// DefaultName is `<sheet> <n>`, with n the first number free in the table
// ("Dev Claude 2"), given the keys of the names in use.
func DefaultName(sheet string, taken func(key string) bool) string {
	base := strings.TrimSpace(sheet)
	var b strings.Builder
	for _, r := range base {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	base = strings.TrimSpace(b.String())
	if base == "" {
		base = "Personagem"
	}
	if r := []rune(base); len(r) > 28 {
		base = strings.TrimSpace(string(r[:28]))
	}
	for n := 1; ; n++ {
		name := fmt.Sprintf("%s %d", base, n)
		if !taken(nameKey(name)) {
			return name
		}
	}
}
