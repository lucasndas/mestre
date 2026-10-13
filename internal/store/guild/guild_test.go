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
