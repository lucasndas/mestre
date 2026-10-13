package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
)

// ErrNewerSchema is wrapped by the error Open returns when the database was
// migrated by a newer binary than this one.
var ErrNewerSchema = errors.New("database schema is newer than this binary")

// NewerSchemaError reports a database whose schema version is higher than the
// latest migration this binary embeds. Open never touches such a database.
type NewerSchemaError struct {
	Path      string
	Have      int // version recorded in the database
	Supported int // latest version embedded in this binary
}

func (e *NewerSchemaError) Error() string {
	return fmt.Sprintf("store: %s is at schema version %d but this binary supports up to %d", e.Path, e.Have, e.Supported)
}

func (e *NewerSchemaError) Is(target error) bool { return target == ErrNewerSchema }

// Migration is one numbered, forward-only SQL file.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// Set is the ordered list of migrations of one database kind. Versions run
// 1..n with no gaps.
type Set struct {
	migrations []Migration
}

// Latest is the highest version in the set (0 for an empty set).
func (s Set) Latest() int { return len(s.migrations) }

// Migrations returns a copy of the migrations, in order.
func (s Set) Migrations() []Migration { return append([]Migration(nil), s.migrations...) }

var migrationFile = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

// Load reads the migration files in dir of fsys (typically an embed.FS). Each
// file is named NNNN_name.sql; the numbers must run 1..n with no gap or
// repeat, and nothing else may live in the directory.
func Load(fsys fs.FS, dir string) (Set, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return Set{}, fmt.Errorf("store: read migrations: %w", err)
	}
	var set Set
	for i, e := range entries { // ReadDir sorts by file name
		m := migrationFile.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			return Set{}, fmt.Errorf("store: migration %q does not match NNNN_name.sql", e.Name())
		}
		v, _ := strconv.Atoi(m[1])
		if v != i+1 {
			return Set{}, fmt.Errorf("store: migration %q has version %d, want %d (versions run 1..n without gaps)", e.Name(), v, i+1)
		}
		body, err := fs.ReadFile(fsys, dir+"/"+e.Name())
		if err != nil {
			return Set{}, fmt.Errorf("store: read migration %q: %w", e.Name(), err)
		}
		set.migrations = append(set.migrations, Migration{Version: v, Name: m[2], SQL: string(body)})
	}
	return set, nil
}

// MustLoad is Load for embedded sets, where a malformed set is a programming
// error that the package tests catch.
func MustLoad(fsys fs.FS, dir string) Set {
	s, err := Load(fsys, dir)
	if err != nil {
		panic(err)
	}
	return s
}

const createMigrationsTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version    INTEGER PRIMARY KEY,
	name       TEXT    NOT NULL,
	applied_at INTEGER NOT NULL
)`

// appliedVersion returns the schema version recorded in db: 0 when the table
// is absent or empty. It fails when the recorded versions are not 1..k.
func appliedVersion(ctx context.Context, db *sql.DB) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: look for schema_migrations: %w", err)
	}
	if n == 0 {
		return 0, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return 0, fmt.Errorf("store: read schema_migrations: %w", err)
	}
	defer rows.Close()
	last := 0
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return 0, fmt.Errorf("store: read schema_migrations: %w", err)
		}
		if v != last+1 {
			return 0, fmt.Errorf("store: schema_migrations is inconsistent: version %d follows %d", v, last)
		}
		last = v
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("store: read schema_migrations: %w", err)
	}
	return last, nil
}

// migrate applies every migration above from, each in its own transaction
// together with its schema_migrations row, so a failure leaves the database
// at the last fully applied version. The writer is opened with immediate
// transactions, so a second process migrating the same file waits and then
// sees the versions already applied.
func (d *DB) migrate(ctx context.Context, set Set, from int) error {
	if _, err := d.writer.ExecContext(ctx, createMigrationsTable); err != nil {
		return fmt.Errorf("store: create schema_migrations: %w", err)
	}
	for _, m := range set.migrations[from:] {
		if err := d.apply(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

func (d *DB) apply(ctx context.Context, m Migration) error {
	tx, err := d.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin migration %04d_%s: %w", m.Version, m.Name, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	var done int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version = ?`, m.Version).Scan(&done); err != nil {
		return fmt.Errorf("store: check migration %04d_%s: %w", m.Version, m.Name, err)
	}
	if done > 0 { // applied meanwhile by another process
		return nil
	}
	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return fmt.Errorf("store: migration %04d_%s: %w", m.Version, m.Name, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		m.Version, m.Name, d.clock.Now().UnixMilli()); err != nil {
		return fmt.Errorf("store: record migration %04d_%s: %w", m.Version, m.Name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit migration %04d_%s: %w", m.Version, m.Name, err)
	}
	return nil
}
