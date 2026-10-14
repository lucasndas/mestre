package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// The writes a replica accepts (docs/guilda/replicacao.md, CA-REPL-05): its
// writer is query_only, and the replication lifts that for the length of one
// of the operations below, on the one connection the writer has.

// ErrNotReplica is returned by the operations that only make sense on a
// replica.
var ErrNotReplica = errors.New("store: the database is not a replica")

// ErrClosed is returned by Replace on a database that was closed: it would
// open the pools again, and with them the file and its WAL, after the owner
// closed the database and let go of the folder.
var ErrClosed = errors.New("store: the database is closed")

// GapError is returned by ApplyLog when a record does not follow the last one
// applied: the replica asks again from Have.
type GapError struct{ Have, Got uint64 }

func (e *GapError) Error() string {
	return fmt.Sprintf("store: record %d does not follow the last applied, %d", e.Got, e.Have)
}

// DivergedError is returned when a record does not fit the replica (a row it
// should change is not there): the replica is not the primary's copy and
// needs a new full copy.
type DivergedError struct {
	LSN    uint64
	Detail string
}

func (e *DivergedError) Error() string {
	return fmt.Sprintf("store: the replica diverged at record %d: %s", e.LSN, e.Detail)
}

// privileged runs fn on the writer connection of a replica with the guard
// lifted (query_only off) and foreign keys off: the primary already checked
// them, and the cascades arrive as changes of their own. The guard is back
// when fn returns; if it cannot be put back the connection is dropped, and the
// pool opens another with the pragmas of the DSN.
func (d *DB) privileged(ctx context.Context, fn func(conn *sql.Conn) error) error {
	if !d.replica {
		return ErrNotReplica
	}
	conn, err := d.Writer().Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	gate := func(open bool) error {
		return conn.Raw(func(dc any) error {
			g, ok := dc.(*guardConn)
			if !ok {
				return fmt.Errorf("store: the writer of a replica is a %T, not the guarded connection", dc)
			}
			g.open.Store(open)
			return nil
		})
	}
	if err := gate(true); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA query_only = 0`); err != nil {
		_ = gate(false)
		return err
	}
	defer func() {
		bg := context.WithoutCancel(ctx)
		_, e1 := conn.ExecContext(bg, `PRAGMA query_only = 1`)
		_, e2 := conn.ExecContext(bg, `PRAGMA foreign_keys = ON`)
		e3 := gate(false)
		if e1 != nil || e2 != nil || e3 != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	return fn(conn)
}

type tableInfo struct {
	cols []string
	pk   []int // indexes into cols, in key order
}

func (d *DB) tableInfo(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, table string) (tableInfo, error) {
	d.infoMu.Lock()
	ti, ok := d.infos[table]
	d.infoMu.Unlock()
	if ok {
		return ti, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT name, pk FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		return tableInfo{}, err
	}
	defer rows.Close()
	order := map[int]int{}
	for i := 0; rows.Next(); i++ {
		var name string
		var pk int
		if err := rows.Scan(&name, &pk); err != nil {
			return tableInfo{}, err
		}
		ti.cols = append(ti.cols, name)
		if pk > 0 {
			order[pk] = i
		}
	}
	if err := rows.Err(); err != nil {
		return tableInfo{}, err
	}
	if len(ti.cols) == 0 {
		return tableInfo{}, fmt.Errorf("table %s does not exist", table)
	}
	for k := 1; k <= len(order); k++ {
		ti.pk = append(ti.pk, order[k])
	}
	if len(ti.pk) == 0 {
		return tableInfo{}, fmt.Errorf("table %s has no declared primary key: its rows cannot be addressed", table)
	}
	d.infoMu.Lock()
	d.infos[table] = ti
	d.infoMu.Unlock()
	return ti, nil
}

func (d *DB) forgetTables() {
	d.infoMu.Lock()
	clear(d.infos)
	d.infoMu.Unlock()
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// ApplyLog applies records of the primary to a replica, in order, in one
// transaction, and stores each row in the log of the replica in the same
// transaction: the last lsn applied is max(lsn). A record that is not above
// the last one is a repeat and is skipped; one that leaves a hole stops the
// batch with a *GapError, after committing the ones before it. It returns
// the number of records applied.
func (d *DB) ApplyLog(ctx context.Context, rows []LogRow) (int, error) {
	applied := 0
	var gap error
	err := d.privileged(ctx, func(conn *sql.Conn) error {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback() //nolint:errcheck // no-op after Commit
		var last uint64
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(lsn), 0) FROM replication_log`).Scan(&last); err != nil {
			return err
		}
		for _, r := range rows {
			if r.LSN <= last {
				continue
			}
			if r.LSN != last+1 {
				gap = &GapError{Have: last, Got: r.LSN}
				break
			}
			cs, err := DecodeChanges(r.Body)
			if err != nil {
				return fmt.Errorf("record %d: %w", r.LSN, err)
			}
			for _, c := range cs {
				if err := d.applyChange(ctx, tx, r.LSN, c); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO replication_log (lsn, at, body) VALUES (?, ?, ?)`, r.LSN, r.At, r.Body); err != nil {
				return fmt.Errorf("record %d: %w", r.LSN, err)
			}
			last = r.LSN
			applied++
		}
		return tx.Commit()
	})
	if err != nil {
		return 0, err
	}
	return applied, gap
}

func (d *DB) applyChange(ctx context.Context, tx *sql.Tx, lsn uint64, c Change) error {
	if c.Op == OpSQL {
		d.forgetTables()
		if _, err := tx.ExecContext(ctx, c.SQL); err != nil {
			return fmt.Errorf("record %d: schema statement: %w", lsn, err)
		}
		return nil
	}
	ti, err := d.tableInfo(ctx, tx, c.Table)
	if err != nil {
		return &DivergedError{LSN: lsn, Detail: err.Error()}
	}
	var q strings.Builder
	var args []any
	switch c.Op {
	case OpInsert:
		if len(c.New) != len(ti.cols) {
			return &DivergedError{LSN: lsn, Detail: fmt.Sprintf("%s has %d columns here and %d in the record", c.Table, len(ti.cols), len(c.New))}
		}
		q.WriteString("INSERT INTO " + quoteIdent(c.Table) + " (")
		marks := make([]string, len(ti.cols))
		for i, col := range ti.cols {
			if i > 0 {
				q.WriteString(", ")
			}
			q.WriteString(quoteIdent(col))
			marks[i] = "?"
		}
		q.WriteString(") VALUES (" + strings.Join(marks, ", ") + ")")
		args = c.New
	case OpUpdate, OpDelete:
		if len(c.Old) != len(ti.cols) || (c.Op == OpUpdate && len(c.New) != len(ti.cols)) {
			return &DivergedError{LSN: lsn, Detail: fmt.Sprintf("%s has %d columns here and a different count in the record", c.Table, len(ti.cols))}
		}
		if c.Op == OpUpdate {
			q.WriteString("UPDATE " + quoteIdent(c.Table) + " SET ")
			for i, col := range ti.cols {
				if i > 0 {
					q.WriteString(", ")
				}
				q.WriteString(quoteIdent(col) + " = ?")
			}
			args = append(args, c.New...)
		} else {
			q.WriteString("DELETE FROM " + quoteIdent(c.Table))
		}
		q.WriteString(" WHERE ")
		for n, i := range ti.pk {
			if n > 0 {
				q.WriteString(" AND ")
			}
			if c.Old[i] == nil {
				q.WriteString(quoteIdent(ti.cols[i]) + " IS NULL")
				continue
			}
			q.WriteString(quoteIdent(ti.cols[i]) + " = ?")
			args = append(args, c.Old[i])
		}
	default:
		return fmt.Errorf("record %d: unknown operation %d", lsn, c.Op)
	}
	res, err := tx.ExecContext(ctx, q.String(), args...)
	if err != nil {
		return fmt.Errorf("record %d: %s %s: %w", lsn, c.Op, c.Table, err)
	}
	if c.Op != OpInsert {
		if n, _ := res.RowsAffected(); n != 1 {
			return &DivergedError{LSN: lsn, Detail: fmt.Sprintf("%s of %s changed %d rows, not 1", c.Op, c.Table, n)}
		}
	}
	return nil
}

// Purge empties the tables and scrubs the log of what they held: the rows
// of the vault tables on a machine that was excluded, and the changes of those
// tables in the records it kept. The file is vacuumed and the WAL truncated, so
// the bytes are gone from the disk too. Only a replica; the schema stays, so
// the migrations that follow still apply.
func (d *DB) Purge(ctx context.Context, tables ...string) error {
	err := d.privileged(ctx, func(conn *sql.Conn) error {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback() //nolint:errcheck // no-op after Commit
		for _, t := range tables {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, t).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				continue
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+quoteIdent(t)); err != nil {
				return err
			}
		}
		// The log: only its last row stays, with nothing in it, so the lsn
		// sequence goes on.
		if _, err := tx.ExecContext(ctx, `DELETE FROM replication_log WHERE lsn < (SELECT max(lsn) FROM replication_log)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE replication_log SET body = ?`, EncodeChanges(nil)); err != nil {
			return err
		}
		return tx.Commit()
	})
	if err != nil {
		return fmt.Errorf("store: purge: %w", err)
	}
	// Deleted pages keep their content until the file is rewritten, and the
	// WAL keeps the pages it had before until it is truncated, which it is
	// not while a reader still holds a snapshot of them: say so, and let the
	// caller try again, rather than report a purge that left the secrets in
	// the -wal file.
	return d.privileged(ctx, func(conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, `VACUUM`); err != nil {
			return fmt.Errorf("store: purge: %w", err)
		}
		for range purgeAttempts {
			var busy, logFrames, checkpointed int
			if err := conn.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointed); err != nil {
				return fmt.Errorf("store: purge: %w", err)
			}
			if busy == 0 {
				return nil
			}
		}
		return ErrPurgeIncomplete
	})
}

// purgeAttempts is how many times Purge asks for the WAL to be truncated
// before it gives up (each ask waits for the busy timeout).
const purgeAttempts = 3

// ErrPurgeIncomplete is returned by Purge when the rows are gone and the file
// is rewritten, but the WAL could not be truncated because a reader held it:
// what was deleted may still be in the -wal file. Try again when the readers
// are done.
var ErrPurgeIncomplete = errors.New("store: purge: the WAL could not be truncated, a reader is still using it")

// SnapshotOptions shapes a full copy.
type SnapshotOptions struct {
	// EmptyTables are left without rows in the copy (the vault tables, for an
	// excluded machine); their schema stays, and so does nothing of theirs on
	// disk.
	EmptyTables []string
	// KeepLogBytes: a log with more than this many bytes of bodies (default
	// 1 MiB) is trimmed to its last row in the copy, which costs a second pass;
	// a smaller one goes as it is, because it is not worth the pass.
	KeepLogBytes int64
}

// DefaultKeepLogBytes is SnapshotOptions.KeepLogBytes when it is zero.
const DefaultKeepLogBytes = 1 << 20

// Snapshot writes at dst a consistent full copy of the database (VACUUM INTO)
// and returns the lsn it holds. The log in the copy is trimmed to its last
// row when it is big (or when tables are emptied, because the last record may
// carry changes of them): the machine that receives the copy continues from
// there and needs no history. dst must not exist. The writes of the primary go
// on while the copy is made.
func (d *DB) Snapshot(ctx context.Context, dst string, opts SnapshotOptions) (uint64, error) {
	full := dst + ".full"
	for _, p := range []string{dst, full} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return 0, fmt.Errorf("store: snapshot: %w", err)
		}
	}
	defer removeDB(full)
	keep := opts.KeepLogBytes
	if keep <= 0 {
		keep = DefaultKeepLogBytes
	}
	var logBytes int64
	if err := d.Reader().QueryRowContext(ctx, `SELECT coalesce(sum(length(body)), 0) FROM replication_log`).Scan(&logBytes); err != nil {
		return 0, fmt.Errorf("store: snapshot: %w", err)
	}
	trim := len(opts.EmptyTables) > 0 || logBytes > keep
	first := dst
	if trim {
		first = full
	}
	// A read of its own: the writer keeps writing meanwhile.
	src, err := sql.Open("sqlite", roDSN(d.path, d.sidePragmas(fmt.Sprintf("busy_timeout(%d)", d.opts.BusyTimeout.Milliseconds()))))
	if err != nil {
		return 0, fmt.Errorf("store: snapshot: %w", err)
	}
	src.SetMaxOpenConns(1)
	_, err = src.ExecContext(ctx, `VACUUM INTO ?`, first)
	_ = src.Close()
	if err != nil {
		return 0, fmt.Errorf("store: snapshot: %w", err)
	}
	if !trim {
		return copyLSN(ctx, dst)
	}
	// Trim the copy, then rewrite it so what was dropped is not in the file.
	cp, err := sql.Open("sqlite", dsn(full, d.sidePragmas("busy_timeout(5000)", "journal_mode(DELETE)", "foreign_keys(0)"), ""))
	if err != nil {
		return 0, fmt.Errorf("store: snapshot: %w", err)
	}
	cp.SetMaxOpenConns(1)
	defer cp.Close()
	conn, err := cp.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: snapshot: %w", err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: snapshot: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	if _, err := tx.ExecContext(ctx, `DELETE FROM replication_log WHERE lsn < (SELECT max(lsn) FROM replication_log)`); err != nil {
		return 0, fmt.Errorf("store: snapshot: %w", err)
	}
	for _, t := range opts.EmptyTables {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, t).Scan(&n); err != nil {
			return 0, fmt.Errorf("store: snapshot: %w", err)
		}
		if n == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+quoteIdent(t)); err != nil {
			return 0, fmt.Errorf("store: snapshot: %w", err)
		}
	}
	if len(opts.EmptyTables) > 0 { // the last record may carry changes of those tables
		if _, err := tx.ExecContext(ctx, `UPDATE replication_log SET body = ?`, EncodeChanges(nil)); err != nil {
			return 0, fmt.Errorf("store: snapshot: %w", err)
		}
	}
	var lsn uint64
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(lsn), 0) FROM replication_log`).Scan(&lsn); err != nil {
		return 0, fmt.Errorf("store: snapshot: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: snapshot: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `VACUUM INTO ?`, dst); err != nil {
		return 0, fmt.Errorf("store: snapshot: %w", err)
	}
	return lsn, nil
}

// sidePragmas are the pragmas of a connection the DB opens besides its pools:
// a database that runs without flushes (Options.Synchronous OFF) keeps the
// copies it writes without them too, and the level goes first, because the
// default one (FULL) would flush the switch of journal mode.
func (d *DB) sidePragmas(pragmas ...string) []string {
	if d.sync == "OFF" {
		return append([]string{"synchronous(OFF)"}, pragmas...)
	}
	return pragmas
}

// copyLSN is the highest lsn of the copy at path.
func copyLSN(ctx context.Context, path string) (uint64, error) {
	db, err := sql.Open("sqlite", dsn(path, []string{"query_only(1)"}, ""))
	if err != nil {
		return 0, fmt.Errorf("store: snapshot: %w", err)
	}
	defer db.Close()
	var lsn uint64
	if err := db.QueryRowContext(ctx, `SELECT coalesce(max(lsn), 0) FROM replication_log`).Scan(&lsn); err != nil {
		return 0, fmt.Errorf("store: snapshot: %w", err)
	}
	return lsn, nil
}

func removeDB(path string) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		_ = os.Remove(path + suffix)
	}
}

