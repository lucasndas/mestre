package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/pkg/clock"
)

func files(sql ...string) fstest.MapFS {
	names := []string{"0001_items.sql", "0002_notes.sql", "0003_tags.sql"}
	fsys := fstest.MapFS{}
	for i, s := range sql {
		fsys["migrations/"+names[i]] = &fstest.MapFile{Data: []byte(s)}
	}
	return fsys
}

const (
	createItems = `CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT NOT NULL);`
	createNotes = `CREATE TABLE notes (id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES items(id));`
	createTags  = `CREATE TABLE tags (id INTEGER PRIMARY KEY);`
)

func load(t *testing.T, sql ...string) store.Set {
	t.Helper()
	set, err := store.Load(files(sql...), "migrations")
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func open(t *testing.T, path string, set store.Set, opts store.Options) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), path, set, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func scan[T any](t *testing.T, db *sql.DB, query string, args ...any) T {
	t.Helper()
	var v T
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return v
}

// CA-PERSIST-01
func TestOpenConfiguresEveryConnection(t *testing.T) {
	t.Parallel()
	db := open(t, filepath.Join(t.TempDir(), "x.db"), load(t, createItems), store.Options{})

	for name, pool := range map[string]*sql.DB{"writer": db.Writer(), "reader": db.Reader()} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := scan[string](t, pool, `PRAGMA journal_mode`); got != "wal" {
				t.Errorf("journal_mode = %q, want wal", got)
			}
			if got := scan[int](t, pool, `PRAGMA foreign_keys`); got != 1 {
				t.Errorf("foreign_keys = %d, want 1", got)
			}
			if got := scan[int](t, pool, `PRAGMA busy_timeout`); got != 5000 {
				t.Errorf("busy_timeout = %d, want 5000", got)
			}
		})
	}
	if got := scan[int](t, db.Writer(), `PRAGMA synchronous`); got != 1 {
		t.Errorf("writer synchronous = %d, want 1 (NORMAL)", got)
	}

	// The reader pool cannot write; the writer is a single connection.
	if _, err := db.Reader().Exec(`INSERT INTO items (name) VALUES ('x')`); err == nil {
		t.Error("write through the reader pool succeeded")
	}
	if got := db.Writer().Stats().MaxOpenConnections; got != 1 {
		t.Errorf("writer MaxOpenConnections = %d, want 1", got)
	}
}

func TestOpenHonoursBusyTimeoutOption(t *testing.T) {
	t.Parallel()
	db := open(t, filepath.Join(t.TempDir(), "x.db"), load(t, createItems), store.Options{BusyTimeout: 1500 * time.Millisecond})
	if got := scan[int](t, db.Writer(), `PRAGMA busy_timeout`); got != 1500 {
		t.Errorf("busy_timeout = %d, want 1500", got)
	}
}

// CA-PERSIST-02
func TestOpenAppliesMigrationsInOrderOnce(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "x.db")
	fake := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	set := load(t, createItems, createNotes)

	db := open(t, path, set, store.Options{Clock: fake})
	if v, err := db.Version(context.Background()); err != nil || v != 2 {
		t.Fatalf("Version = %d, %v; want 2", v, err)
	}
	if got := scan[string](t, db.Reader(), `SELECT group_concat(version || ':' || name, ',') FROM (SELECT * FROM schema_migrations ORDER BY version)`); got != "1:items,2:notes" {
		t.Errorf("schema_migrations = %q", got)
	}
	if got := scan[int64](t, db.Reader(), `SELECT min(applied_at) FROM schema_migrations`); got != 1_700_000_000_000 {
		t.Errorf("applied_at = %d, want the fake clock's Unix ms", got)
	}
	db.Close()

	// Reopening neither reapplies (CREATE TABLE would fail) nor changes rows.
	fake.Advance(time.Hour)
	db = open(t, path, set, store.Options{Clock: fake})
	if got := scan[int](t, db.Reader(), `SELECT count(*) FROM schema_migrations`); got != 2 {
		t.Errorf("rows after reopen = %d, want 2", got)
	}
	if got := scan[int64](t, db.Reader(), `SELECT max(applied_at) FROM schema_migrations`); got != 1_700_000_000_000 {
		t.Errorf("applied_at changed on reopen: %d", got)
	}

	// A later binary with one more migration applies only the new one.
	db.Close()
	db = open(t, path, load(t, createItems, createNotes, createTags), store.Options{Clock: fake})
	if v, _ := db.Version(context.Background()); v != 3 {
		t.Errorf("Version after upgrade = %d, want 3", v)
	}
}

