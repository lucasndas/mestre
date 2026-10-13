package store_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/pkg/clock"
)

const (
	createParents = `CREATE TABLE parents (id INTEGER PRIMARY KEY, name TEXT NOT NULL, note TEXT);
CREATE TABLE children (id INTEGER PRIMARY KEY, parent_id INTEGER NOT NULL REFERENCES parents(id) ON DELETE CASCADE, label TEXT NOT NULL);`
	createMixed = `CREATE TABLE named (code TEXT PRIMARY KEY, num REAL, raw BLOB, big INTEGER);
CREATE TABLE seq (seq INTEGER PRIMARY KEY AUTOINCREMENT, body TEXT NOT NULL);
CREATE TABLE secrets (name TEXT PRIMARY KEY, value BLOB NOT NULL);`
)

var logSet = store.MustLoad(fsOf(createParents, createMixed), "migrations")

func fsOf(sqls ...string) fstest.MapFS {
	m := fstest.MapFS{}
	for i, s := range sqls {
		m[fmt.Sprintf("migrations/%04d_m%d.sql", i+1, i+1)] = &fstest.MapFile{Data: []byte(s)}
	}
	return m
}

// capturedTemplate is a database of logSet migrated with the log on, built
// once by TestMain: the tests that are not about migrating start from a copy
// (creating and migrating a file costs tens of milliseconds on a Windows runner).
var capturedTemplate []byte

func buildCapturedTemplate(dir string) error {
	path := filepath.Join(dir, "captured.db")
	db, err := store.Open(context.Background(), path, logSet, store.Options{Capture: true, Synchronous: "OFF"})
	if err != nil {
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	capturedTemplate, err = os.ReadFile(path)
	return err
}

// openPrimary opens a copy of the template as the main database.
func openPrimary(t *testing.T, clk clock.Clock) *store.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "p.db")
	if err := os.WriteFile(path, capturedTemplate, 0o600); err != nil {
		t.Fatal(err)
	}
	return open(t, path, logSet, store.Options{Capture: true, Clock: clk})
}

