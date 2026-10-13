//go:build spike

package dbcapture

// Plan B of docs/guilda/replicacao.md#verificar-na-implementação: the store
// writes a logical change log (replication_log: table, key, operation,
// values) in the same transaction, and that log is what travels. The
// changes are collected by SQLite's preupdate hook on the single writer
// connection (modernc.org/sqlite exposes it), so no write path can forget
// them; DDL is invisible to the hook and travels as an explicit SQL change.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/gob"
	"fmt"
	"strings"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/lucasndas/mestre/internal/store"
)

const (
	OpInsert = sqlite3.SQLITE_INSERT
	OpUpdate = sqlite3.SQLITE_UPDATE
	OpDelete = sqlite3.SQLITE_DELETE
	OpSQL    = -1 // DDL, executed as is on the replica
)

// Change is one row change (or one DDL statement).
type Change struct {
	Table string
	Op    int32
	Old   []any // full old row (UPDATE, DELETE)
	New   []any // full new row (INSERT, UPDATE)
	SQL   string
}

// LogRecord is one committed transaction of the logical log.
type LogRecord struct {
	LSN     uint64
	At      int64
	Changes []Change
}

const logTable = "replication_log"

// LogDDL creates the log table; it belongs in the first migration.
const LogDDL = `CREATE TABLE IF NOT EXISTS replication_log (lsn INTEGER PRIMARY KEY, at INTEGER NOT NULL, body BLOB NOT NULL)`

func init() {
	gob.Register([]byte(nil))
	gob.Register(int64(0))
	gob.Register(float64(0))
	gob.Register("")
}

// EncodeLog is the wire (and table) form of a record.
func EncodeLog(r *LogRecord) []byte {
	var b bytes.Buffer
	if err := gob.NewEncoder(&b).Encode(r); err != nil {
		panic(err)
	}
	return b.Bytes()
}

func DecodeLog(p []byte) (*LogRecord, error) {
	var r LogRecord
	err := gob.NewDecoder(bytes.NewReader(p)).Decode(&r)
	return &r, err
}

// LogPrimary is the Citadel side of plan B.
type LogPrimary struct {
	DB      *store.DB
	LSN     uint64
	Ship    func(*LogRecord)
	Skip    map[string]bool // tables never logged
	pending []Change
	hookErr error
}

type preUpdater interface {
	RegisterPreUpdateHook(sqlite.PreUpdateHookFn)
}

// OpenLogPrimary opens path with internal/store and installs the hook on the
// single writer connection.
func OpenLogPrimary(ctx context.Context, path string, set store.Set, opts store.Options) (*LogPrimary, error) {
	db, err := store.Open(ctx, path, set, opts)
	if err != nil {
		return nil, err
	}
	p := &LogPrimary{DB: db, Skip: map[string]bool{logTable: true, "schema_migrations": false}}
	if _, err := db.Writer().ExecContext(ctx, LogDDL); err != nil {
		db.Close()
		return nil, err
	}
	conn, err := db.Writer().Conn(ctx)
	if err != nil {
		db.Close()
		return nil, err
	}
	err = conn.Raw(func(dc any) error {
		pu, ok := dc.(preUpdater)
		if !ok {
			return fmt.Errorf("driver conn %T has no preupdate hook", dc)
		}
		pu.RegisterPreUpdateHook(p.hook)
		return nil
	})
	conn.Close()
	if err != nil {
		db.Close()
		return nil, err
	}
	if err := db.Writer().QueryRowContext(ctx, `SELECT coalesce(max(lsn), 0) FROM replication_log`).Scan(&p.LSN); err != nil {
		db.Close()
		return nil, err
	}
	return p, nil
}

func (p *LogPrimary) hook(d sqlite.SQLitePreUpdateData) {
	if d.DatabaseName != "main" || p.Skip[d.TableName] {
		return
	}
	c := Change{Table: d.TableName, Op: d.Op}
	n := d.Count()
	if d.Op != OpInsert {
		c.Old = make([]any, n)
		if err := d.Old(c.Old...); err != nil && p.hookErr == nil {
			p.hookErr = err
		}
	}
	if d.Op != OpDelete {
		c.New = make([]any, n)
		if err := d.New(c.New...); err != nil && p.hookErr == nil {
			p.hookErr = err
		}
	}
	p.pending = append(p.pending, c)
}

// Write runs fn in one transaction, appends its changes to replication_log
// in the same transaction, commits and ships the record.
func (p *LogPrimary) Write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	return p.write(ctx, fn, nil)
}

