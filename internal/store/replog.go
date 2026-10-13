package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Reading, retention and watching of replication_log
// (docs/guilda/replicacao.md#banco-cópia-completa-e-envio-contínuo). The
// Citadel ships the rows as they are; a Tower stores the same rows when it
// applies them, so any machine can continue the sequence after a promotion.

// DefaultLogMaxAge and DefaultLogMaxBytes are the retention of the log: rows
// older than the age and, beyond the size, the oldest ones are removed.
const (
	DefaultLogMaxAge   = time.Hour
	DefaultLogMaxBytes = 256 << 20
)

// LogWatch returns a channel that is closed at the next commit that wrote to
// the log (nil on a database that does not capture: a replica learns of its
// commits from Apply).
func (d *DB) LogWatch() <-chan struct{} {
	if d.capture == nil {
		return nil
	}
	return d.capture.Watch()
}

// LastLSN is the highest lsn in the log: the last transaction of a primary,
// the last record a replica applied. 0 when there is none.
func (d *DB) LastLSN(ctx context.Context) (uint64, error) {
	var n uint64
	err := d.Reader().QueryRowContext(ctx, `SELECT coalesce(max(lsn), 0) FROM replication_log`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: last lsn: %w", err)
	}
	return n, nil
}

// LogBounds are the lowest and the highest lsn kept (0, 0 for an empty log).
func (d *DB) LogBounds(ctx context.Context) (lo, hi uint64, err error) {
	err = d.Reader().QueryRowContext(ctx, `SELECT coalesce(min(lsn), 0), coalesce(max(lsn), 0) FROM replication_log`).Scan(&lo, &hi)
	if err != nil {
		return 0, 0, fmt.Errorf("store: log bounds: %w", err)
	}
	return lo, hi, nil
}

// LogSince returns up to limit rows with an lsn above after, in order, and
// stops early once the bodies add up to maxBytes (always at least one row, if
// there is one).
func (d *DB) LogSince(ctx context.Context, after uint64, limit int, maxBytes int) ([]LogRow, error) {
	rows, err := d.Reader().QueryContext(ctx, `SELECT lsn, at, body FROM replication_log WHERE lsn > ? ORDER BY lsn LIMIT ?`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("store: read the log: %w", err)
	}
	defer rows.Close()
	var out []LogRow
	size := 0
	for rows.Next() {
		var r LogRow
		if err := rows.Scan(&r.LSN, &r.At, &r.Body); err != nil {
			return nil, fmt.Errorf("store: read the log: %w", err)
		}
		if len(out) > 0 && size+len(r.Body) > maxBytes {
			break
		}
		size += len(r.Body)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read the log: %w", err)
	}
	return out, nil
}

// LogTime is the moment, in Unix milliseconds, the transaction with this lsn
// committed on the primary; ok is false when the row is gone.
func (d *DB) LogTime(ctx context.Context, lsn uint64) (at int64, ok bool, err error) {
	err = d.Reader().QueryRowContext(ctx, `SELECT at FROM replication_log WHERE lsn = ?`, lsn).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("store: log time: %w", err)
	}
	return at, true, nil
}

// PruneLog applies the retention: it removes the rows older than maxAge and,
// if the bodies still add up to more than maxBytes, the oldest ones. The
// newest row stays, because the next lsn comes from it. It returns how many
// rows it removed. On a replica it is one of the writes the replication
// does.
func (d *DB) PruneLog(ctx context.Context, maxAge time.Duration, maxBytes int64) (int, error) {
	removed := 0
	err := d.maintenance(ctx, func(tx *sql.Tx) error {
		cutoff := d.clock.Now().Add(-maxAge).UnixMilli()
		res, err := tx.ExecContext(ctx, `DELETE FROM replication_log WHERE at < ? AND lsn < (SELECT max(lsn) FROM replication_log)`, cutoff)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		removed += int(n)
		// The newest rows that fit in maxBytes stay: the first lsn, counting
		// from the top, whose running total passes it is the last to go.
		var over sql.NullInt64
		err = tx.QueryRowContext(ctx, `SELECT lsn FROM (
			SELECT lsn, sum(length(body)) OVER (ORDER BY lsn DESC) AS tail FROM replication_log
		) WHERE tail > ? ORDER BY lsn DESC LIMIT 1`, maxBytes).Scan(&over)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !over.Valid) {
			return nil
		}
		if err != nil {
			return err
		}
		res, err = tx.ExecContext(ctx, `DELETE FROM replication_log WHERE lsn <= ? AND lsn < (SELECT max(lsn) FROM replication_log)`, over.Int64)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		removed += int(n)
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("store: prune the log: %w", err)
	}
	return removed, nil
}

// maintenance runs fn in a write transaction on the writer of either kind of
// database: a primary writes through its (logging) connection, where a write
// to replication_log itself leaves no record; a replica through the
// privileged connection of the replication.
func (d *DB) maintenance(ctx context.Context, fn func(tx *sql.Tx) error) error {
	if !d.replica {
		tx, err := d.Writer().BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback() //nolint:errcheck // no-op after Commit
		if err := fn(tx); err != nil {
			return err
		}
		return tx.Commit()
	}
	return d.privileged(ctx, func(conn *sql.Conn) error {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback() //nolint:errcheck // no-op after Commit
		if err := fn(tx); err != nil {
			return err
		}
		return tx.Commit()
	})
}
