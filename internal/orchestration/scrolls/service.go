package scrolls

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/connections"
	"github.com/lucasndas/mestre/internal/safefile"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Linker is the connections as the scrolls use them: the scroll is connected
// to who made it, its connections go when it does, and what an agent sees is
// computed from them (connections.Service).
type Linker interface {
	Link(ctx context.Context, workspace, table string, a, b connections.Ref, by string) error
	Forget(ctx context.Context, ref connections.Ref) error
	CanSeeScroll(ctx context.Context, agentID, scroll string) (bool, error)
	CanSeeTome(ctx context.Context, agentID, tome string) (bool, error)
}

// QuestNames gives the name of a quest, for the scroll of its summaries
// ("Missão: <nome>"). Nil: the id of the quest is used.
type QuestNames interface {
	QuestName(ctx context.Context, quest string) (string, bool)
}

// ChangeKind says what changed in a scroll.
type ChangeKind string

// The changes the canvas is told of, at once.
const (
	Created ChangeKind = "created"
	Changed ChangeKind = "changed"
	Stacked ChangeKind = "stacked"
	Deleted ChangeKind = "deleted"
)

// Change is told to OnChange after every change, in the order the changes were
// applied (docs/painel/canvas.md shows them without a reload).
type Change struct {
	Kind ChangeKind
	Note Note
}

// Options are the pieces of the Service.
type Options struct {
	Store    *Store
	Clock    clock.Clock
	Registry characters.Registry
	// Principal gives the folder of the main clone of a workspace on this
	// machine, the source of the files of the scrolls.
	Principal func(workspace string) string
	// Tables, when set, is asked whether a table exists.
	Tables characters.Tables
	// Connections may be nil: no connection is made, and every scroll is
	// visible to every character of the table.
	Connections Linker
	// Targets and Copies keep the read-only copies in the worktrees where
	// there are characters; nil: no copies.
	Targets Targets
	Copies  Copies
	// Quests may be nil.
	Quests QuestNames
	// OnChange may be nil.
	OnChange func(Change)
	// MaxSize defaults to 256 KiB.
	MaxSize int
	Logger  *slog.Logger
}

// Service keeps the scrolls and tomes of the tables
// (docs/orquestracao/pergaminhos-e-tomos.md): the file of each scroll in the
// main clone, the index in the database, the operations one at a time in the
// order they arrive, and the copies for the characters.
type Service struct {
	o   Options
	st  *Store
	clk clock.Clock
	log *slog.Logger
	max int

	// mu applies the changes one at a time.
	mu sync.Mutex
	// cmu serializes the copies among themselves (not the reads and writes).
	cmu sync.Mutex
}

var (
	_ connections.Scrolls = (*Service)(nil)
	_ characters.Scrolls  = (*Service)(nil)
)