// CA-PERSIST-03
func TestFailedMigrationIsRolledBackWhole(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "x.db")
	broken := load(t, createItems, `CREATE TABLE half (id INTEGER); INSERT INTO missing_table VALUES (1);`)

	if _, err := store.Open(context.Background(), path, broken, store.Options{}); err == nil {
		t.Fatal("Open succeeded with a failing migration")
	}

	db := open(t, path, load(t, createItems), store.Options{})
	if v, _ := db.Version(context.Background()); v != 1 {
		t.Errorf("Version = %d, want 1: version 2 must not be recorded", v)
	}
	if got := scan[int](t, db.Reader(), `SELECT count(*) FROM sqlite_master WHERE name = 'half'`); got != 0 {
		t.Error("table created by the failed migration survived")
	}
}

// CA-PERSIST-04
func TestOpenRefusesSchemaNewerThanBinary(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "x.db")
	newer := load(t, createItems, createNotes, createTags)
	open(t, path, newer, store.Options{}).Close()

	_, err := store.Open(context.Background(), path, load(t, createItems, createNotes), store.Options{})
	if !errors.Is(err, store.ErrNewerSchema) {
		t.Fatalf("err = %v, want ErrNewerSchema", err)
	}
	var ne *store.NewerSchemaError
	if !errors.As(err, &ne) || ne.Have != 3 || ne.Supported != 2 {
		t.Errorf("err = %#v, want Have 3 and Supported 2", err)
	}

	// The refused database is untouched, and the replica mode refuses it too.
	if _, err := store.Open(context.Background(), path, load(t, createItems), store.Options{Replica: true}); !errors.Is(err, store.ErrNewerSchema) {
		t.Errorf("replica err = %v, want ErrNewerSchema", err)
	}
	db := open(t, path, newer, store.Options{})
	if v, _ := db.Version(context.Background()); v != 3 {
		t.Errorf("Version = %d, want 3", v)
	}
}

// CA-PERSIST-05
func TestPreMigrationBackup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "x.db")
	backups := filepath.Join(dir, "backups")

	// A new database has nothing to save.
	db := open(t, path, load(t, createItems), store.Options{BackupDir: backups})
	if _, err := db.Writer().Exec(`INSERT INTO items (name) VALUES ('keep')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if matches, _ := filepath.Glob(filepath.Join(backups, "*")); len(matches) != 0 {
		t.Fatalf("backup written for a new database: %v", matches)
	}

	// Pending migrations on an existing database write pre-migracao-<version>.db first.
	db = open(t, path, load(t, createItems, createNotes), store.Options{BackupDir: backups})
	if v, _ := db.Version(context.Background()); v != 2 {
		t.Fatalf("Version = %d, want 2", v)
	}
	bak := open(t, filepath.Join(backups, "pre-migracao-1.db"), load(t, createItems), store.Options{})
	if v, _ := bak.Version(context.Background()); v != 1 {
		t.Errorf("backup version = %d, want 1 (the schema before migrating)", v)
	}
	if got := scan[string](t, bak.Reader(), `SELECT name FROM items`); got != "keep" {
		t.Errorf("backup row = %q, want keep", got)
	}
	if got := scan[string](t, bak.Reader(), `PRAGMA quick_check`); got != "ok" {
		t.Errorf("quick_check = %q", got)
	}
	db.Close()

	// Nothing pending: no new backup.
	db = open(t, path, load(t, createItems, createNotes), store.Options{BackupDir: backups})
	db.Close()
	if matches, _ := filepath.Glob(filepath.Join(backups, "*.db")); len(matches) != 1 {
		t.Errorf("backups = %v, want only pre-migracao-1.db", matches)
	}
}

// CA-PERSIST-06
func TestReplicaNeverMigrates(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "replica.db")
	set := load(t, createItems, createNotes)

	// A fresh replica waits for the replication to bring the schema.
	db := open(t, path, set, store.Options{Replica: true})
	if got := scan[int](t, db.Reader(), `SELECT count(*) FROM sqlite_master WHERE type = 'table'`); got != 0 {
		t.Errorf("replica has %d tables, want none", got)
	}
	if v, err := db.Version(context.Background()); err != nil || v != 0 {
		t.Errorf("Version = %d, %v; want 0", v, err)
	}
	db.Close()

	// An older replica is not migrated either.
	main := filepath.Join(t.TempDir(), "main.db")
	open(t, main, load(t, createItems), store.Options{}).Close()
	db = open(t, main, set, store.Options{Replica: true})
	if v, _ := db.Version(context.Background()); v != 1 {
		t.Errorf("Version = %d, want 1: a replica must not migrate", v)
	}
}

// CA-PERSIST-07
func TestReaderSeesOnlyCommittedWhileWriterWorks(t *testing.T) {
	t.Parallel()
	db := open(t, filepath.Join(t.TempDir(), "x.db"), load(t, createItems), store.Options{})
	ctx := context.Background()

	tx, err := db.Writer().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO items (name) VALUES ('a')`); err != nil {
		t.Fatal(err)
	}
	// WAL: the read does not wait for the open write transaction.
	if got := scan[int](t, db.Reader(), `SELECT count(*) FROM items`); got != 0 {
		t.Errorf("reader saw %d uncommitted rows", got)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := scan[int](t, db.Reader(), `SELECT count(*) FROM items`); got != 1 {
		t.Errorf("reader saw %d rows after commit, want 1", got)
	}
}

