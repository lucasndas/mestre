package grimoires

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lucasndas/mestre/internal/store"
)

// Store is the grimoires table of the Guild database: the documents of every
// workspace, since a grimoire is restored in any of them.
type Store struct{ DB *store.DB }

type row struct {
	name, author, workspace, table, doc string
	version                             int
	created, updated                    int64
}

func (s *Store) get(ctx context.Context, name string) (row, error) {
	var r row
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT name, version, author, workspace, table_id, document, created_at, updated_at FROM grimoires WHERE name = ?`, name).
		Scan(&r.name, &r.version, &r.author, &r.workspace, &r.table, &r.doc, &r.created, &r.updated)
	if errors.Is(err, sql.ErrNoRows) {
		return row{}, fmt.Errorf("%w: %s", ErrNoGrimoire, name)
	}
	return r, err
}

// Get reads a grimoire. A document of a version above the supported one is
// refused (VersionError), before anything else of it is read.
func (s *Store) Get(ctx context.Context, name string) (Document, error) {
	r, err := s.get(ctx, name)
	if err != nil {
		return Document{}, err
	}
	if r.version > Version {
		return Document{}, &VersionError{Have: r.version, Max: Version}
	}
	var d Document
	if err := json.Unmarshal([]byte(r.doc), &d); err != nil {
		return Document{}, fmt.Errorf("grimoire %s: %w", name, err)
	}
	return d, nil
}

// Put saves a document under its name; with replace, one of that name is
// overwritten (it keeps its creation date), without it the name must be free.
func (s *Store) Put(ctx context.Context, d Document, replace bool, now time.Time) error {
	doc, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var created int64
		err := tx.QueryRowContext(ctx, `SELECT created_at FROM grimoires WHERE name = ?`, d.Name).Scan(&created)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			_, err = tx.ExecContext(ctx, `INSERT INTO grimoires (name, version, author, workspace, table_id, document, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				d.Name, d.Version, d.Author, d.Workspace, d.Table, string(doc), now.UnixMilli(), now.UnixMilli())
			return err
		case err != nil:
			return err
		case !replace:
			return fmt.Errorf("%w: %s", ErrNameTaken, d.Name)
		}
		_, err = tx.ExecContext(ctx, `UPDATE grimoires SET version = ?, author = ?, workspace = ?, table_id = ?, document = ?, updated_at = ? WHERE name = ?`,
			d.Version, d.Author, d.Workspace, d.Table, string(doc), now.UnixMilli(), d.Name)
		return err
	})
}

// Delete removes a grimoire; the tables already restored are not touched.
func (s *Store) Delete(ctx context.Context, name string) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM grimoires WHERE name = ?`, name)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: %s", ErrNoGrimoire, name)
		}
		return nil
	})
}

// List lists the grimoires of every workspace, by name. A document that cannot
// be read still shows, with its version and no counts.
func (s *Store) List(ctx context.Context) ([]Summary, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT name, version, author, workspace, table_id, document, updated_at FROM grimoires ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Summary
	for rows.Next() {
		var (
			sm  Summary
			doc string
			at  int64
		)
		if err := rows.Scan(&sm.Name, &sm.Version, &sm.Author, &sm.Workspace, &sm.Table, &doc, &at); err != nil {
			return nil, err
		}
		sm.SavedAt = time.UnixMilli(at).UTC()
		if sm.Version <= Version {
			var d Document
			if json.Unmarshal([]byte(doc), &d) == nil {
				sm.Characters, sm.Classes, sm.Scrolls = len(d.Characters), len(d.Classes), len(d.Scrolls)
				sm.Tomes, sm.Connections, sm.Quests = len(d.Tomes), len(d.Connections), len(d.Quests)
			}
		}
		out = append(out, sm)
	}
	return out, rows.Err()
}
