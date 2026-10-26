package classes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mestre-ia/mestre/internal/store"
)

// CeilingDB keeps the approved versions of the ceilings in the Guild
// database (workspace_ceilings), on the Citadel.
type CeilingDB struct{ DB *store.DB }

// Approved implements CeilingRecords.
func (c CeilingDB) Approved(ctx context.Context, workspace string) (CeilingRecord, bool, error) {
	var (
		r      CeilingRecord
		at     int64
		level  string
		via    string
		decree sql.NullInt64
	)
	err := c.DB.Reader().QueryRowContext(ctx, `SELECT hash, doc, approved_at, approved_level, approved_via, decree_id
		FROM workspace_ceilings WHERE workspace = ?`, workspace).Scan(&r.Hash, &r.Doc, &at, &level, &via, &decree)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return CeilingRecord{}, false, nil
	case err != nil:
		return CeilingRecord{}, false, fmt.Errorf("approved ceiling of %s: %w", workspace, err)
	}
	r.ApprovedAt = time.UnixMilli(at).UTC()
	r.By = Authority{Level: Level(level), Via: Via(via), Decree: decree.Int64}
	return r, true, nil
}

// Approve implements CeilingRecords.
func (c CeilingDB) Approve(ctx context.Context, workspace string, r CeilingRecord) error {
	var decree sql.NullInt64
	if r.By.Decree > 0 {
		decree = sql.NullInt64{Int64: r.By.Decree, Valid: true}
	}
	return c.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO workspace_ceilings (workspace, hash, doc, approved_at, approved_level, approved_via, decree_id)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (workspace) DO UPDATE SET hash = excluded.hash, doc = excluded.doc, approved_at = excluded.approved_at,
				approved_level = excluded.approved_level, approved_via = excluded.approved_via, decree_id = excluded.decree_id`,
			workspace, r.Hash, r.Doc, r.ApprovedAt.UTC().UnixMilli(), string(r.By.Level), string(r.By.Via), decree)
		return err
	})
}
