// Package guild opens the Guild database (mestre.db): the main database on
// the Citadel, a read-only replica on a Tower
// (docs/arquitetura/persistencia.md).
package guild

import (
	"context"
	"embed"

	"github.com/mestre-ia/mestre/internal/store"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrations is the Guild database schema, one numbered file per version.
var Migrations = store.MustLoad(migrationFiles, "migrations")

// Open opens the Guild database at path. On the Citadel it migrates to the
// latest version and records every change in replication_log, in the same
// transaction (docs/guilda/replicacao.md); with opts.Replica (a Tower) the
// schema and the rows come from the replication, and nothing is applied or
// written locally.
func Open(ctx context.Context, path string, opts store.Options) (*store.DB, error) {
	opts.Capture = !opts.Replica
	return store.Open(ctx, path, Migrations, opts)
}
