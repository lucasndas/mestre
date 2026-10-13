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

func openPrimary(t *testing.T, clk clock.Clock) *store.DB {
	t.Helper()
	opts := store.Options{Capture: true, Clock: clk}
	return open(t, filepath.Join(t.TempDir(), "p.db"), logSet, opts)
}

func exec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// log returns the decoded records of the change log.
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

// afterMigrations is the lsn of the last migration record: the tests look at
// what comes after it.
func afterMigrations(t *testing.T, db *store.DB) uint64 {
	t.Helper()
	n, err := db.LastLSN(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTheMigrationsAreTheFirstRecordsOfTheLog(t *testing.T) {
	t.Parallel()
	db := openPrimary(t, clock.NewFake(time.UnixMilli(1_700_000_000_000)))
	rows, recs := readLog(t, db)
	if len(rows) != 2 {
		t.Fatalf("log has %d records, want one per migration", len(rows))
	}
	for i, cs := range recs {
		if len(cs) != 2 || cs[0].Op != store.OpSQL || cs[1].Op != store.OpInsert || cs[1].Table != "schema_migrations" {
			t.Fatalf("record %d = %+v, want the SQL and the schema_migrations row", rows[i].LSN, cs)
		}
	}
	if !strings.Contains(recs[0][0].SQL, "CREATE TABLE parents") {
		t.Errorf("first migration SQL = %q", recs[0][0].SQL)
	}
	if rows[0].LSN != 1 || rows[1].LSN != 2 || rows[0].At != 1_700_000_000_000 {
		t.Errorf("lsn/at = %d/%d, %d", rows[0].LSN, rows[0].At, rows[1].LSN)
	}
}

// Every change of a committed transaction is in one record, in the order it
// happened, cascades included; a transaction that changes no row has no lsn.
func TestEachCommittedTransactionIsOneRecordInTheLog(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openPrimary(t, clock.NewFake(time.UnixMilli(5)))
	base := afterMigrations(t, db)

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
	rows, recs := readLog(t, db)
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
	if got := afterMigrations(t, db); got != base+1 {
		t.Errorf("lsn = %d, want %d: no row changed", got, base+1)
	}
}

func TestAWriteOutsideATransactionIsLoggedToo(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openPrimary(t, nil)
	base := afterMigrations(t, db)
	exec(t, db.Writer(), `INSERT INTO parents (id, name) VALUES (1, 'a')`)
	exec(t, db.Writer(), `UPDATE parents SET name = 'b'`)
	res, err := db.Writer().ExecContext(ctx, `INSERT INTO parents (name) VALUES (?)`, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := res.LastInsertId(); id != 2 {
		t.Errorf("LastInsertId = %d, want 2", id)
	}
	if got := afterMigrations(t, db); got != base+3 {
		t.Errorf("lsn = %d, want %d", got, base+3)
	}
	// The row of the auto-assigned key is logged with the key that was chosen.
	_, recs := readLog(t, db)
	last := recs[len(recs)-1][0]
	if !reflect.DeepEqual(last.New, []any{int64(2), "auto", nil}) {
		t.Errorf("last insert = %+v", last)
	}
}

func TestEveryKindOfValueSurvivesTheLog(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openPrimary(t, nil)
	exec(t, db.Writer(), `INSERT INTO named (code, num, raw, big) VALUES (?, ?, ?, ?)`, "k", 1.5, []byte{0, 1, 2}, int64(1)<<62)
	exec(t, db.Writer(), `INSERT INTO named (code, num, raw, big) VALUES (?, NULL, ?, NULL)`, "empty", []byte{})
	exec(t, db.Writer(), `INSERT INTO seq (body) VALUES ('one'), ('two')`)
	_, recs := readLog(t, db)
	n := len(recs)
	if got := recs[n-3][0].New; !reflect.DeepEqual(got, []any{"k", 1.5, []byte{0, 1, 2}, int64(1) << 62}) {
		t.Errorf("named k = %#v", got)
	}
	if got := recs[n-2][0].New; !reflect.DeepEqual(got, []any{"empty", nil, []byte{}, nil}) {
		t.Errorf("named empty = %#v (an empty blob is not NULL)", got)
	}
	if got := recs[n-1][1].New; !reflect.DeepEqual(got, []any{int64(2), "two"}) {
		t.Errorf("seq 2 = %#v", got)
	}
	_ = ctx
}

func TestUpsertAndReplaceAreLoggedAsTheyChangeRows(t *testing.T) {
	t.Parallel()
	db := openPrimary(t, nil)
	exec(t, db.Writer(), `INSERT INTO secrets (name, value) VALUES ('a', x'01')`)
	exec(t, db.Writer(), `INSERT INTO secrets (name, value) VALUES ('a', x'02') ON CONFLICT (name) DO UPDATE SET value = excluded.value`)
	exec(t, db.Writer(), `INSERT OR REPLACE INTO secrets (name, value) VALUES ('a', x'03')`)
	_, recs := readLog(t, db)
	n := len(recs)
	if c := recs[n-2][0]; c.Op != store.OpUpdate {
		t.Errorf("upsert logged as %s", c.Op)
	}
	var ops []string
	for _, c := range recs[n-1] {
		ops = append(ops, c.Op.String())
	}
	if !reflect.DeepEqual(ops, []string{"delete", "insert"}) {
		t.Errorf("replace logged as %v, want delete and insert", ops)
	}
}

// CA-REPL-11: a write that does not go through the log is refused.
func TestAWriteThatSkipsTheLogIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openPrimary(t, nil)
	base := afterMigrations(t, db)

	t.Run("a prepared statement runs outside the wrapper", func(t *testing.T) {
		t.Parallel()
		st, err := db.Writer().PrepareContext(ctx, `INSERT INTO parents (id, name) VALUES (100, 'sneaky')`)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if _, err := st.ExecContext(ctx); err == nil || !strings.Contains(err.Error(), "constraint") {
			t.Fatalf("Exec = %v, want the commit refused", err)
		}
	})
	t.Run("a write that comes back in a query", func(t *testing.T) {
		t.Parallel()
		var id int
		err := db.Writer().QueryRowContext(ctx, `INSERT INTO parents (id, name) VALUES (101, 'sneaky') RETURNING id`).Scan(&id)
		if err == nil {
			t.Fatal("RETURNING write went through the writer without a log")
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
		if got := afterMigrations(t, db); got != base {
			t.Errorf("lsn = %d, want %d: none of those writes may have left a record", got, base)
		}
		if n := scan[int](t, db.Reader(), `SELECT count(*) FROM parents`); n != 0 {
			t.Errorf("%d parents leaked in", n)
		}
	})
}

// A database that does not capture (the machine one) has no log and no
// restrictions.
func TestADatabaseThatDoesNotCaptureHasNoLog(t *testing.T) {
	t.Parallel()
	db := open(t, filepath.Join(t.TempDir(), "m.db"), logSet, store.Options{})
	exec(t, db.Writer(), `INSERT INTO parents (id, name) VALUES (1, 'a')`)
	if _, err := db.LastLSN(context.Background()); err == nil {
		t.Error("LastLSN works on a database without a log")
	}
	exec(t, db.Writer(), `CREATE TABLE free (id INTEGER PRIMARY KEY)`)
}

func TestLogWatchWakesAtTheNextCommit(t *testing.T) {
	t.Parallel()
	db := openPrimary(t, nil)
	w := db.LogWatch()
	select {
	case <-w:
		t.Fatal("woke before a commit")
	default:
	}
	exec(t, db.Writer(), `INSERT INTO parents (id, name) VALUES (1, 'a')`)
	select {
	case <-w:
	default:
		t.Fatal("a commit did not wake the watcher")
	}
}

func TestPruneLogKeepsTheNewestRowAndTheRetention(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fake := clock.NewFake(time.UnixMilli(1_000_000))
	db := openPrimary(t, fake)
	for i := range 6 {
		fake.Advance(20 * time.Minute)
		exec(t, db.Writer(), `INSERT INTO named (code, raw) VALUES (?, ?)`, fmt.Sprint(i), bytes.Repeat([]byte{1}, 1000))
	}
	// 6 inserts at 20, 40... 120 minutes; "now" is 120 minutes: older than 1 h goes.
	n, err := db.PruneLog(ctx, time.Hour, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	lo, hi, _ := db.LogBounds(ctx)
	if hi != 8 || lo != 5 || n != 4 {
		t.Errorf("after the age: removed %d, bounds %d..%d; want 4 removed, 5..8", n, lo, hi)
	}
	// By size: the bodies are about 1 KB each, keep room for two.
	if _, err := db.PruneLog(ctx, time.Hour, 2300); err != nil {
		t.Fatal(err)
	}
	lo, hi, _ = db.LogBounds(ctx)
	if hi != 8 || lo != 7 {
		t.Errorf("after the size: bounds %d..%d, want 7..8", lo, hi)
	}
	// Everything is older than the age and over the size: the last row stays.
	fake.Advance(24 * time.Hour)
	if _, err := db.PruneLog(ctx, time.Hour, 0); err != nil {
		t.Fatal(err)
	}
	lo, hi, _ = db.LogBounds(ctx)
	if lo != 8 || hi != 8 {
		t.Errorf("bounds %d..%d, want only 8: the next lsn comes from the newest row", lo, hi)
	}
	exec(t, db.Writer(), `INSERT INTO named (code) VALUES ('next')`)
	if _, hi, _ = db.LogBounds(ctx); hi != 9 {
		t.Errorf("next lsn = %d, want 9", hi)
	}
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

func TestARecordAppliedToAReplicaGivesTheSameRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p := openPrimary(t, clock.NewFake(time.UnixMilli(9)))
	exec(t, p.Writer(), `INSERT INTO parents (id, name) VALUES (1, 'a'), (2, 'b'), (3, 'c')`)
	r := replicate(t, p)

	tx, _ := p.Writer().BeginTx(ctx, nil)
	for _, q := range []string{
		`INSERT INTO children (parent_id, label) VALUES (1, 'x'), (1, 'y'), (2, 'z')`,
		`UPDATE parents SET id = 10, note = 'moved' WHERE id = 3`, // changes the key
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
	lp, _ := p.LastLSN(ctx)
	if lr, _ := r.LastLSN(ctx); lr != lp {
		t.Errorf("replica at %d, primary at %d", lr, lp)
	}
}

func TestAMigrationReachesTheReplicaAsARecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "p.db")
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

func TestARepeatIsSkippedAndAHoleStopsTheBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p := openPrimary(t, nil)
	r := replicate(t, p)
	for i := range 4 {
		exec(t, p.Writer(), `INSERT INTO named (code) VALUES (?)`, fmt.Sprint(i))
	}
	have, _ := r.LastLSN(ctx)
	rows, _ := p.LogSince(ctx, have, 100, 1<<30)
	if n, err := r.ApplyLog(ctx, rows[:2]); err != nil || n != 2 {
		t.Fatalf("first two = %d, %v", n, err)
	}
	if n, err := r.ApplyLog(ctx, rows[:2]); err != nil || n != 0 {
		t.Fatalf("a repeat = %d, %v; want skipped", n, err)
	}
	n, err := r.ApplyLog(ctx, []store.LogRow{rows[2], rows[3]}[1:])
	var gap *store.GapError
	if !errors.As(err, &gap) || n != 0 || gap.Have != have+2 || gap.Got != rows[3].LSN {
		t.Fatalf("hole = %d, %v; want a GapError from %d", n, err, have+2)
	}
	if got, _ := r.LastLSN(ctx); got != have+2 {
		t.Errorf("replica at %d, the hole must not move it", got)
	}
}

func TestARecordThatDoesNotFitMarksTheReplicaAsDiverged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p := openPrimary(t, nil)
	exec(t, p.Writer(), `INSERT INTO parents (id, name) VALUES (1, 'a')`)
	r := replicate(t, p)
	have, _ := r.LastLSN(ctx)
	var div *store.DivergedError
	if _, err := r.ApplyLog(ctx, []store.LogRow{{LSN: have + 1, At: 1, Body: store.EncodeChanges([]store.Change{{Op: store.OpDelete, Table: "parents", Old: []any{int64(77), "x", nil}}})}}); !errors.As(err, &div) {
		t.Fatalf("delete of a missing row = %v, want DivergedError", err)
	}
	if got, _ := r.LastLSN(ctx); got != have {
		t.Errorf("replica at %d: a failed batch must leave nothing", got)
	}
}

// CA-REPL-05: nothing writes to a replica but the replication.
func TestAReplicaRefusesEveryWriteButTheReplicationsOwn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p := openPrimary(t, nil)
	r := replicate(t, p)
	for _, q := range []string{
		`INSERT INTO parents (id, name) VALUES (1, 'a')`,
		`DELETE FROM schema_migrations`,
		`CREATE TABLE loose (id INTEGER PRIMARY KEY)`,
		`INSERT INTO replication_log (lsn, at, body) VALUES (99, 0, x'01')`,
	} {
		if _, err := r.Writer().ExecContext(ctx, q); err == nil {
			t.Errorf("%q worked on a replica", q)
		}
		if _, err := r.Reader().ExecContext(ctx, q); err == nil {
			t.Errorf("%q worked through the reader", q)
		}
	}
	tx, err := r.Writer().BeginTx(ctx, nil)
	if err == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO parents (id, name) VALUES (1, 'a')`)
		_ = tx.Rollback()
	}
	if err == nil {
		t.Error("a transaction wrote to a replica")
	}
	if !r.IsReplica() {
		t.Error("IsReplica = false")
	}
	// The replication writes, and the guard is back right after.
	exec(t, p.Writer(), `INSERT INTO parents (id, name) VALUES (1, 'a')`)
	ship(t, p, r)
	if n := scan[int](t, r.Reader(), `SELECT count(*) FROM parents`); n != 1 {
		t.Fatalf("parents on the replica = %d", n)
	}
	if _, err := r.Writer().ExecContext(ctx, `INSERT INTO parents (id, name) VALUES (2, 'b')`); err == nil {
		t.Error("the guard did not come back after an apply")
	}
	if got := scan[int](t, r.Writer(), `PRAGMA foreign_keys`); got != 1 {
		t.Errorf("foreign_keys = %d after an apply, want 1", got)
	}
	if _, err := p.ApplyLog(ctx, nil); !errors.Is(err, store.ErrNotReplica) {
		t.Errorf("ApplyLog on a primary = %v", err)
	}
}

func TestReplaceChecksTheCopyBeforeSwappingIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p := openPrimary(t, nil)
	exec(t, p.Writer(), `INSERT INTO parents (id, name) VALUES (1, 'a')`)
	r := replicate(t, p)
	before := dump(t, r, allTables...)

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
	if _, err := p.Snapshot(ctx, filepath.Join(t.TempDir(), "x.db"), store.SnapshotOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := p.Replace(ctx, good, lsn); !errors.Is(err, store.ErrNotReplica) {
		t.Errorf("Replace on a primary = %v", err)
	}
	if after := dump(t, r, allTables...); after != before {
		t.Error("a refused copy changed the replica")
	}
}

func TestReplaceRefusesASchemaNewerThanTheBinary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	big := openPrimary(t, nil) // two migrations
	snap := filepath.Join(t.TempDir(), "s.db")
	lsn, err := big.Snapshot(ctx, snap, store.SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	small := store.MustLoad(fsOf(createParents), "migrations")
	r := open(t, filepath.Join(t.TempDir(), "r.db"), small, store.Options{Replica: true})
	if err := r.Replace(ctx, snap, lsn); !errors.Is(err, store.ErrNewerSchema) {
		t.Fatalf("Replace = %v, want ErrNewerSchema", err)
	}
}

// A copy for a machine that was excluded has the schema of the vault tables
// and none of their rows, not even on disk.
func TestTheCopyOfAnExcludedMachineHasNoVaultBytes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p := openPrimary(t, nil)
	marker := bytes.Repeat([]byte("TOP-SECRET-CIPHERTEXT-"), 20)
	exec(t, p.Writer(), `INSERT INTO secrets (name, value) VALUES ('token', ?)`, marker)
	exec(t, p.Writer(), `INSERT INTO named (code) VALUES ('kept')`)

	r := replicate(t, p, "secrets")
	if n := scan[int](t, r.Reader(), `SELECT count(*) FROM secrets`); n != 0 {
		t.Errorf("the excluded replica has %d secrets", n)
	}
	if n := scan[int](t, r.Reader(), `SELECT count(*) FROM named`); n != 1 {
		t.Errorf("named = %d: only the vault tables are left out", n)
	}
	if bytes.Contains(readAll(t, r.Path()), marker) {
		t.Error("the file of the excluded replica contains the secret")
	}
	if n := scan[int](t, r.Reader(), `SELECT count(*) FROM pragma_table_info('secrets')`); n == 0 {
		t.Error("the table is gone; the schema must stay for the migrations that follow")
	}
	_ = ctx
}

func TestPurgeLeavesNoVaultBytesBehind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p := openPrimary(t, nil)
	marker := bytes.Repeat([]byte("TOP-SECRET-CIPHERTEXT-"), 20)
	exec(t, p.Writer(), `INSERT INTO secrets (name, value) VALUES ('token', ?)`, marker)
	r := replicate(t, p)
	exec(t, p.Writer(), `INSERT INTO secrets (name, value) VALUES ('later', ?)`, marker)
	exec(t, p.Writer(), `INSERT INTO named (code) VALUES ('kept')`)
	ship(t, p, r)
	if !bytes.Contains(readAll(t, r.Path()), marker) && scan[int](t, r.Reader(), `SELECT count(*) FROM secrets`) != 2 {
		t.Fatal("setup: the replica should hold the secrets before the purge")
	}

	if err := r.Purge(ctx, "secrets", "site_login_attempts"); err != nil {
		t.Fatal(err)
	}
	if n := scan[int](t, r.Reader(), `SELECT count(*) FROM secrets`); n != 0 {
		t.Errorf("%d secrets left", n)
	}
	if n := scan[int](t, r.Reader(), `SELECT count(*) FROM named`); n != 1 {
		t.Errorf("named = %d: the purge is only about the vault", n)
	}
	for _, f := range []string{r.Path(), r.Path() + "-wal"} {
		if b, err := os.ReadFile(f); err == nil && bytes.Contains(b, marker) {
			t.Errorf("%s still contains the secret", filepath.Base(f))
		}
	}
	lo, hi, _ := r.LogBounds(ctx)
	if lo != hi {
		t.Errorf("log %d..%d: only the last row stays, empty", lo, hi)
	}
	// The sequence goes on: the next record applies.
	exec(t, p.Writer(), `INSERT INTO named (code) VALUES ('after')`)
	ship(t, p, r)
	if n := scan[int](t, r.Reader(), `SELECT count(*) FROM named`); n != 2 {
		t.Errorf("named = %d after the purge, want 2", n)
	}
}

func readAll(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