// NewService returns the Service.
func NewService(o Options) (*Service, error) {
	switch {
	case o.Store == nil || o.Store.DB == nil:
		return nil, errors.New("scrolls: the service needs the store")
	case o.Principal == nil:
		return nil, errors.New("scrolls: the service needs to know where the main clone of a workspace is")
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.MaxSize <= 0 {
		o.MaxSize = DefaultMaxSize
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{o: o, st: o.Store, clk: o.Clock, log: o.Logger, max: o.MaxSize}, nil
}

// Store gives access to the tables.
func (s *Service) Store() *Store { return s.st }

// File is the path of the file of a scroll in the main clone.
func (s *Service) File(workspace, table, id string) string {
	return filepath.Join(s.o.Principal(workspace), filepath.FromSlash(Rel(table, id)))
}

func (s *Service) emit(k ChangeKind, n Note) {
	if s.o.OnChange != nil {
		s.o.OnChange(Change{Kind: k, Note: n})
	}
}

// where checks the table of a request: scrolls exist only inside tables.
func (s *Service) where(ctx context.Context, workspace, table string) error {
	if table == "" {
		return ErrNoTable
	}
	if err := checkPart("workspace", workspace); err != nil {
		return err
	}
	if err := checkPart("table", table); err != nil {
		return err
	}
	if s.o.Tables != nil {
		if _, err := s.o.Tables.Table(ctx, workspace, table); err != nil {
			return fmt.Errorf("%w: table %s", ErrNoTable, table)
		}
	}
	return nil
}

// find resolves a name or an id in the table, for who asks: a scroll that does
// not exist and one the character cannot see are refused alike, with the
// name that was asked for.
func (s *Service) find(ctx context.Context, workspace, table string, a Actor, name string) (Note, error) {
	n, ok, err := s.st.Find(ctx, workspace, table, name)
	if err != nil {
		return Note{}, err
	}
	if !ok {
		return Note{}, notVisible(name)
	}
	if a.Kind == ByCharacter && s.o.Connections != nil {
		seen, err := s.o.Connections.CanSeeScroll(ctx, a.ID, n.Key)
		if err != nil {
			return Note{}, err
		}
		if !seen {
			return Note{}, notVisible(name)
		}
	}
	return n, nil
}

func (s *Service) readFile(n Note) (string, error) {
	data, _, err := safefile.Read(s.File(n.Workspace, n.Table, n.ID), int64(s.max))
	if err != nil {
		return "", fmt.Errorf("scroll %s: %w", n.Name, err)
	}
	return string(data), nil
}

func (s *Service) writeFile(n Note, content string) error {
	// The file is flushed like the database it goes with: a database opened
	// without flushes (the tests that are not about durability, as in
	// store.Options.Synchronous) has nothing to flush here either.
	return safefile.Write(s.File(n.Workspace, n.Table, n.ID), []byte(content), safefile.Options{Sync: s.st.DB.Flushes()})
}

// ReadRequest names a scroll.
type ReadRequest struct {
	Workspace, Table string
	Actor            Actor
	Name             string
}

// Read returns the scroll and its current content.
func (s *Service) Read(ctx context.Context, r ReadRequest) (Note, string, error) {
	if err := s.where(ctx, r.Workspace, r.Table); err != nil {
		return Note{}, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.find(ctx, r.Workspace, r.Table, r.Actor, r.Name)
	if err != nil {
		return Note{}, "", err
	}
	content, err := s.readFile(n)
	return n, content, err
}

// CreateRequest is `note create`.
type CreateRequest struct {
	Workspace, Table string
	Actor            Actor
	Content          string
	// Name, when set, is the fixed name of the scroll.
	Name string
	// Stack is the tome to put it in (made if it does not exist).
	Stack string
}

// Create makes a scroll and connects it to who made it.
func (s *Service) Create(ctx context.Context, r CreateRequest) (Note, error) {
	if err := s.where(ctx, r.Workspace, r.Table); err != nil {
		return Note{}, err
	}
	if !r.Actor.canWrite() {
		return Note{}, ErrLevelDenied
	}
	if size := int64(len(r.Content)); size > int64(s.max) {
		return Note{}, &SizeError{Name: orUntitled(r.Name, r.Content), Size: size, Limit: int64(s.max)}
	}
	s.mu.Lock()
	n, err := s.createLocked(ctx, r)
	s.mu.Unlock()
	if err != nil {
		return Note{}, err
	}
	// the copies go out with the lock released: a Tower is reached over the network
	s.copyOut(ctx, n)
	s.emit(Created, n)
	return n, nil
}

// createLocked makes the scroll: the file first, then the row of the index (a
// row that cites a file that is not there must never be seen, nor sent to a
// replica). The lock is held.
func (s *Service) createLocked(ctx context.Context, r CreateRequest) (Note, error) {
	now := s.clk.Now()
	var n Note
	written := false
	err := s.st.exec(ctx, func(tx *sql.Tx) error {
		name, fixed := r.Name, r.Name != ""
		if fixed {
			taken, err := nameTaken(ctx, tx, r.Workspace, r.Table, name, "")
			if err != nil {
				return err
			}
			if taken {
				return fmt.Errorf("%w: %s", ErrNameTaken, name)
			}
		} else {
			var err error
			if name, err = uniqueName(ctx, tx, r.Workspace, r.Table, deriveName(r.Content), ""); err != nil {
				return err
			}
		}
		num, err := next(ctx, tx, "scrolls", r.Workspace, r.Table)
		if err != nil {
			return err
		}
		id := idOf(num)
		tome := ""
		if r.Stack != "" {
			if tome, err = tomeFor(ctx, tx, r.Workspace, r.Table, r.Stack, now.UnixMilli()); err != nil {
				return err
			}
		}
		n = Note{Key: keyOf(r.Workspace, r.Table, id), Workspace: r.Workspace, Table: r.Table, ID: id, Name: name, FixedName: fixed, Tome: tome,
			Revision: 1, Size: int64(len(r.Content)), Author: r.Actor.author(), AuthorID: r.Actor.ID, CreatedAt: now, UpdatedAt: now}
		if err := s.writeFile(n, r.Content); err != nil {
			return err
		}
		written = true
		_, err = tx.ExecContext(ctx, `INSERT INTO notes (key, workspace, table_id, id, name, name_key, fixed_name, tome, revision, size, author, author_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?)`, n.Key, n.Workspace, n.Table, n.ID, n.Name, nameKey(n.Name), boolInt(fixed), n.Tome, n.Size, n.Author, n.AuthorID, now.UnixMilli(), now.UnixMilli())
		return err
	})
	if err != nil {
		if written { // the index refused it: the file must not stay
			if rerr := safefile.Remove(s.File(n.Workspace, n.Table, n.ID), nil); rerr != nil {
				s.log.Warn("scrolls: the file of a scroll the index refused was not removed", "scroll", n.Key, "err", rerr)
			}
		}
		return Note{}, err
	}
	if n, _, err = s.reload(ctx, n.Key); err != nil {
		return Note{}, err
	}
	if r.Actor.Kind == ByCharacter && s.o.Connections != nil {
		if err := s.o.Connections.Link(ctx, n.Workspace, n.Table, connections.Char(r.Actor.ID), connections.Scroll(n.Key), r.Actor.ID); err != nil {
			s.log.Warn("scrolls: the scroll was not connected to its author", "scroll", n.Key, "err", err)
		}
	}
	return n, nil
}

func orUntitled(name, content string) string {
	if name != "" {
		return name
	}
	return deriveName(content)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Service) reload(ctx context.Context, key string) (Note, bool, error) {
	return s.st.Get(ctx, key)
}

// tomeFor finds the tome of a table by name, or makes it.
func tomeFor(ctx context.Context, tx *sql.Tx, workspace, table, name string, at int64) (string, error) {
	t, ok, err := findTome(ctx, tx, `workspace = ? AND table_id = ? AND name_key = ?`, workspace, table, nameKey(name))
	if err != nil {
		return "", err
	}
	if ok {
		return t.Key, nil
	}
	num, err := next(ctx, tx, "tomes", workspace, table)
	if err != nil {
		return "", err
	}
	id := tomeIDOf(num)
	key := keyOf(workspace, table, id)
	_, err = tx.ExecContext(ctx, `INSERT INTO tomes (key, workspace, table_id, id, name, name_key, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		key, workspace, table, id, strings.TrimSpace(name), nameKey(name), at)
	return key, err
}

// WriteRequest is `note write`.
type WriteRequest struct {
	Workspace, Table string
	Actor            Actor
	Name             string
	Content          string
	// Append adds the content to the end instead of replacing everything.
	Append bool
	// ExpectRev, when set, makes the write fail if the scroll is not at that
	// revision (somebody changed it first).
	ExpectRev *int64
}

// Write replaces the content of a scroll (or adds to it). The revision grows
// by one.
func (s *Service) Write(ctx context.Context, r WriteRequest) (Note, error) {
	return s.change(ctx, r.Workspace, r.Table, r.Actor, r.Name, r.ExpectRev, func(cur string) (string, error) {
		if r.Append {
			return cur + r.Content, nil
		}
		return r.Content, nil
	})
}

// EditRequest is `note edit`.
type EditRequest struct {
	Workspace, Table string
	Actor            Actor
	Name             string
	Old, New         string
	ExpectRev        *int64
}

// Edit swaps an exact piece for another; the piece has to appear once. Nothing
// changes otherwise.
func (s *Service) Edit(ctx context.Context, r EditRequest) (Note, error) {
	if r.Old == "" {
		return Note{}, fmt.Errorf("%w: the piece to replace is empty", ErrBadRequest)
	}
	return s.change(ctx, r.Workspace, r.Table, r.Actor, r.Name, r.ExpectRev, func(cur string) (string, error) {
		switch strings.Count(cur, r.Old) {
		case 0:
			return "", fmt.Errorf("%w: %q", ErrEditNotFound, excerpt(r.Old))
		case 1:
			return strings.Replace(cur, r.Old, r.New, 1), nil
		}
		return "", fmt.Errorf("%w: %q", ErrEditAmbiguous, excerpt(r.Old))
	})
}

func excerpt(s string) string {
	if r := []rune(s); len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return s
}

// change is the one way a content changes: under the lock, against the
// revision, within the limit, file first and then the index.
func (s *Service) change(ctx context.Context, workspace, table string, a Actor, name string, expect *int64, f func(cur string) (string, error)) (Note, error) {
	if err := s.where(ctx, workspace, table); err != nil {
		return Note{}, err
	}
	if !a.canWrite() {
		return Note{}, ErrLevelDenied
	}
	s.mu.Lock()
	n, err := s.find(ctx, workspace, table, a, name)
	if err != nil {
		s.mu.Unlock()
		return Note{}, err
	}
	out, _, err := s.apply(ctx, n, a, expect, f)
	s.mu.Unlock()
	if err != nil {
		return Note{}, err
	}
	s.copyOut(ctx, out) // with the lock released
	s.emit(Changed, out)
	return out, nil
}

// apply changes the content of a scroll that was found. The lock is held.
func (s *Service) apply(ctx context.Context, n Note, a Actor, expect *int64, f func(cur string) (string, error)) (Note, string, error) {
	if expect != nil && *expect != n.Revision {
		return Note{}, "", &ConflictError{Name: n.Name, Expected: *expect, Have: n.Revision}
	}
	cur, err := s.readFile(n)
	if err != nil {
		return Note{}, "", err
	}
	next, err := f(cur)
	if err != nil {
		return Note{}, "", err
	}
	if size := int64(len(next)); size > int64(s.max) {
		return Note{}, "", &SizeError{Name: n.Name, Size: size, Limit: int64(s.max)}
	}
	now := s.clk.Now()
	name := n.Name
	written := false
	err = s.st.exec(ctx, func(tx *sql.Tx) error {
		if !n.FixedName {
			var err error
			if name, err = uniqueName(ctx, tx, n.Workspace, n.Table, deriveName(next), n.Key); err != nil {
				return err
			}
		}
		// the file first, then the row: the index never cites a file that is
		// not there yet
		if err := s.writeFile(n, next); err != nil {
			return err
		}
		written = true
		_, err := tx.ExecContext(ctx, `UPDATE notes SET name = ?, name_key = ?, revision = revision + 1, size = ?, author = ?, author_id = ?, updated_at = ? WHERE key = ? AND revision = ?`,
			name, nameKey(name), int64(len(next)), a.author(), a.ID, now.UnixMilli(), n.Key, n.Revision)
		return err
	})
	if err != nil {
		if written { // the index refused it: put the old content back in the file
			if rerr := s.writeFile(n, cur); rerr != nil {
				s.log.Error("scrolls: the file could not be put back after an index that refused the change", "scroll", n.Key, "err", rerr)
			}
		}
		return Note{}, "", err
	}
	out, _, err := s.reload(ctx, n.Key)
	if err != nil {
		return Note{}, "", err
	}
	return out, next, nil
}

// StackRequest is `note stack`.
type StackRequest struct {
	Workspace, Table string
	Actor            Actor
	// Tome is the name of the tome (made if new) and Names the scrolls.
	Tome  string
	Names []string
}

// Stack puts scrolls in a tome. A scroll is in at most one tome, so it leaves
// the one it was in, and a tome left empty is removed. A scroll of another
// table is refused.
func (s *Service) Stack(ctx context.Context, r StackRequest) (Tome, []Note, error) {
	if err := s.where(ctx, r.Workspace, r.Table); err != nil {
		return Tome{}, nil, err
	}
	if !r.Actor.canWrite() {
		return Tome{}, nil, ErrLevelDenied
	}
	if strings.TrimSpace(r.Tome) == "" || len(r.Names) == 0 {
		return Tome{}, nil, fmt.Errorf("%w: a tome and at least one scroll", ErrBadRequest)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// A tome that exists is stacked into only by who sees it: a scroll put in
	// it becomes visible to everyone connected to the tome.
	if r.Actor.Kind == ByCharacter && s.o.Connections != nil {
		if t, ok, err := s.st.FindTome(ctx, r.Workspace, r.Table, r.Tome); err != nil {
			return Tome{}, nil, err
		} else if ok {
			seen, err := s.o.Connections.CanSeeTome(ctx, r.Actor.ID, t.Key)
			if err != nil {
				return Tome{}, nil, err
			}
			if !seen {
				return Tome{}, nil, notVisible(r.Tome)
			}
		}
	}
	var notes []Note
	for _, name := range r.Names {
		if strings.Contains(name, "/") { // a key: only of this table
			if n, ok, err := s.st.Get(ctx, name); err != nil {
				return Tome{}, nil, err
			} else if ok && (n.Workspace != r.Workspace || n.Table != r.Table) {
				return Tome{}, nil, fmt.Errorf("%w: %s", ErrOtherTable, name)
			}
		}
		n, err := s.find(ctx, r.Workspace, r.Table, r.Actor, name)
		if err != nil {
			return Tome{}, nil, err
		}
		notes = append(notes, n)
	}
	now := s.clk.Now()
	var tomeKey string
	var emptied []string
	err := s.st.exec(ctx, func(tx *sql.Tx) error {
		var err error
		if tomeKey, err = tomeFor(ctx, tx, r.Workspace, r.Table, r.Tome, now.UnixMilli()); err != nil {
			return err
		}
		for _, n := range notes {
			if _, err := tx.ExecContext(ctx, `UPDATE notes SET tome = ? WHERE key = ?`, tomeKey, n.Key); err != nil {
				return err
			}
		}
		// Tomes left with nothing go away.
		rows, err := tx.QueryContext(ctx, `SELECT key FROM tomes WHERE workspace = ? AND table_id = ? AND key NOT IN (SELECT tome FROM notes WHERE tome <> '')`, r.Workspace, r.Table)
		if err != nil {
			return err
		}
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				rows.Close()
				return err
			}
			emptied = append(emptied, k)
		}
		rows.Close()
		for _, k := range emptied {
			if _, err := tx.ExecContext(ctx, `DELETE FROM tomes WHERE key = ?`, k); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Tome{}, nil, err
	}
	s.forget(ctx, emptied)
	tome, _, err := s.st.GetTome(ctx, tomeKey)
	if err != nil {
		return Tome{}, nil, err
	}
	var out []Note
	for _, n := range notes {
		fresh, _, err := s.reload(ctx, n.Key)
		if err != nil {
			return Tome{}, nil, err
		}
		out = append(out, fresh)
		s.emit(Stacked, fresh)
	}
	return tome, out, nil
}

// forget takes the connections of tomes that no longer exist.
func (s *Service) forget(ctx context.Context, tomes []string) {
	if s.o.Connections == nil {
		return
	}
	for _, k := range tomes {
		if err := s.o.Connections.Forget(ctx, connections.Tome(k)); err != nil {
			s.log.Warn("scrolls: the connections of a removed tome were not taken away", "tome", k, "err", err)
		}
	}
}

// DeleteRequest is `note delete`.
type DeleteRequest struct {
	Workspace, Table string
	Actor            Actor
	Name             string
	// Approved is set by the flow of approval when an authorized user asked for
	// the deletion explicitly (milestone 5).
	Approved bool
	// ExpectKey and ExpectRevision, when set, are the scroll and the revision the
	// approval was given for: any other is refused (ErrRevision).
	ExpectKey      string
	ExpectRevision int64
}

// Delete removes a scroll. Only on the explicit request of an authorized user
// (docs/cli/convencoes.md): a user Administrator or above does it; a character
// needs the approval, which the flow brings, and is otherwise refused with
// ErrApproval.
func (s *Service) Delete(ctx context.Context, r DeleteRequest) error {
	if err := s.where(ctx, r.Workspace, r.Table); err != nil {
		return err
	}
	switch {
	case r.Actor.Kind == ByUser && !r.Actor.canWrite():
		return ErrLevelDenied
	case r.Actor.Kind != ByUser && !r.Approved:
		return ErrApproval
	}
	s.mu.Lock()
	n, err := s.deleteLocked(ctx, r)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.dropCopies(ctx, n) // with the lock released
	s.emit(Deleted, n)
	return nil
}

func (s *Service) deleteLocked(ctx context.Context, r DeleteRequest) (Note, error) {
	n, err := s.find(ctx, r.Workspace, r.Table, r.Actor, r.Name)
	if err != nil {
		return Note{}, err
	}
	if r.ExpectKey != "" && (n.Key != r.ExpectKey || n.Revision != r.ExpectRevision) {
		return Note{}, ErrRevision
	}
	var emptied []string
	err = s.st.exec(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM notes WHERE key = ?`, n.Key); err != nil {
			return err
		}
		if n.Tome == "" {
			return nil
		}
		var left int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM notes WHERE tome = ?`, n.Tome).Scan(&left); err != nil {
			return err
		}
		if left == 0 {
			emptied = append(emptied, n.Tome)
			_, err := tx.ExecContext(ctx, `DELETE FROM tomes WHERE key = ?`, n.Tome)
			return err
		}
		return nil
	})
	if err != nil {
		return Note{}, err
	}
	if err := safefile.Remove(s.File(n.Workspace, n.Table, n.ID), nil); err != nil {
		s.log.Warn("scrolls: the file of a deleted scroll was not removed", "scroll", n.Key, "err", err)
	}
	if s.o.Connections != nil {
		if err := s.o.Connections.Forget(ctx, connections.Scroll(n.Key)); err != nil {
			s.log.Warn("scrolls: the connections of a deleted scroll were not taken away", "scroll", n.Key, "err", err)
		}
	}
	s.forget(ctx, emptied)
	return n, nil
}

// --- connections.Scrolls: what the connections need to know ---

// Resolve implements connections.Scrolls: a scroll of the table by name or id,
// else a tome.
func (s *Service) Resolve(ctx context.Context, workspace, table, nameOrID string) (connections.ScrollRef, bool, error) {
	if n, ok, err := s.st.Find(ctx, workspace, table, nameOrID); err != nil || ok {
		return connections.ScrollRef{ID: n.Key, Name: n.Name, Table: n.Table, Stack: n.TomeName}, ok, err
	}
	t, ok, err := s.st.FindTome(ctx, workspace, table, nameOrID)
	if err != nil || !ok {
		return connections.ScrollRef{}, false, err
	}
	return connections.ScrollRef{ID: t.Key, Name: t.Name, Table: t.Table, Tome: true}, true, nil
}

// Get implements connections.Scrolls.
func (s *Service) Get(ctx context.Context, ref connections.Ref) (connections.ScrollRef, bool, error) {
	if ref.Kind == connections.KindTome {
		t, ok, err := s.st.GetTome(ctx, ref.ID)
		return connections.ScrollRef{ID: t.Key, Name: t.Name, Table: t.Table, Tome: true}, ok, err
	}
	n, ok, err := s.st.Get(ctx, ref.ID)
	return connections.ScrollRef{ID: n.Key, Name: n.Name, Table: n.Table, Stack: n.TomeName}, ok, err
}

// Members implements connections.Scrolls: the scrolls a tome holds now, the
// ones that join it later included.
func (s *Service) Members(ctx context.Context, tome string) ([]string, error) {
	ns, err := s.st.InTome(ctx, tome)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Key
	}
	return out, nil
}