func exec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// readLog returns the rows of the change log and their decoded changes.
func readLog(t *testing.T, db *store.DB) ([]store.LogRow, [][]store.Change) {
	t.Helper()
	rows, err := db.LogSince(context.Background(), 0, 1<<20, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	var out [][]store.Change
	for _, r := range rows {
		cs, err := store.DecodeChanges(r.Body)
		if err != nil {
			t.Fatalf("lsn %d: %v", r.LSN, err)
		}
		out = append(out, cs)
	}
	return rows, out
}

func lastLSN(t *testing.T, db *store.DB) uint64 {
	t.Helper()
	n, err := db.LastLSN(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// The log records every committed change, in the transaction that made it:
// the migrations first, then each transaction as one record with its changes in
// the order they happened, cascades included, whole rows, every kind of value.
func TestTheLogFollowsEveryCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fake := clock.NewFake(time.UnixMilli(1_700_000_000_000))
	db := open(t, filepath.Join(t.TempDir(), "p.db"), logSet, store.Options{Capture: true, Clock: fake})

	// The migrations are the first records: the SQL and the row of schema_migrations.
	rows, recs := readLog(t, db)
	if len(rows) != 2 {
		t.Fatalf("log has %d records, want one per migration", len(rows))
	}
	for i, cs := range recs {
		if len(cs) != 2 || cs[0].Op != store.OpSQL || cs[1].Op != store.OpInsert || cs[1].Table != "schema_migrations" {
			t.Fatalf("record %d = %+v, want the SQL and the schema_migrations row", rows[i].LSN, cs)
		}
	}
	if !strings.Contains(recs[0][0].SQL, "CREATE TABLE parents") || rows[0].LSN != 1 || rows[1].LSN != 2 || rows[0].At != 1_700_000_000_000 {
		t.Errorf("first migration: lsn %d at %d sql %q", rows[0].LSN, rows[0].At, recs[0][0].SQL)
	}
	base := lastLSN(t, db)

	// One transaction, one record; cascades are changes of their own.
	tx, err := db.Writer().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO parents (id, name) VALUES (1, 'a'), (2, 'b')`,
		`INSERT INTO children (parent_id, label) VALUES (1, 'x'), (1, 'y'), (2, 'z')`,
		`UPDATE parents SET note = 'n' WHERE id = 1`,
		`DELETE FROM parents WHERE id = 1`, // cascades to two children
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rows, recs = readLog(t, db)
	if got := rows[len(rows)-1].LSN; got != base+1 || len(rows) != int(base)+1 {
		t.Fatalf("one transaction left %d records after lsn %d", len(rows)-int(base), base)
	}
	var ops []string
	for _, c := range recs[len(recs)-1] {
		ops = append(ops, c.Op.String()+" "+c.Table)
	}
	want := []string{
		"insert parents", "insert parents",
		"insert children", "insert children", "insert children",
		"update parents",
		"delete parents", "delete children", "delete children",
	}
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("changes = %v\nwant      %v", ops, want)
	}
	upd := recs[len(recs)-1][5]
	if !reflect.DeepEqual(upd.Old, []any{int64(1), "a", nil}) || !reflect.DeepEqual(upd.New, []any{int64(1), "a", "n"}) {
		t.Errorf("update = %+v, want the whole old and new rows", upd)
	}

	// Rolled back, read only, and a write that changes nothing: no lsn.
	tx, _ = db.Writer().BeginTx(ctx, nil)
	if _, err := tx.ExecContext(ctx, `INSERT INTO parents (id, name) VALUES (9, 'gone')`); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	tx, _ = db.Writer().BeginTx(ctx, nil)
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM parents`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE parents SET name = 'q' WHERE id = 404`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := lastLSN(t, db); got != base+1 {
		t.Errorf("lsn = %d, want %d: no row changed", got, base+1)
	}

	// A write outside a transaction is a transaction.
	watch := db.LogWatch()
	exec(t, db.Writer(), `INSERT INTO parents (id, name) VALUES (5, 'a')`)
	exec(t, db.Writer(), `UPDATE parents SET name = 'b' WHERE id = 5`)
	res, err := db.Writer().ExecContext(ctx, `INSERT INTO parents (name) VALUES (?)`, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := res.LastInsertId(); id != 6 {
		t.Errorf("LastInsertId = %d, want 6", id)
	}
	if got := lastLSN(t, db); got != base+4 {
		t.Errorf("lsn = %d, want %d", got, base+4)
	}
	select {
	case <-watch:
	default:
		t.Error("a commit did not wake the watcher")
	}
	// the row of an auto-assigned key is logged with the key that was chosen
	_, recs = readLog(t, db)
	if last := recs[len(recs)-1][0]; !reflect.DeepEqual(last.New, []any{int64(6), "auto", nil}) {
		t.Errorf("last insert = %+v", last)
	}

	// Every kind of value, and the writes that conflict.
	exec(t, db.Writer(), `INSERT INTO named (code, num, raw, big) VALUES (?, ?, ?, ?)`, "k", 1.5, []byte{0, 1, 2}, int64(1)<<62)
	exec(t, db.Writer(), `INSERT INTO named (code, num, raw, big) VALUES (?, NULL, ?, NULL)`, "empty", []byte{})
	exec(t, db.Writer(), `INSERT INTO seq (body) VALUES ('one'), ('two')`)
	_, recs = readLog(t, db)
	k := len(recs)
	if got := recs[k-3][0].New; !reflect.DeepEqual(got, []any{"k", 1.5, []byte{0, 1, 2}, int64(1) << 62}) {
		t.Errorf("named k = %#v", got)
	}
	if got := recs[k-2][0].New; !reflect.DeepEqual(got, []any{"empty", nil, []byte{}, nil}) {
		t.Errorf("named empty = %#v (an empty blob is not NULL)", got)
	}
	if got := recs[k-1][1].New; !reflect.DeepEqual(got, []any{int64(2), "two"}) {
		t.Errorf("seq 2 = %#v", got)
	}
	exec(t, db.Writer(), `INSERT INTO secrets (name, value) VALUES ('a', x'01')`)
	exec(t, db.Writer(), `INSERT INTO secrets (name, value) VALUES ('a', x'02') ON CONFLICT (name) DO UPDATE SET value = excluded.value`)
	exec(t, db.Writer(), `INSERT OR REPLACE INTO secrets (name, value) VALUES ('a', x'03')`)
	_, recs = readLog(t, db)
	k = len(recs)
	if c := recs[k-2][0]; c.Op != store.OpUpdate {
		t.Errorf("upsert logged as %s", c.Op)
	}
	ops = ops[:0]
	for _, c := range recs[k-1] {
		ops = append(ops, c.Op.String())
	}
	if !reflect.DeepEqual(ops, []string{"delete", "insert"}) {
		t.Errorf("replace logged as %v, want delete and insert", ops)
	}

	// The retention: rows older than the age go, then the oldest ones beyond the
	// size, and the newest row stays because the next lsn comes from it.
	fake.Advance(2 * time.Hour)
	exec(t, db.Writer(), `INSERT INTO named (code, raw) VALUES ('big', ?)`, bytes.Repeat([]byte{1}, 1000))
	exec(t, db.Writer(), `INSERT INTO named (code, raw) VALUES ('big2', ?)`, bytes.Repeat([]byte{1}, 1000))
	last := lastLSN(t, db)
	if n, err := db.PruneLog(ctx, time.Hour, 1<<30); err != nil {
		t.Fatal(err)
	} else if lo, hi, _ := db.LogBounds(ctx); lo != last-1 || hi != last || n == 0 {
		t.Errorf("after the age: removed %d, bounds %d..%d; want %d..%d", n, lo, hi, last-1, last)
	}
	if _, err := db.PruneLog(ctx, time.Hour, 1100); err != nil {
		t.Fatal(err)
	}
	if lo, hi, _ := db.LogBounds(ctx); lo != last || hi != last {
		t.Errorf("after the size: bounds %d..%d, want only %d", lo, hi, last)
	}
	fake.Advance(24 * time.Hour)
	if _, err := db.PruneLog(ctx, time.Hour, 0); err != nil {
		t.Fatal(err)
	}
	exec(t, db.Writer(), `INSERT INTO named (code) VALUES ('next')`)
	if _, hi, _ := db.LogBounds(ctx); hi != last+1 {
		t.Errorf("next lsn = %d, want %d", hi, last+1)
	}
}

// CA-REPL-11: a write that does not go through the log is refused.
func TestAWriteThatSkipsTheLogIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openPrimary(t, nil)
	base := lastLSN(t, db)

	t.Run("a prepared statement runs outside the wrapper", func(t *testing.T) {
		t.Parallel()
		st, err := db.Writer().PrepareContext(ctx, `INSERT INTO parents (id, name) VALUES (100, 'sneaky')`)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if _, err := st.ExecContext(ctx); !errors.Is(err, store.ErrUnloggedWrite) {
			t.Fatalf("Exec = %v, want the commit refused (ErrUnloggedWrite)", err)
		}
	})
	t.Run("a write that comes back in a query", func(t *testing.T) {
		t.Parallel()
		var id int
		err := db.Writer().QueryRowContext(ctx, `INSERT INTO parents (id, name) VALUES (101, 'sneaky') RETURNING id`).Scan(&id)
		if !errors.Is(err, store.ErrUnloggedWrite) {
			t.Fatalf("RETURNING write outside a transaction = %v, want ErrUnloggedWrite", err)
		}
	})
	t.Run("a schema change outside a migration", func(t *testing.T) {
		t.Parallel()
		for _, q := range []string{`CREATE TABLE loose (id INTEGER PRIMARY KEY)`, `ALTER TABLE parents ADD COLUMN extra TEXT`, `DROP TABLE children`, `-- c
		create index i on parents (name)`} {
			if _, err := db.Writer().ExecContext(ctx, q); !errors.Is(err, store.ErrSchemaOutsideMigration) {
				t.Errorf("%q = %v, want ErrSchemaOutsideMigration", q, err)
			}
		}
	})
	t.Run("savepoints", func(t *testing.T) {
		t.Parallel()
		tx, err := db.Writer().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback() //nolint:errcheck // the test ends with the transaction
		if _, err := tx.ExecContext(ctx, `SAVEPOINT s`); !errors.Is(err, store.ErrUnsupportedStatement) {
			t.Errorf("SAVEPOINT = %v", err)
		}
	})
	t.Run("a statement that fails after changing rows", func(t *testing.T) {
		t.Parallel()
		tx, err := db.Writer().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO parents (id, name) VALUES (200, 'ok')`); err != nil {
			t.Fatal(err)
		}
		// The second row repeats a key after the first was hooked, and SQLite
		// takes the whole statement back.
		if _, err := tx.ExecContext(ctx, `INSERT INTO named (code) VALUES ('a'), ('b'), ('a')`); err == nil {
			t.Fatal("insert with a repeated key succeeded")
		}
		if err := tx.Commit(); err == nil {
			t.Fatal("the transaction committed with changes the log cannot tell apart from phantoms")
		}
	})
	t.Cleanup(func() { // after the parallel subtests
		if got := lastLSN(t, db); got != base {
			t.Errorf("lsn = %d, want %d: none of those writes may have left a record", got, base)
		}
		if n := scan[int](t, db.Reader(), `SELECT count(*) FROM parents`); n != 0 {
			t.Errorf("%d parents leaked in", n)
		}
	})
}

