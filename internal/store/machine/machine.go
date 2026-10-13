// Package machine opens the machine-local database (maquina.db): what only
// this machine knows, never replicated (docs/arquitetura/persistencia.md).
package machine

import (
	"context"
	"embed"

	"github.com/lucasndas/mestre/internal/store"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrations is the machine database schema, one numbered file per version.
var Migrations = store.MustLoad(migrationFiles, "migrations")

// Open opens the machine database at path and migrates it to the latest
// version. It is never a replica, so opts.Replica is ignored.
func Open(ctx context.Context, path string, opts store.Options) (*store.DB, error) {
	opts.Replica = false
	return store.Open(ctx, path, Migrations, opts)
}
