// Package store opens the SQLite databases of the Mestre
// (docs/arquitetura/persistencia.md): WAL, one writer connection, a pool of
// read-only connections, and forward-only embedded migrations. The two
// databases live in subpackages: guild (mestre.db) and machine (maquina.db).
package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"modernc.org/sqlite" // registers the "sqlite" database/sql driver

	"github.com/lucasndas/mestre/internal/privdir"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Defaults from docs/arquitetura/persistencia.md#banco-configuração.
const (
	DefaultBusyTimeout = 5 * time.Second
	DefaultReadConns   = 4
)

// Options configures Open. The zero value is the documented default.
type Options struct {
	// Clock stamps schema_migrations.applied_at; nil means clock.Real.
	Clock clock.Clock
	// BusyTimeout is how long a connection waits for a lock; zero means
	// DefaultBusyTimeout.
	BusyTimeout time.Duration
	// ReadConns caps the read-only pool; zero means DefaultReadConns.
	ReadConns int
	// Synchronous is the synchronous level of the writer: "" means NORMAL,
	// the documented default. Tests that are not about durability use OFF:
	// every fsync costs tens of milliseconds on a Windows runner. OFF turns
	// every flush off, WriteDurable's and Snapshot's included; any other level
	// leaves WriteDurable committing with FULL.
	Synchronous string
	// BackupDir, when set, receives pre-migracao-<name>-<version>.db before
	// pending migrations run on a database that already has a schema; <name>
	// is the database file name without its extension (mestre, maquina), so
	// both databases can share one backups folder.
	BackupDir string
	// Replica opens a database whose schema arrives by replication (a Tower's
	// copy of the Guild database): migrations are never applied locally,
	// though a schema newer than the binary is still refused. Nothing writes
	// to it but the replication (Apply, Replace, Purge): the writer is
	// query_only.
	Replica bool
	// Capture makes the database record every change in replication_log, in
	// the same transaction, for the replication to ship (the Guild database
	// on the Citadel). See capture.go.
	Capture bool
}

// DB is an open database: one writer connection and a read-only pool.
type DB struct {
	path    string
	clock   clock.Clock
	sync    string // synchronous level of the writer
	set     Set
	opts    Options
	replica bool
	capture *capture

	mu     sync.RWMutex // guards the pools, which Replace swaps, and closed
	writer *sql.DB
	reader *sql.DB
	closed bool // Close was called: Replace does not bring the file up again

	infoMu sync.Mutex
	infos  map[string]tableInfo // primary keys of the tables, for Apply
}

// Open opens (creating it if needed) the database at path, refuses it when
// its schema is newer than set, and applies the pending migrations of set
// unless opts.Replica is set. The directory of path must exist.
func Open(ctx context.Context, path string, set Set, opts Options) (*DB, error) {
	if path == "" || strings.ContainsAny(path, "?#") {
		return nil, fmt.Errorf("store: invalid database path %q", path)
	}
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	if opts.BusyTimeout <= 0 {
		opts.BusyTimeout = DefaultBusyTimeout
	}
	if opts.ReadConns <= 0 {
		opts.ReadConns = DefaultReadConns
	}
	switch opts.Synchronous {
	case "":
		opts.Synchronous = "NORMAL"
	case "OFF", "NORMAL", "FULL":
	default:
		return nil, fmt.Errorf("store: invalid synchronous level %q (OFF, NORMAL or FULL)", opts.Synchronous)
	}

	d := &DB{
		path: path, clock: opts.Clock, sync: opts.Synchronous, set: set, opts: opts,
		replica: opts.Replica, infos: map[string]tableInfo{},
	}
	if opts.Capture && !opts.Replica {
		d.capture = newCapture(opts.Clock)
	}
	if err := d.openPools(ctx); err != nil {
		return nil, err
	}
	return d, nil
}

// openPools opens the writer, prepares the schema (unless it is a replica)
// and opens the read-only pool. It is also how Replace brings a new file up.
func (d *DB) openPools(ctx context.Context) error {
	writer, err := d.openWriter()
	if err != nil {
		return fmt.Errorf("store: open %s: %w", d.path, err)
	}
	d.writer = writer
	if err := d.prepare(ctx, d.set, d.opts); err != nil {
		writer.Close()
		return err
	}
	reader, err := sql.Open("sqlite", roDSN(d.path, readerPragmas(d.opts.BusyTimeout)))
	if err != nil {
		writer.Close()
		return fmt.Errorf("store: open %s for reading: %w", d.path, err)
	}
	reader.SetMaxOpenConns(d.opts.ReadConns)
	reader.SetMaxIdleConns(d.opts.ReadConns)
	reader.SetConnMaxLifetime(0)
	if err := reader.PingContext(ctx); err != nil {
		reader.Close()
		writer.Close()
		return fmt.Errorf("store: open %s for reading: %w", d.path, err)
	}
	d.reader = reader
	return nil
}

