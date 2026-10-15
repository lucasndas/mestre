package grimoires

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/connections"
	"github.com/lucasndas/mestre/internal/orchestration/scrolls"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Options are the pieces of the Service.
type Options struct {
	Store       *Store
	Clock       clock.Clock
	Registry    characters.Registry
	Characters  Characters
	Scrolls     Scrolls
	Connections Connections
	Quests      Quests
	Tables      Tables
	Inbox       Inbox
	Sheets      Sheets
	// Classes may be nil: no class is found, so a grimoire that uses a class
	// of its own cannot be saved or restored (the sheets' classes are enough
	// for the rest).
	Classes Classes
	// Access may be nil: every Administrator and above uses every workspace.
	Access Access
	Logger *slog.Logger
}

// Service saves, lists, deletes and restores the grimoires.
type Service struct {
	o   Options
	st  *Store
	clk clock.Clock
	log *slog.Logger
}

// NewService returns the Service.
func NewService(o Options) (*Service, error) {
	switch {
	case o.Store == nil || o.Store.DB == nil:
		return nil, errors.New("grimoires: the service needs the store")
	case o.Registry.DB == nil:
		return nil, errors.New("grimoires: the service needs the registry of characters")
	case o.Characters == nil || o.Scrolls == nil || o.Connections == nil || o.Quests == nil || o.Tables == nil || o.Inbox == nil || o.Sheets == nil:
		return nil, errors.New("grimoires: the service needs the characters, the scrolls, the connections, the quests, the tables, the inbox and the sheets")
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{o: o, st: o.Store, clk: o.Clock, log: o.Logger}, nil
}

// Store gives access to the table.
func (s *Service) Store() *Store { return s.st }

// MaxName is the longest name of a grimoire.
const MaxName = 80

// Pick is an element of the selection of the canvas: its id (the key of a
// scroll or tome, the id of a character, the name or id of a quest) and where
// it is on the canvas.
type Pick struct {
	ID       string
	Position *Position
}

// Selection is what the user selected on the canvas of the table. A tome takes
// every scroll it holds with it.
type Selection struct {
	Characters, Scrolls, Tomes, Quests []Pick
}

// SaveRequest is the "save" of the panel.
type SaveRequest struct {
	Workspace, Table string
	Name             string
	Select           Selection
	// StructureOnly saves the names, the tomes and the places of the scrolls,
	// without their content.
	StructureOnly bool
	// Replace overwrites a grimoire of the same name; without it that is
	// ErrNameTaken and the user chooses to replace or to pick another name.
	Replace bool
	By      Actor
}

// SaveResult is what was saved and what was left out.
type SaveResult struct {
	Document Document
	// Left are the elements of the selection that never enter a grimoire: the
	// Grand Master and the table master.
	Left []string
}

func (s *Service) allowed(ctx context.Context, by Actor, workspace string) error {
	if !by.admin() {
		return ErrLevelDenied
	}
	if s.o.Access != nil && !s.o.Access.CanUse(ctx, by, workspace) {
		return fmt.Errorf("%w: %s", ErrNoAccess, workspace)
	}
	return nil
}

// Save writes the grimoire of the selection (docs/orquestracao/grimorios.md#salvar).
func (s *Service) Save(ctx context.Context, req SaveRequest) (SaveResult, error) {
	if err := s.allowed(ctx, req.By, req.Workspace); err != nil {
		return SaveResult{}, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || utf8.RuneCountInString(name) > MaxName {
		return SaveResult{}, fmt.Errorf("%w: the name has 1 to %d characters", ErrBadRequest, MaxName)
	}
	if _, err := s.o.Tables.Table(ctx, req.Workspace, req.Table); err != nil {
		return SaveResult{}, fmt.Errorf("%w: table %s: %w", ErrBadRequest, req.Table, err)
	}
	if !req.Replace {
		if _, err := s.st.get(ctx, name); err == nil {
			return SaveResult{}, fmt.Errorf("%w: %s", ErrNameTaken, name)
		} else if !errors.Is(err, ErrNoGrimoire) {
			return SaveResult{}, err
		}
	}
	b := &builder{s: s, ctx: ctx, req: req, doc: Document{Version: Version, Name: name, Author: req.By.author(), SavedAt: s.clk.Now().UTC(),
		Workspace: req.Workspace, Table: req.Table, StructureOnly: req.StructureOnly}}
	if err := b.build(); err != nil {
		return SaveResult{}, err
	}
	if err := s.st.Put(ctx, b.doc, req.Replace, s.clk.Now()); err != nil {
		return SaveResult{}, err
	}
	return SaveResult{Document: b.doc, Left: b.left}, nil
}

// builder reads the selection into a document.
type builder struct {
	s        *Service
	ctx      context.Context
	req      SaveRequest
	doc      Document
	left     []string
	problems []string

	charName map[string]string // id -> name
	scrName  map[string]string // key -> name
	tomeName map[string]string // key -> name
	classes  map[string]bool
}

func (b *builder) bad(format string, a ...any) {
	b.problems = append(b.problems, fmt.Sprintf(format, a...))
}

func (b *builder) build() error {
	b.charName, b.scrName, b.tomeName, b.classes = map[string]string{}, map[string]string{}, map[string]string{}, map[string]bool{}
	b.characters()
	b.scrolls()
	b.connections()
	b.quests()
	b.classPackages()
	if len(b.problems) > 0 {
		return &SelectionError{Problems: b.problems}
	}
	return nil
}

func (b *builder) characters() {
	for _, p := range b.req.Select.Characters {
		c, err := b.s.o.Registry.Get(b.ctx, p.ID)
		switch {
		case err != nil:
			b.bad("character %s does not exist", p.ID)
			continue
		case c.Workspace != b.req.Workspace || c.Table != b.req.Table:
			b.bad("%s is not in the table", c.Name)
			continue
		case c.Role != characters.RoleCharacter:
			b.left = append(b.left, c.Name)
			continue
		case !c.State.Alive():
			b.bad("%s has ended", c.Name)
			continue
		}
		ch := Character{Name: c.Name, Machine: c.Machine, Position: p.Position, Pinned: c.Pinned}
		if c.Kind == characters.KindTerminal {
			ch.Base = c.Base
		} else {
			ch.Sheet = c.Sheet
			if snap, err := b.s.o.Sheets.Snapshot(b.ctx, c.Sheet); err != nil || snap.Sheet.Class != c.Class {
				ch.Class = c.Class
			}
		}
		if ch.Class != "" {
			b.classes[ch.Class] = true
		}
		b.charName[c.ID] = c.Name
		b.doc.Characters = append(b.doc.Characters, ch)
	}
}

func (b *builder) scrolls() {
	st := b.s.o.Scrolls.Store()
	ws, table := b.req.Workspace, b.req.Table
	pos := map[string]*Position{}
	var keys []string
	add := func(key string, p *Position) {
		if _, ok := pos[key]; !ok {
			keys = append(keys, key)
		}
		if p != nil || pos[key] == nil {
			pos[key] = p
		}
	}
	for _, p := range b.req.Select.Tomes {
		t, ok, err := st.GetTome(b.ctx, p.ID)
		if err != nil || !ok || t.Workspace != ws || t.Table != table {
			b.bad("tome %s does not exist in the table", p.ID)
			continue
		}
		b.tomeName[t.Key] = t.Name
		b.doc.Tomes = append(b.doc.Tomes, Tome{Name: t.Name, Position: p.Position})
		members, err := st.InTome(b.ctx, t.Key)
		if err != nil {
			b.bad("tome %s: %v", t.Name, err)
			continue
		}
		for _, n := range members {
			add(n.Key, nil)
		}
	}
	for _, p := range b.req.Select.Scrolls {
		add(p.ID, p.Position)
	}
	for _, key := range keys {
		n, ok, err := st.Get(b.ctx, key)
		switch {
		case err != nil || !ok || n.Workspace != ws || n.Table != table:
			b.bad("scroll %s does not exist in the table", key)
			continue
		case strings.HasPrefix(n.Name, scrolls.QuestScrollName("")) || n.Name == scrolls.HistoryName:
			b.bad("the scroll %q belongs to the engine", n.Name)
			continue
		}
		sc := Scroll{Name: n.Name, Tome: n.TomeName, Position: pos[key]}
		if !b.req.StructureOnly {
			_, text, err := b.s.o.Scrolls.Read(b.ctx, scrolls.ReadRequest{Workspace: ws, Table: table, Actor: scrolls.Engine, Name: n.ID})
			if err != nil {
				b.bad("scroll %s: %v", n.Name, err)
				continue
			}
			sc.Content = text
		}
		b.scrName[n.Key] = n.Name
		b.doc.Scrolls = append(b.doc.Scrolls, sc)
	}
	// A tome that was not selected is not saved, even if a scroll was in it.
	for i := range b.doc.Scrolls {
		sc := &b.doc.Scrolls[i]
		keep := false
		for _, t := range b.doc.Tomes {
			keep = keep || strings.EqualFold(t.Name, sc.Tome)
		}
		if !keep {
			sc.Tome = ""
		}
	}
	// A tome with no scroll is not a thing: the engine removes the empty ones.
	kept := b.doc.Tomes[:0]
	for _, t := range b.doc.Tomes {
		for _, sc := range b.doc.Scrolls {
			if strings.EqualFold(sc.Tome, t.Name) {
				kept = append(kept, t)
				break
			}
		}
	}
	b.doc.Tomes = kept
	for key, name := range b.tomeName {
		saved := false
		for _, t := range b.doc.Tomes {
			saved = saved || t.Name == name
		}
		if !saved {
			delete(b.tomeName, key)
		}
	}
}

// connections keeps the manual connections between elements that were saved;
// the automatic ones are made again by the engine, and one to an element
// outside the selection is dropped.
func (b *builder) connections() {
	all, err := b.s.o.Connections.Store().OfTable(b.ctx, b.req.Workspace, b.req.Table)
	if err != nil {
		b.bad("connections: %v", err)
		return
	}
	end := func(r connections.Ref) (End, bool) {
		switch r.Kind {
		case connections.KindCharacter:
			n, ok := b.charName[r.ID]
			return End{Kind: "character", Name: n}, ok
		case connections.KindScroll:
			n, ok := b.scrName[r.ID]
			return End{Kind: "scroll", Name: n}, ok
		case connections.KindTome:
			n, ok := b.tomeName[r.ID]
			return End{Kind: "tome", Name: n}, ok
		}
		return End{}, false
	}
	for _, c := range all {
		if !c.Has(connections.ReasonManual) {
			continue
		}
		a, okA := end(c.A)
		z, okB := end(c.B)
		if okA && okB {
			b.doc.Connections = append(b.doc.Connections, Connection{A: a, B: z})
		}
	}
}

// quests keeps the definition of the selected quests whose steps use only
// characters of the selection (and sheets, which are cited by name).
func (b *builder) quests() {
	have := map[string]bool{}
	for _, n := range b.charName {
		have[strings.ToLower(n)] = true
	}
	for _, p := range b.req.Select.Quests {
		q, err := b.s.o.Quests.Get(b.ctx, b.req.Workspace, b.req.Table, p.ID)
		if err != nil {
			b.bad("quest %s does not exist in the table", p.ID)
			continue
		}
		var outside []string
		for _, st := range q.Def.Steps {
			if st.Character != "" && !have[strings.ToLower(st.Character)] {
				outside = append(outside, st.Character)
			}
			if st.Class != "" {
				b.classes[st.Class] = true
			}
		}
		if len(outside) > 0 {
			b.bad("quest %s uses characters outside the selection: %s", q.Name, strings.Join(outside, ", "))
			continue
		}
		b.doc.Quests = append(b.doc.Quests, Quest{Definition: q.Def})
	}
}

// classPackages notes the classes the team uses: a reference to a global one,
// the package of one of the workspace or of the table.
func (b *builder) classPackages() {
	names := make([]string, 0, len(b.classes))
	for n := range b.classes {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		if b.s.o.Classes == nil {
			b.bad("class %s cannot be read by this engine", n)
			continue
		}
		info, ok, err := b.s.o.Classes.Find(b.ctx, b.req.Workspace, b.req.Table, n)
		if err != nil || !ok {
			b.bad("class %s was not found", n)
			continue
		}
		cl := Class{Name: info.Name, Scope: info.Scope, App: info.App}
		if info.Scope != classes.ClassGlobal {
			for _, f := range info.Files {
				cl.Files = append(cl.Files, ClassFile{Path: f.Path, Data: f.Data})
			}
		}
		b.doc.Classes = append(b.doc.Classes, cl)
	}
}

// List lists the grimoires of every workspace.
func (s *Service) List(ctx context.Context, by Actor) ([]Summary, error) {
	if !by.admin() {
		return nil, ErrLevelDenied
	}
	return s.st.List(ctx)
}

// Get returns a grimoire.
func (s *Service) Get(ctx context.Context, by Actor, name string) (Document, error) {
	if !by.admin() {
		return Document{}, ErrLevelDenied
	}
	return s.st.Get(ctx, name)
}

// Delete removes a grimoire. Only an explicit request of an Administrator or
// above does it, and the tables that were restored are not touched.
func (s *Service) Delete(ctx context.Context, by Actor, name string) error {
	if !by.admin() {
		return ErrLevelDenied
	}
	return s.st.Delete(ctx, name)
}
