package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lucasndas/mestre/internal/store"
)

// A statement that writes without being DML (ANALYZE creates sqlite_stat1,
// which no replica has) does not run in SQLite's own autocommit, where
// nothing would compare the schema version: it goes through the logged path,
// and its commit is refused.
func TestAStatementThatChangesTheSchemaWithoutBeingDMLIsRefusedOnThePrimary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p := openPrimary(t, nil)
	exec(t, p.Writer(), `INSERT INTO named (code) VALUES ('a')`)
	before := scan[int](t, p.Reader(), `PRAGMA schema_version`)
	for _, q := range []string{`ANALYZE`, `analyze main`, `PRAGMA optimize`, `REINDEX`} {
		if _, err := p.Writer().ExecContext(ctx, q); err != nil && !errors.Is(err, store.ErrSchemaOutsideMigration) {
			t.Errorf("%q = %v", q, err)
		}
		if got := scan[int](t, p.Reader(), `PRAGMA schema_version`); got != before {
			t.Fatalf("after %q the schema version went from %d to %d, outside a migration", q, before, got)
		}
		if n := scan[int](t, p.Reader(), `SELECT count(*) FROM sqlite_master WHERE name = 'sqlite_stat1'`); n != 0 {
			t.Fatalf("after %q the primary has a table its replicas lack", q)
		}
	}
	// the same statements inside a transaction were already refused at commit
	tx, err := p.Writer().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `ANALYZE`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); !errors.Is(err, store.ErrSchemaOutsideMigration) {
		t.Errorf("ANALYZE in a transaction committed with %v, want ErrSchemaOutsideMigration", err)
	}
}