func TestTheCodecRoundTripsAndRefusesGarbage(t *testing.T) {
	t.Parallel()
	in := []store.Change{
		{Op: store.OpSQL, SQL: "CREATE TABLE x (id INTEGER PRIMARY KEY)"},
		{Op: store.OpInsert, Table: "t", New: []any{int64(-7), 2.5, "text", []byte{1, 2}, nil}},
		{Op: store.OpUpdate, Table: "t", Old: []any{int64(1)}, New: []any{int64(2)}},
		{Op: store.OpDelete, Table: "t", Old: []any{"k"}},
	}
	out, err := store.DecodeChanges(store.EncodeChanges(in))
	if err != nil || !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip = %+v, %v", out, err)
	}
	if out, err := store.DecodeChanges(store.EncodeChanges(nil)); err != nil || len(out) != 0 {
		t.Errorf("empty = %+v, %v", out, err)
	}
	good := store.EncodeChanges(in)
	for i := range good { // every truncation is an error, never a panic
		if _, err := store.DecodeChanges(good[:i]); err == nil {
			t.Fatalf("truncated at %d decoded", i)
		}
	}
	for _, bad := range [][]byte{nil, {9}, {1, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}, append(append([]byte{}, good...), 0)} {
		if _, err := store.DecodeChanges(bad); !errors.Is(err, store.ErrBadLog) {
			t.Errorf("DecodeChanges(%v) = %v, want ErrBadLog", bad, err)
		}
	}
}