// Replace swaps the file of a replica for the full copy at src, once it checks
// out: SQLite's quick_check passes, the schema is not newer than this binary
// and the highest lsn in it is wantLSN. The replica is closed and opened again
// on the new file; Writer and Reader return the new pools afterwards (a caller
// that kept an old one gets "database is closed"). src is moved (or copied, from another volume); what is left of it is up to the caller.
// A database that was closed stays closed: Replace returns ErrClosed and
// touches no file.
func (d *DB) Replace(ctx context.Context, src string, wantLSN uint64) error {
	if !d.replica {
		return ErrNotReplica
	}
	if d.isClosed() {
		return ErrClosed
	}
	if err := d.checkCopy(ctx, src, wantLSN); err != nil {
		return err
	}
	// Next to the target, so the move below is a rename.
	incoming := d.path + ".incoming"
	// The copy is flushed to disk unless the database runs without flushes (the
	// tests that are not about durability: a flush costs tens of milliseconds
	// on a Windows runner).
	if err := os.Rename(src, incoming); err != nil { // not on the same volume: copy it
		if err := copyFile(src, incoming, d.opts.Synchronous != "OFF"); err != nil {
			return fmt.Errorf("store: replace: %w", err)
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed { // closed while the copy was being checked and moved
		removeDB(incoming)
		return ErrClosed
	}
	if err := d.closePools(); err != nil {
		removeDB(incoming)
		return fmt.Errorf("store: replace: %w", err)
	}
	d.forgetTables()
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if err := os.Remove(d.path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			removeDB(incoming)
			return errors.Join(fmt.Errorf("store: replace: %w", err), d.openPools(ctx))
		}
	}
	if err := os.Rename(incoming, d.path); err != nil {
		removeDB(incoming)
		return errors.Join(fmt.Errorf("store: replace: %w", err), d.openPools(ctx))
	}
	if err := d.openPools(ctx); err != nil {
		return fmt.Errorf("store: replace: %w", err)
	}
	return nil
}

func (d *DB) isClosed() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.closed
}

