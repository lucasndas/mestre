package tables

import (
	"context"
	"database/sql"
	"time"
)

// SetAlert records that a ref of the table moved without the engine, with the
// commit it had. It says whether the alert is new for that commit (the same
// move is told once).
func (s Store) SetAlert(ctx context.Context, workspace, table, ref, commit string, at time.Time) (bool, error) {
	fresh := true
	err := s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var old string
		switch err := tx.QueryRowContext(ctx, `SELECT commit_id FROM table_ref_alerts WHERE workspace = ? AND table_id = ? AND ref = ?`, workspace, table, ref).Scan(&old); {
		case err == nil:
			fresh = old != commit
		case err != sql.ErrNoRows:
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO table_ref_alerts (workspace, table_id, ref, commit_id, at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (workspace, table_id, ref) DO UPDATE SET commit_id = excluded.commit_id, at = CASE WHEN table_ref_alerts.commit_id = excluded.commit_id THEN table_ref_alerts.at ELSE excluded.at END`,
			workspace, table, ref, commit, ms(at))
		return err
	})
	return fresh, err
}

// HasAlert says whether any ref of the table is under an alert.
func (s Store) HasAlert(ctx context.Context, workspace, table string) (bool, error) {
	var n int
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM table_ref_alerts WHERE workspace = ? AND table_id = ?`, workspace, table).Scan(&n)
	return n > 0, err
}

// ClearAlerts forgets the alerts of the table: an Administrator trusted its
// refs again.
func (s Store) ClearAlerts(ctx context.Context, workspace, table string) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM table_ref_alerts WHERE workspace = ? AND table_id = ?`, workspace, table)
		return err
	})
}