// Migrate runs DDL (and its schema_migrations row) as one logged
// transaction.
func (p *LogPrimary) Migrate(ctx context.Context, version int, ddl string) error {
	return p.write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, name, applied_at) VALUES(?, 'm', 0)`, version)
		return err
	}, []Change{{Op: OpSQL, SQL: ddl}})
}

func (p *LogPrimary) write(ctx context.Context, fn func(tx *sql.Tx) error, ddl []Change) error {
	tx, err := p.DB.Writer().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	p.pending, p.hookErr = nil, nil
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if p.hookErr != nil {
		_ = tx.Rollback()
		return p.hookErr
	}
	changes := append(ddl, p.pending...)
	if len(changes) == 0 {
		return tx.Commit()
	}
	rec := &LogRecord{LSN: p.LSN + 1, Changes: changes}
	if _, err := tx.ExecContext(ctx, `INSERT INTO replication_log(lsn, at, body) VALUES(?, ?, ?)`, rec.LSN, rec.At, EncodeLog(rec)); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	p.LSN = rec.LSN
	if p.Ship != nil {
		p.Ship(rec)
	}
	return nil
}

// LogReplica applies logical records.
type LogReplica struct {
	DB    *store.DB
	LSN   uint64
	Skip  map[string]bool // tables the replica never receives (excluded machine)
	infos map[string]tableInfo
	// BeforeCommit is a crash point for the failure tests.
	BeforeCommit func()
}

// OpenLogReplica opens a replica; its lsn is the last log row it applied,
// committed in the same transaction as the changes.
func OpenLogReplica(ctx context.Context, path string, set store.Set, opts store.Options) (*LogReplica, error) {
	opts.Replica = true
	db, err := store.Open(ctx, path, set, opts)
	if err != nil {
		return nil, err
	}
	r := &LogReplica{DB: db}
	if err := db.Reader().QueryRowContext(ctx, `SELECT coalesce(max(lsn), 0) FROM replication_log`).Scan(&r.LSN); err != nil {
		db.Close()
		return nil, err
	}
	return r, nil
}

type tableInfo struct {
	cols []string
	pk   []int // indexes of the primary key columns
}

func (r *LogReplica) info(ctx context.Context, tx *sql.Tx, table string) (tableInfo, error) {
	if ti, ok := r.infos[table]; ok {
		return ti, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT name, pk FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		return tableInfo{}, err
	}
	defer rows.Close()
	var ti tableInfo
	pkOrder := map[int]int{}
	for i := 0; rows.Next(); i++ {
		var name string
		var pk int
		if err := rows.Scan(&name, &pk); err != nil {
			return tableInfo{}, err
		}
		ti.cols = append(ti.cols, name)
		if pk > 0 {
			pkOrder[pk] = i
		}
	}
	for k := 1; k <= len(pkOrder); k++ {
		ti.pk = append(ti.pk, pkOrder[k])
	}
	if len(ti.pk) == 0 {
		return tableInfo{}, fmt.Errorf("table %s has no primary key: rows cannot be addressed", table)
	}
	if r.infos == nil {
		r.infos = map[string]tableInfo{}
	}
	r.infos[table] = ti
	return ti, nil
}

// Apply runs the changes of rec in one transaction, foreign keys off (the
// primary already checked them, and cascades arrive as their own changes).
func (r *LogReplica) Apply(ctx context.Context, rec *LogRecord) error {
	if rec.LSN <= r.LSN {
		return nil
	}
	if rec.LSN != r.LSN+1 {
		return &ErrOutOfOrder{Have: r.LSN, Got: rec.LSN}
	}
	conn, err := r.DB.Writer().Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), `PRAGMA foreign_keys = ON`)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, c := range rec.Changes {
		if c.Op == OpSQL {
			if _, err := tx.ExecContext(ctx, c.SQL); err != nil {
				return fmt.Errorf("lsn %d: ddl: %w", rec.LSN, err)
			}
			r.infos = nil
			continue
		}
		if r.Skip[c.Table] {
			continue
		}
		ti, err := r.info(ctx, tx, c.Table)
		if err != nil {
			return err
		}
		q, args := changeSQL(c, ti)
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return fmt.Errorf("lsn %d: %s: %w", rec.LSN, q, err)
		}
	}
	// The log itself travels too, so a promoted Tower has the history; the
	// excluded machine (Skip set) never becomes the Citadel and keeps none.
	if len(r.Skip) > 0 {
		if err := tx.Commit(); err != nil {
			return err
		}
		r.LSN = rec.LSN
		return nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO replication_log(lsn, at, body) VALUES(?, ?, ?)`, rec.LSN, rec.At, EncodeLog(rec)); err != nil {
		return err
	}
	if r.BeforeCommit != nil {
		r.BeforeCommit()
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	r.LSN = rec.LSN
	return nil
}

func changeSQL(c Change, ti tableInfo) (string, []any) {
	q := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
	var where []string
	var wargs []any
	if c.Op != OpInsert {
		for _, i := range ti.pk {
			where = append(where, q(ti.cols[i])+" = ?")
			wargs = append(wargs, c.Old[i])
		}
	}
	switch c.Op {
	case OpInsert:
		cols := make([]string, len(ti.cols))
		marks := make([]string, len(ti.cols))
		for i, col := range ti.cols {
			cols[i], marks[i] = q(col), "?"
		}
		return "INSERT INTO " + q(c.Table) + "(" + strings.Join(cols, ", ") + ") VALUES(" + strings.Join(marks, ", ") + ")", c.New[:len(ti.cols)]
	case OpUpdate:
		set := make([]string, len(ti.cols))
		for i, col := range ti.cols {
			set[i] = q(col) + " = ?"
		}
		return "UPDATE " + q(c.Table) + " SET " + strings.Join(set, ", ") + " WHERE " + strings.Join(where, " AND "), append(append([]any(nil), c.New[:len(ti.cols)]...), wargs...)
	default:
		return "DELETE FROM " + q(c.Table) + " WHERE " + strings.Join(where, " AND "), wargs
	}
}