// ---- the replica ----

// replicate brings a replica up as the replication does: a full copy.
func replicate(t *testing.T, primary *store.DB, empty ...string) *store.DB {
	t.Helper()
	ctx := context.Background()
	tmp := filepath.Join(t.TempDir(), "snap.db")
	lsn, err := primary.Snapshot(ctx, tmp, store.SnapshotOptions{EmptyTables: empty})
	if err != nil {
		t.Fatal(err)
	}
	r := open(t, filepath.Join(t.TempDir(), "r.db"), logSet, store.Options{Replica: true})
	if err := r.Replace(ctx, tmp, lsn); err != nil {
		t.Fatal(err)
	}
	return r
}

func ship(t *testing.T, from, to *store.DB) {
	t.Helper()
	ctx := context.Background()
	have, _ := to.LastLSN(ctx)
	rows, err := from.LogSince(ctx, have, 1<<20, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := to.ApplyLog(ctx, rows); err != nil {
		t.Fatal(err)
	}
}

func dump(t *testing.T, db *store.DB, tables ...string) string {
	t.Helper()
	var b strings.Builder
	for _, tb := range tables {
		rows, err := db.Reader().QueryContext(context.Background(), `SELECT * FROM `+tb+` ORDER BY 1`)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		for rows.Next() {
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "%s:", tb)
			for _, v := range vals {
				fmt.Fprintf(&b, "%T=%v|", v, v)
			}
			b.WriteByte('\n')
		}
		rows.Close()
	}
	return b.String()
}