func (d *DB) checkCopy(ctx context.Context, path string, wantLSN uint64) error {
	db, err := sql.Open("sqlite", dsn(path, []string{"query_only(1)"}, ""))
	if err != nil {
		return fmt.Errorf("store: the copy: %w", err)
	}
	defer db.Close()
	if err := quickCheckDB(ctx, db); err != nil {
		return fmt.Errorf("store: the copy is damaged: %w", err)
	}
	have, err := appliedVersion(ctx, db)
	if err != nil {
		return fmt.Errorf("store: the copy: %w", err)
	}
	if have > d.set.Latest() {
		return &NewerSchemaError{Path: path, Have: have, Supported: d.set.Latest()}
	}
	var lsn uint64
	if err := db.QueryRowContext(ctx, `SELECT coalesce(max(lsn), 0) FROM replication_log`).Scan(&lsn); err != nil {
		return fmt.Errorf("store: the copy has no change log: %w", err)
	}
	if lsn != wantLSN {
		return fmt.Errorf("store: the copy holds lsn %d, not the %d announced", lsn, wantLSN)
	}
	return nil
}

func copyFile(src, dst string, flush bool) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	if flush {
		if err := out.Sync(); err != nil {
			_ = out.Close()
			_ = os.Remove(dst)
			return err
		}
	}
	return out.Close()
}
