package scrolls

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mestre-ia/mestre/internal/store"
)

// Store is the notes, tomes and note_counters tables of the Guild database.
type Store struct{ DB *store.DB }

func fromMs(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}

type scanner interface{ Scan(...any) error }

const noteColumns = `n.key, n.workspace, n.table_id, n.id, n.name, n.fixed_name, n.tome, COALESCE(t.name, ''), n.revision, n.size, n.author, n.author_id, n.created_at, n.updated_at`

const noteFrom = ` FROM notes n LEFT JOIN tomes t ON t.key = n.tome`

func scanNote(sc scanner) (Note, error) {
	var (
		n            Note
		fixed        int
		created, upd int64
	)
	if err := sc.Scan(&n.Key, &n.Workspace, &n.Table, &n.ID, &n.Name, &fixed, &n.Tome, &n.TomeName, &n.Revision, &n.Size, &n.Author, &n.AuthorID, &created, &upd); err != nil {
		return Note{}, err
	}
	n.FixedName = fixed != 0
	n.CreatedAt, n.UpdatedAt = fromMs(created), fromMs(upd)
	return n, nil
}

func (s *Store) notes(ctx context.Context, q querier, where string, args ...any) ([]Note, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+noteColumns+noteFrom+` WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Get returns the scroll with the key.
func (s *Store) Get(ctx context.Context, key string) (Note, bool, error) {
	ns, err := s.notes(ctx, s.DB.Reader(), `n.key = ?`, key)
	if err != nil || len(ns) == 0 {
		return Note{}, false, err
	}
	return ns[0], true, nil
}

// Find finds a scroll of a table by name (without difference of case) or id.
func (s *Store) Find(ctx context.Context, workspace, table, nameOrID string) (Note, bool, error) {
	ns, err := s.notes(ctx, s.DB.Reader(), `n.workspace = ? AND n.table_id = ? AND (n.id = ? OR n.name_key = ?)`, workspace, table, nameOrID, nameKey(nameOrID))
	if err != nil || len(ns) == 0 {
		return Note{}, false, err
	}
	return ns[0], true, nil
}

// OfTable lists the scrolls of a table, by id.
func (s *Store) OfTable(ctx context.Context, workspace, table string) ([]Note, error) {
	return s.notes(ctx, s.DB.Reader(), `n.workspace = ? AND n.table_id = ? ORDER BY n.id`, workspace, table)
}

// InTome lists the scrolls a tome holds now.
func (s *Store) InTome(ctx context.Context, tome string) ([]Note, error) {
	return s.notes(ctx, s.DB.Reader(), `n.tome = ? ORDER BY n.id`, tome)
}

// FindTome finds a tome of a table by name or id.
func (s *Store) FindTome(ctx context.Context, workspace, table, nameOrID string) (Tome, bool, error) {
	return findTome(ctx, s.DB.Reader(), `workspace = ? AND table_id = ? AND (id = ? OR name_key = ?)`, workspace, table, nameOrID, nameKey(nameOrID))
}

// GetTome returns the tome with the key.
func (s *Store) GetTome(ctx context.Context, key string) (Tome, bool, error) {
	return findTome(ctx, s.DB.Reader(), `key = ?`, key)
}

func findTome(ctx context.Context, q querier, where string, args ...any) (Tome, bool, error) {
	var t Tome
	var created int64
	err := q.QueryRowContext(ctx, `SELECT key, workspace, table_id, id, name, created_at FROM tomes WHERE `+where, args...).Scan(&t.Key, &t.Workspace, &t.Table, &t.ID, &t.Name, &created)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Tome{}, false, nil
	case err != nil:
		return Tome{}, false, err
	}
	t.CreatedAt = fromMs(created)
	return t, true, nil
}

// next gives the next number of a counter ("scrolls" or "tomes") of a table.
func next(ctx context.Context, tx *sql.Tx, column, workspace, table string) (int64, error) {
	if column != "scrolls" && column != "tomes" {
		return 0, fmt.Errorf("no counter %q", column)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO note_counters (workspace, table_id) VALUES (?, ?) ON CONFLICT (workspace, table_id) DO NOTHING`, workspace, table); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE note_counters SET `+column+` = `+column+` + 1 WHERE workspace = ? AND table_id = ?`, workspace, table); err != nil {
		return 0, err
	}
	var n int64
	err := tx.QueryRowContext(ctx, `SELECT `+column+` FROM note_counters WHERE workspace = ? AND table_id = ?`, workspace, table).Scan(&n)
	return n, err
}

// uniqueName gives the name, or the name with " (2)", " (3)"… when a scroll
// of the table other than self already has it.
func uniqueName(ctx context.Context, q querier, workspace, table, base, self string) (string, error) {
	for n := 1; ; n++ {
		name := base
		if n > 1 {
			name = fmt.Sprintf("%s (%d)", base, n)
		}
		var key string
		err := q.QueryRowContext(ctx, `SELECT key FROM notes WHERE workspace = ? AND table_id = ? AND name_key = ?`, workspace, table, nameKey(name)).Scan(&key)
		switch {
		case errors.Is(err, sql.ErrNoRows), err == nil && key == self:
			return name, nil
		case err != nil:
			return "", err
		}
	}
}

func nameTaken(ctx context.Context, q querier, workspace, table, name, self string) (bool, error) {
	var key string
	err := q.QueryRowContext(ctx, `SELECT key FROM notes WHERE workspace = ? AND table_id = ? AND name_key = ?`, workspace, table, nameKey(name)).Scan(&key)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, err
	}
	return key != self, nil
}

func (s *Store) exec(ctx context.Context, fn func(tx *sql.Tx) error) error {
	return s.DB.WriteDurable(ctx, fn)
}
