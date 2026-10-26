package store_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/store"
)

// CA-REPL-11
// A statement that fails halfway through a query or a prepared statement still
// poisons its transaction: the hook reported the rows before SQLite took them
// back, and the log must not carry them (CA-REPL-11).
func TestAStatementThatFailsOnAQueryOrAPreparedPathPoisonsItsTransaction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const dup = `INSERT INTO named (code) VALUES ('a'), ('b'), ('a')`
	cases := map[string]func(tx *sql.Tx) error{
		"RETURNING scanned to the end": func(tx *sql.Tx) error {
			rows, err := tx.QueryContext(ctx, dup+` RETURNING code`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
			}
			return rows.Err()
		},
		"RETURNING read by QueryRow": func(tx *sql.Tx) error {
			var code string
			return tx.QueryRowContext(ctx, dup+` RETURNING code`).Scan(&code)
		},
		"a prepared statement": func(tx *sql.Tx) error {
			st, err := tx.PrepareContext(ctx, dup)
			if err != nil {
				return err
			}
			defer st.Close()
			_, err = st.ExecContext(ctx)
			return err
		},
		"a prepared query": func(tx *sql.Tx) error {
			st, err := tx.PrepareContext(ctx, dup+` RETURNING code`)
			if err != nil {
				return err
			}
			defer st.Close()
			rows, err := st.QueryContext(ctx)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
			}
			return rows.Err()
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			db := openPrimary(t, nil)
			base := lastLSN(t, db)
			tx, err := db.Writer().BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := run(tx); err == nil {
				t.Fatal("the repeated key was accepted")
			}
			if err := tx.Commit(); err == nil {
				t.Fatal("the transaction committed with rows the log cannot tell apart from phantoms")
			}
			if got := lastLSN(t, db); got != base {
				t.Errorf("lsn = %d, want %d: the failed statement left a record", got, base)
			}
			if n := scan[int](t, db.Reader(), `SELECT count(*) FROM named`); n != 0 {
				t.Errorf("%d rows leaked in", n)
			}
			// The connection is clean for the next transaction.
			exec(t, db.Writer(), `INSERT INTO named (code) VALUES ('ok')`)
			if got := lastLSN(t, db); got != base+1 {
				t.Errorf("lsn = %d after a good write, want %d", got, base+1)
			}
		})
	}
}

// CA-REPL-11
// The refusal of the commit hook reaches the caller as ErrUnloggedWrite, on
// every path that can hit it (CA-REPL-11).
func TestErrUnloggedWriteReachesTheCaller(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openPrimary(t, nil)
	base := lastLSN(t, db)

	st, err := db.Writer().PrepareContext(ctx, `INSERT INTO parents (id, name) VALUES (100, 'sneaky')`)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.ExecContext(ctx); !errors.Is(err, store.ErrUnloggedWrite) {
		t.Errorf("a prepared write outside a transaction = %v, want ErrUnloggedWrite", err)
	}
	var id int
	if err := db.Writer().QueryRowContext(ctx, `INSERT INTO parents (id, name) VALUES (101, 'sneaky') RETURNING id`).Scan(&id); !errors.Is(err, store.ErrUnloggedWrite) {
		t.Errorf("RETURNING outside a transaction = %v, want ErrUnloggedWrite", err)
	}
	pq, err := db.Writer().PrepareContext(ctx, `INSERT INTO parents (id, name) VALUES (102, 'sneaky') RETURNING id`)
	if err != nil {
		t.Fatal(err)
	}
	defer pq.Close()
	if err := pq.QueryRowContext(ctx).Scan(&id); !errors.Is(err, store.ErrUnloggedWrite) {
		t.Errorf("a prepared RETURNING outside a transaction = %v, want ErrUnloggedWrite", err)
	}
	// A refusal does not stick to the next, unrelated error.
	_, err = db.Writer().ExecContext(ctx, `INSERT INTO nowhere (x) VALUES (1)`)
	if err == nil || errors.Is(err, store.ErrUnloggedWrite) {
		t.Errorf("an unrelated error = %v, must not be ErrUnloggedWrite", err)
	}
	if got := lastLSN(t, db); got != base {
		t.Errorf("lsn = %d, want %d", got, base)
	}
	if n := scan[int](t, db.Reader(), `SELECT count(*) FROM parents`); n != 0 {
		t.Errorf("%d parents leaked in", n)
	}
}

