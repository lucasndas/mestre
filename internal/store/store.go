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
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

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
	// every fsync costs tens of milliseconds on a Windows runner. WriteDurable
	// always commits with FULL, whatever the level.
	Synchronous string
	// BackupDir, when set, receives pre-migracao-<name>-<version>.db before
	// pending migrations run on a database that already has a schema; <name>
	// is the database file name without its extension (mestre, maquina), so
	// both databases can share one backups folder.
	BackupDir string
	// Replica opens a database whose schema arrives by replication (a Tower's
	// copy of the Guild database): migrations are never applied locally,
	// though a schema newer than the binary is still refused.
	Replica bool
}

// DB is an open database: one writer connection and a read-only pool.
type DB struct {
	path   string
	writer *sql.DB
	reader *sql.DB
	clock  clock.Clock
	sync   string // synchronous level of the writer
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

	writer, err := sql.Open("sqlite", dsn(path, writerPragmas(opts.BusyTimeout, opts.Synchronous), "immediate"))
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)
	writer.SetConnMaxLifetime(0)
	d := &DB{path: path, writer: writer, clock: opts.Clock, sync: opts.Synchronous}

	if err := d.prepare(ctx, set, opts); err != nil {
		writer.Close()
		return nil, err
	}

	reader, err := sql.Open("sqlite", dsn(path, readerPragmas(opts.BusyTimeout), ""))
	if err != nil {
		writer.Close()
		return nil, fmt.Errorf("store: open %s for reading: %w", path, err)
	}
	reader.SetMaxOpenConns(opts.ReadConns)
	reader.SetMaxIdleConns(opts.ReadConns)
	reader.SetConnMaxLifetime(0)
	d.reader = reader
	if err := reader.PingContext(ctx); err != nil {
		d.Close()
		return nil, fmt.Errorf("store: open %s for reading: %w", path, err)
	}
	return d, nil
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
	if err := os.MkdirAll(dir, 0o700); err != nil {
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

// Writer is the single write connection. On a replica only the replication
// uses it.
func (d *DB) Writer() *sql.DB { return d.writer }

// Reader is the pool of read-only connections.
func (d *DB) Reader() *sql.DB { return d.reader }

// Version is the schema version recorded in schema_migrations.
func (d *DB) Version(ctx context.Context) (int, error) { return appliedVersion(ctx, d.reader) }

// WriteDurable runs fn in one write transaction whose commit is on disk when
// WriteDurable returns nil. The writer normally runs with synchronous=NORMAL,
// where a WAL commit survives a crash of the process but not a power loss
// until the next checkpoint; for this transaction the connection switches to
// synchronous=FULL, so SQLite fsyncs the WAL at commit. Use it when something
// outside the database is about to depend on the commit (the vault drops the
// old key only after the rows moved to the new one). fn must use tx only:
// the writer pool has a single connection and WriteDurable holds it.
func (d *DB) WriteDurable(ctx context.Context, fn func(tx *sql.Tx) error) error {
	conn, err := d.writer.Conn(ctx)
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
	if _, err := conn.ExecContext(ctx, `PRAGMA synchronous = FULL`); err != nil {
		return fmt.Errorf("store: durable write: %w", err)
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

// Close closes both pools.
func (d *DB) Close() error {
	var rerr error
	if d.reader != nil {
		rerr = d.reader.Close()
	}
	return errors.Join(rerr, d.writer.Close())
}

func writerPragmas(busy time.Duration, sync string) []string {
	return []string{
		fmt.Sprintf("busy_timeout(%d)", busy.Milliseconds()),
		"journal_mode(WAL)",
		"synchronous(" + sync + ")",
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
// understands paths this way, on Windows too.
func dsn(path string, pragmas []string, txlock string) string {
	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	if txlock != "" {
		q.Set("_txlock", txlock)
	}
	return path + "?" + q.Encode()
}