func (d *DB) openWriter() (*sql.DB, error) {
	pragmas := writerPragmas(d.opts.BusyTimeout, d.opts.Synchronous)
	if d.replica {
		// A deleted page is zeroed, so what a purge deletes is not left in
		// the pages the file reuses; query_only goes last, so it does not
		// block the others.
		pragmas = append(pragmas, "secure_delete(1)", "query_only(1)")
	}
	conn := dsn(d.path, pragmas, "immediate")
	var w *sql.DB
	switch {
	case d.capture != nil:
		base, err := sqlite.NewConnector(conn)
		if err != nil {
			return nil, err
		}
		w = sql.OpenDB(logConnector{Connector: base, cap: d.capture})
	case d.replica:
		base, err := sqlite.NewConnector(conn)
		if err != nil {
			return nil, err
		}
		w = sql.OpenDB(guardConnector{Connector: base})
	default:
		var err error
		if w, err = sql.Open("sqlite", conn); err != nil {
			return nil, err
		}
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)
	return w, nil
}

func (d *DB) prepare(ctx context.Context, set Set, opts Options) error {
	if err := d.writer.PingContext(ctx); err != nil {
		return fmt.Errorf("store: open %s: %w", d.path, err)
	}
	have, err := appliedVersion(ctx, d.writer)
	if err != nil {
		return fmt.Errorf("%s: %w", d.path, err)
	}
	if have > set.Latest() {
		return &NewerSchemaError{Path: d.path, Have: have, Supported: set.Latest()}
	}
	if d.capture != nil {
		// The log exists before the first migration, so the migrations are in it.
		if _, err := d.writer.ExecContext(withDDL(ctx, ddlSilent), createLogTable); err != nil {
			return fmt.Errorf("store: create %s: %w", LogTable, err)
		}
	}
	if opts.Replica || have == set.Latest() {
		return nil
	}
	if opts.BackupDir != "" && have > 0 {
		if err := d.backupBeforeMigrating(ctx, opts.BackupDir, have); err != nil {
			return err
		}
	}
	return d.migrate(ctx, set, have)
}

// backupBeforeMigrating writes pre-migracao-<name>-<version>.db, a
// consistent copy of the database at schema version have, the one a rollback
// restores, and checks it with PRAGMA quick_check: migrations never run
// without a sound copy to go back to.
func (d *DB) backupBeforeMigrating(ctx context.Context, dir string, have int) error {
	if err := privdir.Ensure(dir); err != nil {
		return fmt.Errorf("store: pre-migration backup: %w", err)
	}
	dst := filepath.Join(dir, BackupName(d.path, have))
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("store: pre-migration backup: %w", err)
	}
	// VACUUM INTO copies a consistent snapshot while the database stays in use.
	if _, err := d.writer.ExecContext(ctx, `VACUUM INTO ?`, dst); err != nil {
		return fmt.Errorf("store: pre-migration backup: %w", err)
	}
	if err := quickCheck(ctx, dst); err != nil {
		return fmt.Errorf("store: pre-migration backup %s: %w; migrations not applied", dst, err)
	}
	return nil
}

// BackupName is the file name of the pre-migration copy of the database at
// path, taken at schema version have: pre-migracao-<name>-<version>.db.
func BackupName(path string, have int) string {
	base := filepath.Base(path)
	return fmt.Sprintf("pre-migracao-%s-%d.db", strings.TrimSuffix(base, filepath.Ext(base)), have)
}

// quickCheck runs PRAGMA quick_check on the database file at path through a
// read-only connection of its own.
func quickCheck(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", dsn(path, []string{"query_only(1)"}, ""))
	if err != nil {
		return err
	}
	defer db.Close()
	return quickCheckDB(ctx, db)
}

