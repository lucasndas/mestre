package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/pkg/clock"
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

// templates holds, per list of migrations, a database file already migrated
// with it. TestMain builds them once, outside the time of every test:
// creating and migrating a database file costs tens of milliseconds on a
// Windows runner. Tests about creating or migrating a database never use
// them.
var templates = map[string][]byte{}

func templateKey(sqls []string) string { return strings.Join(sqls, "\x00") }

func TestMain(m *testing.M) {
	if err := buildTemplates(); err != nil {
		fmt.Fprintln(os.Stderr, "store templates:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func buildTemplates() error {
	dir, err := os.MkdirTemp("", "store-templates")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	for i, sqls := range [][]string{{createItems}, {createItems, createNotes}, {createItems, createNotes, createTags}} {
		set, err := store.Load(files(sqls...), "migrations")
		if err != nil {
			return err
		}
		path := filepath.Join(dir, fmt.Sprintf("t%d.db", i))
		db, err := store.Open(context.Background(), path, set, fast)
		if err != nil {
			return err
		}
		if err := db.Close(); err != nil {
			return err
		}
		if templates[templateKey(sqls)], err = os.ReadFile(path); err != nil {
			return err
		}
	}
	return buildCapturedTemplate(dir)
}

// seed writes at path a database already migrated with sqls.
func seed(t *testing.T, path string, sqls ...string) {
	t.Helper()
	b, ok := templates[templateKey(sqls)]
	if !ok {
		t.Fatalf("no template for %d migrations", len(sqls))
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// seeded is a new x.db already migrated with sqls.
func seeded(t *testing.T, sqls ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "x.db")
	seed(t, path, sqls...)
	return path
}

// fast are the options of the tests that are not about durability: no fsync,
// which costs tens of milliseconds each on a Windows runner. The tests of
// the shipped settings and of WriteDurable use openAsShipped.
var fast = store.Options{Synchronous: "OFF"}

// open opens with synchronous=OFF unless opts sets a level.
func open(t *testing.T, path string, set store.Set, opts store.Options) *store.DB {
	t.Helper()
	if opts.Synchronous == "" {
		opts.Synchronous = fast.Synchronous
	}
	return openAsShipped(t, path, set, opts)
}

// openAsShipped opens with opts as given: synchronous=NORMAL by default.
func openAsShipped(t *testing.T, path string, set store.Set, opts store.Options) *store.DB {
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
	db := openAsShipped(t, filepath.Join(t.TempDir(), "x.db"), load(t, createItems), store.Options{})

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
	if _, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "y.db"), load(t, createItems), store.Options{Synchronous: "sometimes"}); err == nil {
		t.Error("Open accepted an unknown synchronous level")
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
	db := open(t, seeded(t, createItems), load(t, createItems), store.Options{BusyTimeout: 1500 * time.Millisecond})
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

	if _, err := store.Open(context.Background(), path, broken, fast); err == nil {
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
	seed(t, path, createItems, createNotes, createTags)
	before := fileSum(t, path)

	_, err := store.Open(context.Background(), path, load(t, createItems, createNotes), fast)
	if !errors.Is(err, store.ErrNewerSchema) {
		t.Fatalf("err = %v, want ErrNewerSchema", err)
	}
	var ne *store.NewerSchemaError
	if !errors.As(err, &ne) || ne.Have != 3 || ne.Supported != 2 {
		t.Errorf("err = %#v, want Have 3 and Supported 2", err)
	}

	// The refused database is untouched, and the replica mode refuses it too.
	if _, err := store.Open(context.Background(), path, load(t, createItems), store.Options{Replica: true, Synchronous: "OFF"}); !errors.Is(err, store.ErrNewerSchema) {
		t.Errorf("replica err = %v, want ErrNewerSchema", err)
	}
	if after := fileSum(t, path); after != before {
		t.Errorf("the refused database changed: SHA-256 %x before, %x after", before, after)
	}
	db := open(t, path, newer, store.Options{})
	if v, _ := db.Version(context.Background()); v != 3 {
		t.Errorf("Version = %d, want 3", v)
	}
}

// readOnly opens a database file to inspect it as it is: no WAL switch, no
// migration, no write.
func readOnly(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_pragma=query_only(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func fileSum(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(b)
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

	// Pending migrations on an existing database write
	// pre-migracao-<name>-<version>.db first.
	db = open(t, path, load(t, createItems, createNotes), store.Options{BackupDir: backups})
	if v, _ := db.Version(context.Background()); v != 2 {
		t.Fatalf("Version = %d, want 2", v)
	}
	bak := readOnly(t, filepath.Join(backups, "pre-migracao-x-1.db"))
	if v := scan[int](t, bak, `SELECT max(version) FROM schema_migrations`); v != 1 {
		t.Errorf("backup version = %d, want 1 (the schema before migrating)", v)
	}
	if got := scan[string](t, bak, `SELECT name FROM items`); got != "keep" {
		t.Errorf("backup row = %q, want keep", got)
	}
	if got := scan[string](t, bak, `PRAGMA quick_check`); got != "ok" {
		t.Errorf("quick_check = %q", got)
	}
	db.Close()

	// Nothing pending: no new backup.
	db = open(t, path, load(t, createItems, createNotes), store.Options{BackupDir: backups})
	db.Close()
	if matches, _ := filepath.Glob(filepath.Join(backups, "*.db")); len(matches) != 1 {
		t.Errorf("backups = %v, want only pre-migracao-x-1.db", matches)
	}
}

// The Guild and the machine databases share the backups folder: their
// pre-migration copies at the same version must not overwrite each other.
func TestPreMigrationBackupsOfTwoDatabasesDoNotCollide(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	backups := filepath.Join(dir, "backups")
	for _, name := range []string{"mestre", "maquina"} {
		path := filepath.Join(dir, name+".db")
		seed(t, path, createItems)
		db := open(t, path, load(t, createItems), store.Options{})
		if _, err := db.Writer().Exec(`INSERT INTO items (name) VALUES (?)`, name); err != nil {
			t.Fatal(err)
		}
		db.Close()
		open(t, path, load(t, createItems, createNotes), store.Options{BackupDir: backups}).Close()
	}
	for _, name := range []string{"mestre", "maquina"} {
		bak := readOnly(t, filepath.Join(backups, store.BackupName(name+".db", 1)))
		if got := scan[string](t, bak, `SELECT name FROM items`); got != name {
			t.Errorf("backup of %s holds %q", name, got)
		}
	}
	if got := store.BackupName(filepath.Join("x", "maquina.db"), 3); got != "pre-migracao-maquina-3.db" {
		t.Errorf("BackupName = %q", got)
	}
}

// A copy that fails PRAGMA quick_check stops the migration.
func TestQuickCheckRefusesADamagedCopy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "x.db")
	seed(t, path, createItems)
	db := open(t, path, load(t, createItems), store.Options{})
	for i := range 200 {
		if _, err := db.Writer().Exec(`INSERT INTO items (name) VALUES (?)`, strings.Repeat("x", 100)+string(rune('a'+i%26))); err != nil {
			t.Fatal(err)
		}
	}
	good := filepath.Join(dir, "good.db")
	if _, err := db.Writer().Exec(`VACUUM INTO ?`, good); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err := store.QuickCheck(context.Background(), good); err != nil {
		t.Fatalf("sound copy refused: %v", err)
	}
	raw, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	// Overwrite the second page (a b-tree page of items) with garbage.
	copy(raw[4096:], bytes.Repeat([]byte{0xA5}, 512))
	bad := filepath.Join(dir, "bad.db")
	if err := os.WriteFile(bad, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.QuickCheck(context.Background(), bad); err == nil {
		t.Fatal("damaged copy passed quick_check")
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
	seed(t, main, createItems)
	db = open(t, main, set, store.Options{Replica: true})
	if v, _ := db.Version(context.Background()); v != 1 {
		t.Errorf("Version = %d, want 1: a replica must not migrate", v)
	}
}

// A handler that is still running when the database is closed gets an error,
// not a nil pointer that takes the whole process down.
func TestUsingTheDatabaseAfterCloseFailsWithAnErrorInsteadOfPanicking(t *testing.T) {
	t.Parallel()
	db := open(t, seeded(t, createItems), load(t, createItems), store.Options{})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := db.Reader().QueryRowContext(ctx, `SELECT 1`).Scan(new(int)); err == nil {
		t.Error("a read after Close succeeded")
	}
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO items (name) VALUES ('x')`); err == nil {
		t.Error("a write after Close succeeded")
	}
	if _, err := db.Version(ctx); err == nil {
		t.Error("Version after Close succeeded")
	}
	if err := db.Close(); err != nil {
		t.Errorf("a second Close = %v", err)
	}
}

// CA-PERSIST-07
func TestReaderSeesOnlyCommittedWhileWriterWorks(t *testing.T) {
	t.Parallel()
	path := seeded(t, createItems)
	// A 1 ms busy timeout: a read that had to wait for the writer would fail
	// with SQLITE_BUSY instead of waiting.
	db := open(t, path, load(t, createItems), store.Options{BusyTimeout: time.Millisecond})
	ctx := context.Background()

	// A tiny page cache makes the open transaction spill its pages to disk
	// before the commit. In rollback-journal mode a spill takes the exclusive
	// lock and readers get SQLITE_BUSY; in WAL the pages go to the WAL and
	// readers keep reading the last commit.
	if _, err := db.Writer().ExecContext(ctx, `PRAGMA cache_size = 5`); err != nil {
		t.Fatal(err)
	}
	onDisk := func() int64 { return fileSize(t, path) + fileSize(t, path+"-wal") }
	before := onDisk()
	tx, err := db.Writer().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	for range 300 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO items (name) VALUES (?)`, strings.Repeat("a", 1000)); err != nil {
			t.Fatal(err)
		}
	}
	if onDisk() <= before {
		t.Fatal("the open transaction did not spill to disk; the test proves nothing")
	}

	readCtx, cancel := clock.WithTimeout(ctx, clock.Real{}, 2*time.Second)
	defer cancel()
	var n int
	if err := db.Reader().QueryRowContext(readCtx, `SELECT count(*) FROM items`).Scan(&n); err != nil {
		t.Fatalf("read during the write transaction waited or failed: %v", err)
	}
	if n != 0 {
		t.Errorf("reader saw %d uncommitted rows", n)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := scan[int](t, db.Reader(), `SELECT count(*) FROM items`); got != 300 {
		t.Errorf("reader saw %d rows after commit, want 300", got)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}

// The vault drops an old key only after a durable commit: the writer runs
// that transaction with synchronous=FULL and goes back to NORMAL.
func TestWriteDurableCommitsWithFullSync(t *testing.T) {
	t.Parallel()
	db := openAsShipped(t, seeded(t, createItems), load(t, createItems), store.Options{})
	ctx := context.Background()
	err := db.WriteDurable(ctx, func(tx *sql.Tx) error {
		var level int
		if err := tx.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&level); err != nil {
			return err
		}
		if level != 2 {
			t.Errorf("synchronous inside the durable transaction = %d, want 2 (FULL)", level)
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO items (name) VALUES ('durable')`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := scan[int](t, db.Reader(), `SELECT count(*) FROM items WHERE name = 'durable'`); got != 1 {
		t.Errorf("committed rows = %d, want 1", got)
	}
	if got := scan[int](t, db.Writer(), `PRAGMA synchronous`); got != 1 {
		t.Errorf("writer synchronous after the durable write = %d, want 1 (NORMAL)", got)
	}

	boom := errors.New("boom")
	err = db.WriteDurable(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO items (name) VALUES ('lost')`); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the error of fn", err)
	}
	if got := scan[int](t, db.Reader(), `SELECT count(*) FROM items WHERE name = 'lost'`); got != 0 {
		t.Error("a failed durable write was committed")
	}
	if got := scan[int](t, db.Writer(), `PRAGMA synchronous`); got != 1 {
		t.Errorf("writer synchronous after a failed durable write = %d, want 1", got)
	}
}

func TestForeignKeysAreEnforced(t *testing.T) {
	t.Parallel()
	db := open(t, seeded(t, createItems, createNotes), load(t, createItems, createNotes), store.Options{})
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
	path := seeded(t, createItems, createNotes)
	set := load(t, createItems, createNotes)
	db := open(t, path, set, store.Options{})
	if _, err := db.Writer().Exec(`DELETE FROM schema_migrations WHERE version = 1`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := store.Open(context.Background(), path, set, fast); err == nil {
		t.Error("Open accepted schema_migrations with a missing version")
	}
}

func TestOpenRejectsUnusablePaths(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"", "a?b.db", "a#b.db", filepath.Join(t.TempDir(), "missing-dir", "x.db")} {
		if _, err := store.Open(context.Background(), p, load(t, createItems), fast); err == nil {
			t.Errorf("Open(%q) succeeded", p)
		}
	}
}