func TestForeignKeysAreEnforced(t *testing.T) {
	t.Parallel()
	db := open(t, filepath.Join(t.TempDir(), "x.db"), load(t, createItems, createNotes), store.Options{})
	if _, err := db.Writer().Exec(`INSERT INTO notes (item_id) VALUES (42)`); err == nil {
		t.Error("insert violating a foreign key succeeded")
	}
}

func TestLoadValidatesTheMigrationFiles(t *testing.T) {
	t.Parallel()
	cases := map[string]fstest.MapFS{
		"gap":         {"m/0001_a.sql": {Data: []byte("")}, "m/0003_c.sql": {Data: []byte("")}},
		"starts at 2": {"m/0002_a.sql": {Data: []byte("")}},
		"duplicate":   {"m/0001_a.sql": {Data: []byte("")}, "m/0001_b.sql": {Data: []byte("")}},
		"bad name":    {"m/1_a.sql": {Data: []byte("")}},
		"stray file":  {"m/0001_a.sql": {Data: []byte("")}, "m/notes.txt": {Data: []byte("")}},
		"no dir":      {},
	}
	for name, fsys := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := store.Load(fsys, "m"); err == nil {
				t.Error("Load accepted the set")
			}
		})
	}
	set, err := store.Load(fstest.MapFS{"m/0001_a.sql": {Data: []byte("x")}, "m/0002_b_c.sql": {Data: []byte("y")}}, "m")
	if err != nil {
		t.Fatal(err)
	}
	if got := set.Migrations(); set.Latest() != 2 || got[1].Name != "b_c" || got[1].SQL != "y" {
		t.Errorf("set = %+v", got)
	}
}

func TestOpenRejectsAnInconsistentVersionTable(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "x.db")
	set := load(t, createItems, createNotes)
	db := open(t, path, set, store.Options{})
	if _, err := db.Writer().Exec(`DELETE FROM schema_migrations WHERE version = 1`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := store.Open(context.Background(), path, set, store.Options{}); err == nil {
		t.Error("Open accepted schema_migrations with a missing version")
	}
}

func TestOpenRejectsUnusablePaths(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"", "a?b.db", "a#b.db", filepath.Join(t.TempDir(), "missing-dir", "x.db")} {
		if _, err := store.Open(context.Background(), p, load(t, createItems), store.Options{}); err == nil {
			t.Errorf("Open(%q) succeeded", p)
		}
	}
}