// quickCheckDB runs PRAGMA quick_check through db.
func quickCheckDB(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA quick_check`)
	if err != nil {
		return fmt.Errorf("quick_check: %w", err)
	}
	defer rows.Close()
	var problems []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return fmt.Errorf("quick_check: %w", err)
		}
		if line != "ok" {
			problems = append(problems, line)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("quick_check: %w", err)
	}
	if len(problems) > 0 {
		return fmt.Errorf("quick_check failed: %s", strings.Join(problems, "; "))
	}
	return nil
}

// Path is the database file.
func (d *DB) Path() string { return d.path }

// Writer is the single write connection. On a replica it refuses every write:
// only the replication changes it, through Apply, Replace and Purge.
func (d *DB) Writer() *sql.DB {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.writer
}

// Reader is the pool of read-only connections.
func (d *DB) Reader() *sql.DB {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.reader
}

// IsReplica says whether the database was opened as a replica (a Tower's
// copy): the vault does not open on it.
func (d *DB) IsReplica() bool { return d.replica }

// Version is the schema version recorded in schema_migrations.
func (d *DB) Version(ctx context.Context) (int, error) { return appliedVersion(ctx, d.Reader()) }

// Flushes says whether commits reach the disk before they return: false only
// for a database opened with Options.Synchronous OFF, which the tests that are
// not about durability use. A caller that checks that a commit is durable
// (the vault) has nothing to check there.
func (d *DB) Flushes() bool { return d.sync != "OFF" }

// WriteDurable runs fn in one write transaction whose commit is on disk when
// WriteDurable returns nil. The writer normally runs with synchronous=NORMAL,
// where a WAL commit survives a crash of the process but not a power loss
// until the next checkpoint; for this transaction the connection switches to
// synchronous=FULL, so SQLite fsyncs the WAL at commit. Use it when something
// outside the database is about to depend on the commit (the vault drops the
// old key only after the rows moved to the new one). fn must use tx only:
// the writer pool has a single connection and WriteDurable holds it. With
// Options.Synchronous OFF (tests that are not about durability) it flushes
// nothing.
func (d *DB) WriteDurable(ctx context.Context, fn func(tx *sql.Tx) error) error {
	conn, err := d.Writer().Conn(ctx)
	if err != nil {
		return fmt.Errorf("store: durable write: %w", err)
	}
	defer func() {
		// Back to the level of the writer. If that fails, the connection is
		// dropped and the pool opens a new one with the pragmas of the DSN.
		if _, err := conn.ExecContext(context.WithoutCancel(ctx), `PRAGMA synchronous = `+d.sync); err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		conn.Close()
	}()
	if d.Flushes() {
		if _, err := conn.ExecContext(ctx, `PRAGMA synchronous = FULL`); err != nil {
			return fmt.Errorf("store: durable write: %w", err)
		}
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: durable write: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: durable write: %w", err)
	}
	return nil
}

// Close closes both pools. The database stays usable as a value: a goroutine
// that is still using it (a handler that was running when the process shut
// down) gets "sql: database is closed" from the pools, not a nil pointer.
func (d *DB) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	return d.closePools()
}

func (d *DB) closePools() error {
	var rerr error
	if d.reader != nil {
		rerr = d.reader.Close()
	}
	var werr error
	if d.writer != nil {
		werr = d.writer.Close()
	}
	return errors.Join(rerr, werr)
}

func writerPragmas(busy time.Duration, sync string) []string {
	return []string{
		fmt.Sprintf("busy_timeout(%d)", busy.Milliseconds()),
		// Before journal_mode: switching a new file to WAL flushes it at the
		// level in force, and a connection starts at FULL.
		"synchronous(" + sync + ")",
		"journal_mode(WAL)",
		"foreign_keys(1)",
	}
}

func readerPragmas(busy time.Duration) []string {
	return []string{
		fmt.Sprintf("busy_timeout(%d)", busy.Milliseconds()),
		"foreign_keys(1)",
		"query_only(1)",
	}
}

// dsn is the plain path followed by the driver's query: the driver only
// understands paths this way, on Windows too. Every connection is opened
// defensive: PRAGMA writable_schema and schema_version become no-ops and the
// shadow tables cannot be written.
func dsn(path string, pragmas []string, txlock string) string {
	return path + "?" + query(pragmas, txlock, nil).Encode()
}

// roDSN is the DSN of a connection that cannot write whatever it is told: the
// file is opened read-only, so PRAGMA query_only = 0 gets it nowhere. Only a
// URI carries that option; a Windows path C:\x\y.db becomes file:/C:/x/y.db.
func roDSN(path string, pragmas []string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	if len(path) >= 2 && path[1] == ':' {
		u.Path = "/" + u.Path
	}
	return u.String() + "?" + query(pragmas, "", map[string]string{"mode": "ro"}).Encode()
}

func query(pragmas []string, txlock string, extra map[string]string) url.Values {
	q := url.Values{"_defensive": {"1"}}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	if txlock != "" {
		q.Set("_txlock", txlock)
	}
	for k, v := range extra {
		q.Set(k, v)
	}
	return q
}
