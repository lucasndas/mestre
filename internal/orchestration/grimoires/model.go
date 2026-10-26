package grimoires

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/quests"
)

// Version is the version of the document the engine writes and reads.
const Version = 1

// Position is where an element is on the canvas. The engine keeps it in the
// grimoire and hands it back when it restores, for the panel to place the
// elements (the canvas is milestone 6).
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Document is the recipe of a grimoire (version 1): what the team is, never
// what it did. There is no process, session of a provider, inbox, wait, use,
// revision, state of a quest, round or number of issue in it.
type Document struct {
	Version int    `json:"version"`
	Name    string `json:"name"`
	Author  string `json:"author"`
	// SavedAt, Workspace and Table are where and when it was saved.
	SavedAt   time.Time `json:"saved_at"`
	Workspace string    `json:"workspace"`
	Table     string    `json:"table"`
	// StructureOnly says the scrolls were saved without their content.
	StructureOnly bool         `json:"structure_only,omitempty"`
	Characters    []Character  `json:"characters,omitempty"`
	Classes       []Class      `json:"classes,omitempty"`
	Tomes         []Tome       `json:"tomes,omitempty"`
	Scrolls       []Scroll     `json:"scrolls,omitempty"`
	Connections   []Connection `json:"connections,omitempty"`
	Quests        []Quest      `json:"quests,omitempty"`
}

// Character is a character or a simple terminal of the team.
type Character struct {
	Name string `json:"name"`
	// Sheet makes an agent, Base a simple terminal.
	Sheet string `json:"sheet,omitempty"`
	Base  string `json:"base,omitempty"`
	// Class is set only when it differs from the class of the sheet.
	Class string `json:"class,omitempty"`
	// Machine is the machine it ran on, a preference when it is restored.
	Machine  string    `json:"machine,omitempty"`
	Position *Position `json:"position,omitempty"`
	Pinned   bool      `json:"pinned,omitempty"`
}

// Class is a class the team uses. A global one is only a reference; one of the
// workspace or of the table carries a copy of the package.
type Class struct {
	Name  string             `json:"name"`
	Scope classes.ClassScope `json:"scope"`
	// App is the App linked to the class, as CLASS.md writes it.
	App   string      `json:"app,omitempty"`
	Files []ClassFile `json:"files,omitempty"`
}

// ClassFile is a file of the package of a class.
type ClassFile struct {
	Path string `json:"path"`
	Data []byte `json:"data"`
}

// Tome is a group of scrolls.
type Tome struct {
	Name     string    `json:"name"`
	Position *Position `json:"position,omitempty"`
}

// Scroll is a scroll with its content (empty when only the structure is saved).
type Scroll struct {
	Name     string    `json:"name"`
	Content  string    `json:"content,omitempty"`
	Tome     string    `json:"tome,omitempty"`
	Position *Position `json:"position,omitempty"`
}

// Connection is a manual connection between two saved elements, which are
// named: a character, a scroll or a tome by its name.
type Connection struct {
	A End `json:"a"`
	B End `json:"b"`
}

// End is one end of a connection.
type End struct {
	Kind string `json:"kind"` // character, scroll or tome
	Name string `json:"name"`
}

// Quest is the definition of a quest (docs/orquestracao/missoes.md#definição),
// nothing of its state.
type Quest struct {
	Definition quests.Definition `json:"definition"`
}

// Summary is a grimoire as the list shows it.
type Summary struct {
	Name, Author, Workspace, Table string
	SavedAt                        time.Time
	Version                        int
	Characters, Classes, Scrolls   int
	Tomes, Connections, Quests     int
}

// Actor is who asks: a user of the panel.
type Actor struct {
	ID, Name string
	Level    characters.UserLevel
}

func (a Actor) author() string {
	if a.Name != "" {
		return a.Name
	}
	return a.ID
}

func (a Actor) admin() bool {
	return a.Level == characters.LevelOwner || a.Level == characters.LevelAdministrator
}

// ClassChoice is what the user decides about a class of the workspace of
// origin that the destination does not have.
type ClassChoice string

// The three options the user is given.
const (
	// ChoiceCopy copies the class, from the package, as a class of the table.
	ChoiceCopy ClassChoice = "copy"
	// ChoiceMap uses another class that the destination has.
	ChoiceMap ClassChoice = "map"
	// ChoiceCancel cancels the restoration.
	ChoiceCancel ClassChoice = "cancel"
)

// Choice is the decision about one class; MapTo names the class of ChoiceMap.
type Choice struct {
	Action ClassChoice
	MapTo  string
}

// How a class of the grimoire is resolved in the destination.
const (
	// ResolvedSame: a global class, the same everywhere.
	ResolvedSame = "same"
	// ResolvedDestination: the destination has a class of that name; it is used.
	ResolvedDestination = "destination"
	// ResolvedCopy: the package is copied as a class of the table.
	ResolvedCopy = "copy"
	// ResolvedMapped: the user chose another class of the destination.
	ResolvedMapped = "mapped"
	// ResolvedChoose: the user has to choose (copy, map or cancel).
	ResolvedChoose = "choose"
)