// CA-REPL-11
// What the guards must not let through, whatever the text hides behind
// (CA-REPL-11, CA-REPL-05).
func TestTheGuardsOfTheWriterCannotBeWalkedAround(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openPrimary(t, nil)
	base := lastLSN(t, db)

	t.Run("a schema change behind something else", func(t *testing.T) {
		t.Parallel()
		for _, q := range []string{
			`;CREATE TABLE loose (id INTEGER PRIMARY KEY)`,
			`SELECT 1; CREATE TABLE loose (id INTEGER PRIMARY KEY)`,
			`INSERT INTO parents (id, name) VALUES (1, 'a'); DROP TABLE children`,
			`/* x */ ; -- y
			ALTER TABLE parents ADD COLUMN extra TEXT`,
			`SELECT 'a;b'; CREATE TABLE loose (id INTEGER PRIMARY KEY)`,
		} {
			_, err := db.Writer().ExecContext(ctx, q)
			if !errors.Is(err, store.ErrSchemaOutsideMigration) {
				t.Errorf("%q = %v, want ErrSchemaOutsideMigration", q, err)
			}
		}
	})
	t.Run("a script of several statements", func(t *testing.T) {
		t.Parallel()
		_, err := db.Writer().ExecContext(ctx, `INSERT INTO parents (id, name) VALUES (7, 'a'); INSERT INTO parents (id, name) VALUES (8, 'b')`)
		if !errors.Is(err, store.ErrMultiStatement) {
			t.Errorf("a two-statement script = %v, want ErrMultiStatement", err)
		}
		// A semicolon in a string or at the end is one statement.
		if _, err := db.Writer().ExecContext(ctx, `SELECT 'x;y';  -- done`); err != nil {
			t.Errorf("one statement with a ';' inside = %v", err)
		}
	})
	t.Run("ATTACH and the pragmas that rewrite the schema", func(t *testing.T) {
		t.Parallel()
		for _, q := range []string{
			`ATTACH DATABASE ':memory:' AS other`,
			`attach ':memory:' as other`,
			`DETACH DATABASE other`,
			`PRAGMA writable_schema = ON`,
			`PRAGMA main.writable_schema(1)`,
			`PRAGMA schema_version = 1000`,
		} {
			if _, err := db.Writer().ExecContext(ctx, q); !errors.Is(err, store.ErrUnsupportedStatement) {
				t.Errorf("%q = %v, want ErrUnsupportedStatement", q, err)
			}
		}
	})
	t.Run("a schema change that the text does not show", func(t *testing.T) {
		t.Parallel()
		// ANALYZE creates sqlite_stat1: the schema moved without a migration.
		tx, err := db.Writer().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `ANALYZE`); err != nil {
			_ = tx.Rollback()
			t.Skipf("ANALYZE does not run here: %v", err)
		}
		if err := tx.Commit(); !errors.Is(err, store.ErrSchemaOutsideMigration) {
			t.Errorf("Commit after a schema change = %v, want ErrSchemaOutsideMigration", err)
		}
	})
	t.Run("a direct write to the log", func(t *testing.T) {
		t.Parallel()
		for _, q := range []string{
			`INSERT INTO replication_log (lsn, at, body) VALUES (99999, 0, x'01')`,
			`UPDATE replication_log SET body = x'01'`,
			`DELETE FROM replication_log`,
		} {
			if _, err := db.Writer().ExecContext(ctx, q); !errors.Is(err, store.ErrLogTableWrite) {
				t.Errorf("%q = %v, want ErrLogTableWrite", q, err)
			}
		}
		tx, err := db.Writer().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO replication_log (lsn, at, body) VALUES (99999, 0, x'01')`); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); !errors.Is(err, store.ErrLogTableWrite) {
			t.Errorf("Commit after a write to the log = %v, want ErrLogTableWrite", err)
		}
	})
	t.Cleanup(func() {
		if got := lastLSN(t, db); got != base {
			t.Errorf("lsn = %d, want %d: none of those may have left a record", got, base)
		}
		if _, hi, _ := db.LogBounds(ctx); hi != base {
			t.Errorf("the log grew to %d", hi)
		}
		// The retention is still the store's own write.
		if _, err := db.PruneLog(ctx, time.Hour, 1<<30); err != nil {
			t.Errorf("PruneLog = %v", err)
		}
		exec(t, db.Writer(), `INSERT INTO named (code) VALUES ('fine')`)
		if got := lastLSN(t, db); got != base+1 {
			t.Errorf("lsn = %d after a good write, want %d", got, base+1)
		}
	})
}

// CA-REPL-05
// The readers cannot write, however they are told to, and neither can the
// writer of a replica outside the replication (CA-REPL-05).
func TestNoPragmaLiftsTheGuardOfAReaderOrAReplica(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p := openPrimary(t, nil)
	r := replicate(t, p)

	for name, db := range map[string]*store.DB{"primary": p, "replica": r} {
		t.Run("reader of the "+name, func(t *testing.T) {
			t.Parallel()
			conn, err := db.Reader().Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_, _ = conn.ExecContext(ctx, `PRAGMA query_only = 0`)
			if _, err := conn.ExecContext(ctx, `INSERT INTO named (code) VALUES ('sneaky')`); err == nil {
				t.Error("a reader wrote after PRAGMA query_only = 0")
			}
			_, _ = conn.ExecContext(ctx, `PRAGMA writable_schema = ON`)
			if _, err := conn.ExecContext(ctx, `CREATE TABLE loose (id INTEGER PRIMARY KEY)`); err == nil {
				t.Error("a reader changed the schema")
			}
		})
	}
	t.Run("writer of the replica", func(t *testing.T) {
		t.Parallel()
		for _, q := range []string{
			`PRAGMA query_only = 0`,
			`PRAGMA query_only(0)`,
			`pragma main.query_only=OFF`,
			`PRAGMA writable_schema = ON`,
			`PRAGMA foreign_keys = OFF`,
			`ATTACH DATABASE ':memory:' AS other`,
			`SELECT 1; PRAGMA query_only = 0`,
			`;PRAGMA query_only = 0`,
			`INSERT INTO named (code) VALUES ('sneaky')`,
			`VACUUM`,
			`EXPLAIN PRAGMA query_only = 0`,
			`explain query plan pragma query_only=0`,
			`EXPLAIN QUERY PLAN INSERT INTO named (code) VALUES ('sneaky')`,
			`EXPLAIN EXPLAIN PRAGMA query_only = 0`,
			`EXPLAIN`,
		} {
			if _, err := r.Writer().ExecContext(ctx, q); !errors.Is(err, store.ErrReplicaWrite) {
				t.Errorf("%q = %v, want ErrReplicaWrite", q, err)
			}
		}
		if _, err := r.Writer().PrepareContext(ctx, `PRAGMA query_only = 0`); !errors.Is(err, store.ErrReplicaWrite) {
			t.Errorf("a prepared PRAGMA = %v", err)
		}
		// A write behind WITH gets past the guard of the statement and meets
		// query_only; and what EXPLAIN reads still reads.
		if _, err := r.Writer().ExecContext(ctx, `WITH x AS (SELECT 'sneaky') INSERT INTO named (code) SELECT * FROM x`); err == nil {
			t.Error("a WITH ... INSERT wrote on the replica")
		}
		if n := scan[int](t, r.Reader(), `SELECT count(*) FROM named WHERE code = 'sneaky'`); n != 0 {
			t.Errorf("%d rows were written behind the guard", n)
		}
		if _, err := r.Writer().ExecContext(ctx, `EXPLAIN QUERY PLAN SELECT * FROM named`); err != nil {
			t.Errorf("EXPLAIN QUERY PLAN SELECT = %v", err)
		}
		if got := scan[int](t, r.Writer(), `PRAGMA query_only`); got != 1 {
			t.Errorf("query_only = %d after the refused statements", got)
		}
		// Reads still work, and the guard is on.
		if got := scan[int](t, r.Writer(), `PRAGMA query_only`); got != 1 {
			t.Errorf("query_only = %d", got)
		}
		if _, err := r.Writer().ExecContext(ctx, `INSERT INTO named (code) VALUES ('after')`); err == nil {
			t.Error("the replica took a write")
		}
		// And the replication itself is not locked out by it.
		exec(t, p.Writer(), `INSERT INTO named (code) VALUES ('shipped')`)
		ship(t, p, r)
		if n := scan[int](t, r.Reader(), `SELECT count(*) FROM named WHERE code = 'shipped'`); n != 1 {
			t.Error("the replication could not write after the refusals")
		}
	})
}

// CA-REPL-04
// A purge reports it when a reader keeps the WAL from being truncated, instead
// of leaving the secrets in the -wal file and saying nothing.
func TestPurgeSaysSoWhenAReaderKeepsTheWAL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p := openPrimary(t, nil)
	marker := bytes.Repeat([]byte("TOP-SECRET-CIPHERTEXT-"), 20)
	exec(t, p.Writer(), `INSERT INTO secrets (name, value) VALUES ('token', ?)`, marker)

	tmp := filepath.Join(t.TempDir(), "snap.db")
	lsn, err := p.Snapshot(ctx, tmp, store.SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r := open(t, filepath.Join(t.TempDir(), "r.db"), logSet, store.Options{Replica: true, BusyTimeout: 20 * time.Millisecond})
	if err := r.Replace(ctx, tmp, lsn); err != nil {
		t.Fatal(err)
	}
	if n := scan[int](t, r.Reader(), `SELECT count(*) FROM secrets`); n != 1 {
		t.Fatalf("setup: the replica has %d secrets", n)
	}

	// A reader that holds a snapshot from before the purge.
	rd, err := r.Reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := rd.QueryRowContext(ctx, `SELECT count(*) FROM secrets`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if err := r.Purge(ctx, "secrets"); !errors.Is(err, store.ErrPurgeIncomplete) {
		t.Fatalf("Purge with a reader on the WAL = %v, want ErrPurgeIncomplete", err)
	}
	if err := rd.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := r.Purge(ctx, "secrets"); err != nil {
		t.Fatalf("Purge once the reader is gone: %v", err)
	}
	for _, f := range []string{r.Path(), r.Path() + "-wal"} {
		if b, err := os.ReadFile(f); err == nil && bytes.Contains(b, marker) {
			t.Errorf("%s still contains the secret", filepath.Base(f))
		}
	}
}

// A forged record cannot make the decoder reserve many times its size.
func TestDecodeChangesDoesNotReserveWhatTheRecordClaims(t *testing.T) { //nolint:paralleltest // it counts the allocations of the whole process
	claim := func(n uint64) []byte { return binary.AppendUvarint(nil, n) }
	const size = 1 << 20
	changes := append([]byte{1}, claim(size)...)
	changes = append(changes, bytes.Repeat([]byte{0}, size)...) // 1M changes claimed, the first is not one

	for name, body := range map[string][]byte{"changes": changes} {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := store.DecodeChanges(body)
		runtime.ReadMemStats(&after)
		if !errors.Is(err, store.ErrBadLog) {
			t.Errorf("%s: DecodeChanges = %v, want ErrBadLog", name, err)
		}
		if got := after.TotalAlloc - before.TotalAlloc; got > 4*size {
			t.Errorf("%s: decoding a %d-byte record allocated %d bytes", name, len(body), got)
		}
	}
}
