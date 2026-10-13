package guild_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/store/guild"
)

// CA-PERSIST-08
func TestEmbeddedMigrationsBuildTheGuildSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := guild.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if v, err := db.Version(ctx); err != nil || v != guild.Migrations.Latest() || v < 1 {
		t.Fatalf("Version = %d, %v; want %d", v, err, guild.Migrations.Latest())
	}
	var secrets int
	if err := db.Reader().QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'secrets'`).Scan(&secrets); err != nil || secrets != 1 {
		t.Errorf("secrets table: count %d, err %v", secrets, err)
	}
	rows, err := db.Reader().QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Error("foreign_key_check reports violations on an empty database")
	}
}

// CA-REPL-11, the schema half: the replication log works by primary key and a
// trigger would fire again on the replica.
func TestEveryGuildTableHasAPrimaryKeyAndThereAreNoTriggers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := guild.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var triggers int
	if err := db.Reader().QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'trigger'`).Scan(&triggers); err != nil || triggers != 0 {
		t.Errorf("triggers = %d, %v; want none", triggers, err)
	}
	rows, err := db.Reader().QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if len(tables) == 0 {
		t.Fatal("no tables")
	}
	for _, name := range tables {
		var pk int
		if err := db.Reader().QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info(?) WHERE pk > 0`, name).Scan(&pk); err != nil || pk == 0 {
			t.Errorf("table %s has no declared primary key (%d, %v)", name, pk, err)
		}
		// The preupdate hook does not see WITHOUT ROWID tables, and so the log
		// would miss their changes; generated columns are not in the row it
		// hands over.
		var rowid, generated int
		if err := db.Reader().QueryRowContext(ctx, `SELECT wr FROM pragma_table_list WHERE name = ? AND schema = 'main'`, name).Scan(&rowid); err != nil || rowid != 0 {
			t.Errorf("table %s is WITHOUT ROWID (%d, %v): the change log cannot follow it", name, rowid, err)
		}
		if err := db.Reader().QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_xinfo(?) WHERE hidden <> 0`, name).Scan(&generated); err != nil || generated != 0 {
			t.Errorf("table %s has generated columns (%d, %v)", name, generated, err)
		}
	}
	for _, want := range []string{"replication_log", "schema_migrations"} {
		var n int
		if err := db.Reader().QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, want).Scan(&n); err != nil || n != 1 {
			t.Errorf("the table %s is missing (%d, %v)", want, n, err)
		}
	}
}

// The Tower's copy is opened through the same wrapper without migrating (the
// rule itself is CA-PERSIST-06 in package store).
func TestReplicaOfTheGuildDatabaseIsNotMigrated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := guild.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Replica: true, Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if v, err := db.Version(ctx); err != nil || v != 0 {
		t.Errorf("Version = %d, %v; want 0 on a fresh replica", v, err)
	}
}
