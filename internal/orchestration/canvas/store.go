package canvas

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The kinds of element, the prefix of a key.
const (
	KindCharacter = "character"
	KindScroll    = "scroll"
	KindTome      = "tome"
	KindGroup     = "group"
)

// Key makes the key of an element.
func Key(kind, id string) string { return kind + ":" + id }

// Item is where an element stands on the canvas of a table.
type Item struct {
	Key    string
	Label  string
	X, Y   float64
	W, H   float64
	Parent string
}

// Kind is the kind of the element the key names.
func (i Item) Kind() string {
	k, _, _ := strings.Cut(i.Key, ":")
	return k
}

// Store is the canvas_items table of the Guild database.
type Store struct {
	DB    *store.DB
	Clock clock.Clock
}

// Items lists the elements of the canvas of a table, by key.
func (s *Store) Items(ctx context.Context, workspace, table string) ([]Item, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT key, label, x, y, w, h, parent FROM canvas_items WHERE workspace = ? AND table_id = ? ORDER BY key`, workspace, table)
	if err != nil {
		return nil, fmt.Errorf("canvas: %w", err)
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var i Item
		if err := rows.Scan(&i.Key, &i.Label, &i.X, &i.Y, &i.W, &i.H, &i.Parent); err != nil {
			return nil, fmt.Errorf("canvas: %w", err)
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Put saves the elements (a key that exists is replaced) in one transaction.
func (s *Store) Put(ctx context.Context, workspace, table string, items []Item) error {
	if len(items) == 0 {
		return nil
	}
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		now := s.Clock.Now().UnixMilli()
		for _, i := range items {
			if _, err := tx.ExecContext(ctx, `INSERT INTO canvas_items (workspace, table_id, key, label, x, y, w, h, parent, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (workspace, table_id, key) DO UPDATE SET label = excluded.label, x = excluded.x, y = excluded.y, w = excluded.w, h = excluded.h, parent = excluded.parent, updated_at = excluded.updated_at`,
				workspace, table, i.Key, i.Label, i.X, i.Y, i.W, i.H, i.Parent, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// Delete forgets the elements; whatever sat in a deleted group leaves it.
func (s *Store) Delete(ctx context.Context, workspace, table string, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		for _, k := range keys {
			if _, err := tx.ExecContext(ctx, `DELETE FROM canvas_items WHERE workspace = ? AND table_id = ? AND key = ?`, workspace, table, k); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE canvas_items SET parent = '' WHERE workspace = ? AND table_id = ? AND parent = ?`, workspace, table, k); err != nil {
				return err
			}
		}
		return nil
	})
}
