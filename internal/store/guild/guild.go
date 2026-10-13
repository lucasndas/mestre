// Package guild opens the Guild database (mestre.db): the main database on
// the Citadel, a read-only replica on a Tower
// (docs/arquitetura/persistencia.md).
package guild

import (
	"context"
	"embed"

	"github.com/lucasndas/mestre/internal/store"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrations is the Guild database schema, one numbered file per version.
var Migrations = store.MustLoad(migrationFiles, "migrations")

// Open opens the Guild database at path. On the Citadel it migrates to the
// latest version; with opts.Replica (a Tower) the schema comes from the
// replication and nothing is applied locally.
func Open(ctx context.Context, path string, opts store.Options) (*store.DB, error) {
	return store.Open(ctx, path, Migrations, opts)
}