var allTables = []string{"parents", "children", "named", "seq", "secrets", "schema_migrations"}

// scanFile runs a query on a database file that no DB has open.
func scanFile[T any](t *testing.T, path, query string, args ...any) T {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_pragma=query_only(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	return scan[T](t, db, query, args...)
}

func readAll(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A replica follows the primary record by record and takes nothing else.
// CA-REPL-05: nothing writes to it but the replication.
func TestAReplicaAppliesTheRecordsInOrderAndTakesNoOtherWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p := openPrimary(t, clock.NewFake(time.UnixMilli(9)))
	exec(t, p.Writer(), `INSERT INTO parents (id, name) VALUES (1, 'a'), (2, 'b'), (3, 'c')`)
	r := replicate(t, p)

	// The same rows after a transaction with every kind of change, a key that
	// moves, a replace and an AUTOINCREMENT table.
	tx, _ := p.Writer().BeginTx(ctx, nil)
	for _, q := range []string{
		`INSERT INTO children (parent_id, label) VALUES (1, 'x'), (1, 'y'), (2, 'z')`,
		`UPDATE parents SET id = 10, note = 'moved' WHERE id = 3`,
		`INSERT INTO named (code, num, raw, big) VALUES ('k', 0.25, x'ff', 9223372036854775807)`,
		`INSERT INTO seq (body) VALUES ('s1'), ('s2')`,
		`DELETE FROM seq WHERE seq = 1`,
		`DELETE FROM parents WHERE id = 1`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	exec(t, p.Writer(), `INSERT OR REPLACE INTO named (code, num) VALUES ('k', 1)`)
	exec(t, p.Writer(), `INSERT INTO seq (body) VALUES ('s3')`)
	ship(t, p, r)
	if a, b := dump(t, p, allTables...), dump(t, r, allTables...); a != b {
		t.Fatalf("the replica differs from the primary\nprimary:\n%s\nreplica:\n%s", a, b)
	}
	// The next key of an AUTOINCREMENT table is the same on both: a promoted
	// Tower must not reuse a number.
	if a, b := scan[int](t, p.Reader(), `SELECT seq FROM sqlite_sequence WHERE name = 'seq'`), scan[int](t, r.Reader(), `SELECT seq FROM sqlite_sequence WHERE name = 'seq'`); a != b {
		t.Errorf("sqlite_sequence = %d on the replica, %d on the primary", b, a)
	}
	if lp, lr := lastLSN(t, p), lastLSN(t, r); lr != lp {
		t.Errorf("replica at %d, primary at %d", lr, lp)
	}

	// A repeat is skipped and a hole stops the batch, after the records before it.
	have := lastLSN(t, r)
	for i := range 4 {
		exec(t, p.Writer(), `INSERT INTO named (code) VALUES (?)`, fmt.Sprint("n", i))
	}
	rows, _ := p.LogSince(ctx, have, 100, 1<<30)
	if n, err := r.ApplyLog(ctx, rows[:2]); err != nil || n != 2 {
		t.Fatalf("first two = %d, %v", n, err)
	}
	if n, err := r.ApplyLog(ctx, rows[:2]); err != nil || n != 0 {
		t.Fatalf("a repeat = %d, %v; want skipped", n, err)
	}
	n, err := r.ApplyLog(ctx, rows[3:])
	var gap *store.GapError
	if !errors.As(err, &gap) || n != 0 || gap.Have != have+2 || gap.Got != rows[3].LSN {
		t.Fatalf("hole = %d, %v; want a GapError from %d", n, err, have+2)
	}
	if got := lastLSN(t, r); got != have+2 {
		t.Errorf("replica at %d, the hole must not move it", got)
	}
	if _, err := r.ApplyLog(ctx, rows[2:]); err != nil {
		t.Fatal(err)
	}

	// A record that does not fit leaves nothing behind.
	at := lastLSN(t, r)
	var div *store.DivergedError
	bad := store.EncodeChanges([]store.Change{
		{Op: store.OpInsert, Table: "named", New: []any{"fresh", nil, nil, nil}},
		{Op: store.OpDelete, Table: "parents", Old: []any{int64(77), "x", nil}},
	})
	if _, err := r.ApplyLog(ctx, []store.LogRow{{LSN: at + 1, At: 1, Body: bad}}); !errors.As(err, &div) {
		t.Fatalf("delete of a missing row = %v, want DivergedError", err)
	}
	if got := lastLSN(t, r); got != at {
		t.Errorf("replica at %d: a failed batch must leave nothing", got)
	}
	if n := scan[int](t, r.Reader(), `SELECT count(*) FROM named WHERE code = 'fresh'`); n != 0 {
		t.Error("the first change of a failed record stayed")
	}

	// Nothing else writes to it, through the writer or the readers, and the guard is back.
	for _, q := range []string{
		`INSERT INTO parents (id, name) VALUES (50, 'a')`,
		`DELETE FROM schema_migrations`,
		`CREATE TABLE loose (id INTEGER PRIMARY KEY)`,
		`INSERT INTO replication_log (lsn, at, body) VALUES (99999, 0, x'01')`,
	} {
		if _, err := r.Writer().ExecContext(ctx, q); err == nil {
			t.Errorf("%q worked on a replica", q)
		}
		if _, err := r.Reader().ExecContext(ctx, q); err == nil {
			t.Errorf("%q worked through the reader", q)
		}
	}
	if tx, err := r.Writer().BeginTx(ctx, nil); err == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO parents (id, name) VALUES (50, 'a')`)
		_ = tx.Rollback()
		if err == nil {
			t.Error("a transaction wrote to a replica")
		}
	}
	if got := scan[int](t, r.Writer(), `PRAGMA foreign_keys`); got != 1 {
		t.Errorf("foreign_keys = %d after an apply, want 1", got)
	}
	if !r.IsReplica() || p.IsReplica() {
		t.Error("IsReplica is wrong")
	}
	if _, err := p.ApplyLog(ctx, nil); !errors.Is(err, store.ErrNotReplica) {
		t.Errorf("ApplyLog on a primary = %v", err)
	}
	if err := p.Replace(ctx, filepath.Join(t.TempDir(), "x.db"), 0); !errors.Is(err, store.ErrNotReplica) {
		t.Errorf("Replace on a primary = %v", err)
	}
	before := dump(t, r, allTables...)

	// A full copy is checked before it is swapped in.
	good := filepath.Join(t.TempDir(), "good.db")
	lsn, err := p.Snapshot(ctx, good, store.SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Replace(ctx, good, lsn+1); err == nil || !strings.Contains(err.Error(), "announced") {
		t.Errorf("Replace with the wrong lsn = %v", err)
	}
	junk := filepath.Join(t.TempDir(), "junk.db")
	if err := os.WriteFile(junk, bytes.Repeat([]byte("not a database "), 500), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.Replace(ctx, junk, 0); err == nil {
		t.Error("Replace accepted a file that is not a database")
	}
	if after := dump(t, r, allTables...); after != before {
		t.Error("a refused copy changed the replica")
	}
	// A schema newer than the binary is refused.
	small := store.MustLoad(fsOf(createParents), "migrations")
	old := open(t, filepath.Join(t.TempDir(), "old.db"), small, store.Options{Replica: true})
	if err := old.Replace(ctx, good, lsn); !errors.Is(err, store.ErrNewerSchema) {
		t.Errorf("Replace with a schema newer than the binary = %v, want ErrNewerSchema", err)
	}

	// The copy of an excluded machine has the schema of the vault tables and
	// none of their rows, not even on disk; the purge of a machine that was
	// excluded later does the same to a replica that had them.
	marker := bytes.Repeat([]byte("TOP-SECRET-CIPHERTEXT-"), 20)
	exec(t, p.Writer(), `INSERT INTO secrets (name, value) VALUES ('token', ?)`, marker)
	exec(t, p.Writer(), `INSERT INTO named (code) VALUES ('kept')`)
	ship(t, p, r)
	if n := scan[int](t, r.Reader(), `SELECT count(*) FROM secrets`); n != 1 {
		t.Fatalf("setup: the replica holds %d secrets before the purge, want 1", n)
	}
	if !bytes.Contains(readAll(t, r.Path()), marker) && !bytes.Contains(readAll(t, r.Path()+"-wal"), marker) {
		t.Fatal("setup: the secret should be on the disk of the replica before the purge")
	}
	excluded := replicate(t, p, "secrets")
	// A small log goes in the copy as it is; with tables emptied, or a big log,
	// the copy keeps only the last row (the record may carry what was emptied).
	if lo, hi, _ := r.LogBounds(ctx); lo >= hi {
		t.Errorf("the log of a small copy is %d..%d, want it whole", lo, hi)
	}
	if lo, hi, _ := excluded.LogBounds(ctx); lo != hi {
		t.Errorf("the log of the excluded copy is %d..%d, want only the last row", lo, hi)
	}
	forced := filepath.Join(t.TempDir(), "forced.db")
	if _, err := p.Snapshot(ctx, forced, store.SnapshotOptions{KeepLogBytes: 1}); err != nil {
		t.Fatal(err)
	}
	if n := scanFile[int](t, forced, `SELECT count(*) FROM replication_log`); n != 1 {
		t.Errorf("a copy of a log over the limit keeps %d rows, want 1", n)
	}
	if n := scan[int](t, excluded.Reader(), `SELECT count(*) FROM secrets`); n != 0 {
		t.Errorf("the excluded replica has %d secrets", n)
	}
	if n := scan[int](t, excluded.Reader(), `SELECT count(*) FROM named WHERE code = 'kept'`); n != 1 {
		t.Errorf("only the vault tables are left out, named has %d rows", n)
	}
	if bytes.Contains(readAll(t, excluded.Path()), marker) {
		t.Error("the file of the excluded replica contains the secret")
	}
	if n := scan[int](t, excluded.Reader(), `SELECT count(*) FROM pragma_table_info('secrets')`); n == 0 {
		t.Error("the table is gone; the schema must stay for the migrations that follow")
	}

	if err := r.Purge(ctx, "secrets", "site_login_attempts"); err != nil {
		t.Fatal(err)
	}
	if n := scan[int](t, r.Reader(), `SELECT count(*) FROM secrets`); n != 0 {
		t.Errorf("%d secrets left after the purge", n)
	}
	if n := scan[int](t, r.Reader(), `SELECT count(*) FROM named WHERE code = 'kept'`); n != 1 {
		t.Errorf("the purge is only about the vault, named has %d rows", n)
	}
	for _, f := range []string{r.Path(), r.Path() + "-wal"} {
		if b, err := os.ReadFile(f); err == nil && bytes.Contains(b, marker) {
			t.Errorf("%s still contains the secret", filepath.Base(f))
		}
	}
	if lo, hi, _ := r.LogBounds(ctx); lo != hi {
		t.Errorf("log %d..%d: only the last row stays, empty", lo, hi)
	}
	exec(t, p.Writer(), `INSERT INTO named (code) VALUES ('after')`) // the sequence goes on
	ship(t, p, r)
	if n := scan[int](t, r.Reader(), `SELECT count(*) FROM named WHERE code = 'after'`); n != 1 {
		t.Error("the record after the purge did not apply")
	}
}

func TestAMigrationReachesTheReplicaAsARecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "p.db")
	small := store.MustLoad(fsOf(createParents), "migrations")
	p := open(t, path, small, store.Options{Capture: true})
	exec(t, p.Writer(), `INSERT INTO parents (id, name) VALUES (1, 'a')`)
	r := replicate(t, p)
	if v, _ := r.Version(ctx); v != 1 {
		t.Fatalf("replica version = %d", v)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	// The binary of the Citadel gets a new migration.
	p2 := open(t, path, logSet, store.Options{Capture: true})
	ship(t, p2, r)
	if v, _ := r.Version(ctx); v != 2 {
		t.Fatalf("replica version = %d after the record, want 2", v)
	}
	exec(t, p2.Writer(), `INSERT INTO named (code) VALUES ('after')`)
	ship(t, p2, r)
	if a, b := dump(t, p2, allTables...), dump(t, r, allTables...); a != b {
		t.Fatalf("the replica differs\n%s\n%s", a, b)
	}
}