// ClassPlan is how one class will be resolved.
type ClassPlan struct {
	Name       string
	Origin     classes.ClassScope
	Resolution string
	// MapTo is the class of the destination that stands for it (ResolvedMapped).
	MapTo string
	// Options are the choices offered (ResolvedChoose).
	Options []ClassChoice
	// CopyExcess lists what the copy would give beyond the ceiling of the
	// destination, for the user to know before choosing to copy.
	CopyExcess []string
}

// Rename is an element that gets another name in the destination.
type Rename struct{ From, To string }

// Plan is what a restoration would do: the validation (nothing is created) and
// the summary the user confirms.
type Plan struct {
	Grimoire         string
	Workspace, Table string
	Classes          []ClassPlan
	Characters       []Rename
	Scrolls          []Rename
	Tomes            []Rename
	Quests           []Rename
	// Room is how many characters the limits of the table and of the workspace
	// still allow.
	Room int
}

// Renamed lists the names that changed, for the summary: "Dev -> Dev (2)".
func (p Plan) Renamed() []string {
	var out []string
	for _, group := range [][]Rename{p.Characters, p.Scrolls, p.Tomes, p.Quests} {
		for _, r := range group {
			if r.From != r.To {
				out = append(out, fmt.Sprintf("%s -> %s", r.From, r.To))
			}
		}
	}
	return out
}

// Pending lists the classes that wait for a choice.
func (p Plan) Pending() []ClassPlan {
	var out []ClassPlan
	for _, c := range p.Classes {
		if c.Resolution == ResolvedChoose {
			out = append(out, c)
		}
	}
	return out
}

// Summary writes the confirmation text.
func (p Plan) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Restaurar o grimório %q na mesa %s: %d personagem(ns), %d pergaminho(s), %d tomo(s), %d missão(ões)", p.Grimoire, p.Table,
		len(p.Characters), len(p.Scrolls), len(p.Tomes), len(p.Quests))
	copies := 0
	for _, c := range p.Classes {
		if c.Resolution == ResolvedCopy {
			copies++
		}
	}
	if copies > 0 {
		fmt.Fprintf(&b, ", %d classe(s) copiada(s) como classe da mesa", copies)
	}
	b.WriteString(".")
	if r := p.Renamed(); len(r) > 0 {
		b.WriteString("\nNomes que mudam: " + strings.Join(r, "; ") + ".")
	}
	return b.String()
}

// Errors of the grimoires.
var (
	// ErrLevelDenied: only an Administrator or above saves, restores and deletes.
	ErrLevelDenied = errors.New("only an Administrator or above may do this")
	// ErrNoGrimoire: no such grimoire.
	ErrNoGrimoire = errors.New("no such grimoire")
	// ErrNameTaken: a grimoire has the name; the user replaces it or picks another.
	ErrNameTaken = errors.New("a grimoire already has this name")
	// ErrBadRequest: the request does not make sense.
	ErrBadRequest = errors.New("the request is not valid")
	// ErrTableState: a grimoire is restored in a table that is working or in review.
	ErrTableState = errors.New("a grimoire is restored in a table that is working or in review")
	// ErrCancelled: the user chose to cancel the restoration.
	ErrCancelled = errors.New("the restoration was cancelled")
	// ErrNoAccess: the user has no access to the destination workspace.
	ErrNoAccess = errors.New("no access to the workspace")
)

// VersionError: the grimoire is of a version this engine does not read.
type VersionError struct{ Have, Max int }

func (e *VersionError) Error() string {
	return fmt.Sprintf("the grimoire has version %d and this engine reads up to version %d", e.Have, e.Max)
}

// MissingSheetsError: sheets the grimoire cites do not exist in the Guild.
type MissingSheetsError struct{ Sheets []string }

func (e *MissingSheetsError) Error() string {
	return "the sheets the grimoire cites do not exist: " + strings.Join(e.Sheets, ", ")
}

// MissingClassesError: classes the grimoire cites by reference do not exist in
// the destination or a class chosen for a mapping does not.
type MissingClassesError struct{ Classes []string }

func (e *MissingClassesError) Error() string {
	return "the classes are not in the destination: " + strings.Join(e.Classes, ", ")
}

// CeilingError: copied classes do not fit in the ceiling of the destination.
type CeilingError struct{ Items []string }

func (e *CeilingError) Error() string {
	return "the copied classes pass the ceiling of the workspace: " + strings.Join(e.Items, "; ")
}

// LimitError: the characters of the grimoire do not fit in the limits.
type LimitError struct{ Need, Room int }

func (e *LimitError) Error() string {
	return fmt.Sprintf("the team has %d characters and the limits of the table and of the workspace leave room for %d", e.Need, e.Room)
}

// ChoiceRequiredError: classes of the workspace of origin wait for the user to
// choose between copying, mapping and cancelling.
type ChoiceRequiredError struct{ Classes []ClassPlan }

func (e *ChoiceRequiredError) Error() string {
	names := make([]string, len(e.Classes))
	for i, c := range e.Classes {
		names[i] = c.Name
	}
	return "choose what to do with the classes of the workspace of origin that the destination does not have (copy as a class of the table, map to another class, or cancel): " + strings.Join(names, ", ")
}

// SelectionError: something in the selection cannot be saved.
type SelectionError struct{ Problems []string }

func (e *SelectionError) Error() string {
	return "the selection cannot be saved: " + strings.Join(e.Problems, "; ")
}
