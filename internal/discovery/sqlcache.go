package discovery

import (
	"context"
	"database/sql"
	"time"

	"github.com/lucasndas/mestre/internal/store"
)

// SQLCache keeps the addresses in the known_addresses table of the machine
// database (migration 0003_discovery.sql).
type SQLCache struct{ db *store.DB }

var _ Cache = (*SQLCache)(nil)

// NewSQLCache returns the cache over an open machine database.
func NewSQLCache(db *store.DB) *SQLCache { return &SQLCache{db: db} }

// List implements Cache.
func (c *SQLCache) List(ctx context.Context) ([]CacheEntry, error) {
	rows, err := c.db.Reader().QueryContext(ctx,
		`SELECT addr, machine_id, source, added_at, last_success FROM known_addresses ORDER BY addr`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CacheEntry
	for rows.Next() {
		var e CacheEntry
		var src string
		var added int64
		var last sql.NullInt64
		if err := rows.Scan(&e.Addr, &e.MachineID, &src, &added, &last); err != nil {
			return nil, err
		}
		e.Source, e.AddedAt = Source(src), time.UnixMilli(added).UTC()
		if last.Valid {
			e.LastSuccess = time.UnixMilli(last.Int64).UTC()
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Add implements Cache.
func (c *SQLCache) Add(ctx context.Context, e CacheEntry) error {
	_, err := c.db.Writer().ExecContext(ctx,
		`INSERT INTO known_addresses (addr, machine_id, source, added_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(addr) DO UPDATE SET machine_id = excluded.machine_id WHERE excluded.machine_id <> ''`,
		e.Addr, e.MachineID, string(e.Source), e.AddedAt.UnixMilli())
	return err
}

// Success implements Cache.
func (c *SQLCache) Success(ctx context.Context, addr, machineID string, src Source, at time.Time) error {
	_, err := c.db.Writer().ExecContext(ctx,
		`INSERT INTO known_addresses (addr, machine_id, source, added_at, last_success) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(addr) DO UPDATE SET machine_id = excluded.machine_id, last_success = excluded.last_success`,
		addr, machineID, string(src), at.UnixMilli(), at.UnixMilli())
	return err
}

// Prune implements Cache.
func (c *SQLCache) Prune(ctx context.Context, before time.Time) error {
	_, err := c.db.Writer().ExecContext(ctx,
		`DELETE FROM known_addresses WHERE MAX(added_at, COALESCE(last_success, 0)) < ?`, before.UnixMilli())
	return err
}
