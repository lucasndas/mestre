package store_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lucasndas/mestre/internal/store"
)

// The statement cache changes how often a text is prepared, nothing a caller
// sees: the same text runs again with other arguments, a query whose rows are
// still open does not share its statement with a second use of the same text,
// a failed use leaves the statement fit for the next, and the replication log
// keeps recording through a database opened with the cache.
func TestTheStatementCacheRunsTheSameTextAgainAndNeverSharesARunningStatement(t *testing.T) {
	t.Parallel()
	set := load(t, createItems)
	for name, opts := range map[string]store.Options{
		"plain":   {StmtCache: 8},
		"capture": {StmtCache: 8, Capture: true},
		"full":    {StmtCache: 1}, // the cache is full after one text: the rest takes the plain path
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			db := open(t, filepath.Join(t.TempDir(), "c.db"), set, opts)
			for i := 1; i <= 5; i++ {
				if _, err := db.Writer().ExecContext(ctx, `INSERT INTO items (id, name) VALUES (?, ?)`, i, "n"); err != nil {
					t.Fatalf("insert %d: %v", i, err)
				}
			}
			// A bad use first: too few arguments.
			if _, err := db.Writer().ExecContext(ctx, `INSERT INTO items (id, name) VALUES (?, ?)`, 9); err == nil {
				t.Fatal("an insert with a missing argument succeeded")
			}
			if _, err := db.Writer().ExecContext(ctx, `INSERT INTO items (id, name) VALUES (?, ?)`, 1, "dup"); err == nil {
				t.Fatal("an insert with a repeated key succeeded")
			}
			if _, err := db.Writer().ExecContext(ctx, `INSERT INTO items (id, name) VALUES (?, ?)`, 6, "n"); err != nil {
				t.Fatalf("the insert after the failures: %v", err)
			}

			const q = `SELECT id FROM items WHERE id > ? ORDER BY id`
			first, err := db.Reader().QueryContext(ctx, q, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer first.Close()
			if !first.Next() {
				t.Fatal("no first row")
			}
			// The same text while the first rows are open.
			second, err := db.Reader().QueryContext(ctx, q, 3)
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			for second.Next() {
				n++
			}
			if err := second.Close(); err != nil || n != 3 {
				t.Fatalf("the second query read %d rows (err %v), want 3", n, err)
			}
			rest := 0
			for first.Next() {
				rest++
			}
			if rest != 5 {
				t.Fatalf("the first query read %d more rows, want 5", rest)
			}
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			// And again after both closed: the statement is back.
			var c int
			if err := db.Reader().QueryRowContext(ctx, `SELECT count(*) FROM items WHERE id > ?`, 4).Scan(&c); err != nil || c != 2 {
				t.Fatalf("count = %d, %v", c, err)
			}
			if !opts.Capture {
				// A schema change after cached uses: the statements prepare again, and
				// closing the database finalizes them, so the WAL is checkpointed away.
				if _, err := db.Writer().ExecContext(ctx, `ALTER TABLE items ADD COLUMN extra TEXT`); err != nil {
					t.Fatal(err)
				}
				if err := db.Reader().QueryRowContext(ctx, `SELECT count(*) FROM items WHERE id > ?`, 4).Scan(&c); err != nil || c != 2 {
					t.Fatalf("count after the ALTER = %d, %v", c, err)
				}
				if _, err := db.Writer().ExecContext(ctx, `INSERT INTO items (id, name, extra) VALUES (?, ?, ?)`, 7, "n", "x"); err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(db.Path() + "-wal"); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("the WAL is left after Close: %v", err)
				}
			}
			if opts.Capture {
				if lsn, err := db.LastLSN(ctx); err != nil || lsn == 0 {
					t.Fatalf("the log has LSN %d, %v after the writes", lsn, err)
				}
			}
		})
	}
}
