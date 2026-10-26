// Package machine opens the machine-local database (maquina.db): what only
// this machine knows, never replicated (docs/arquitetura/persistencia.md).
package machine

import (
	"context"
	"embed"

	"github.com/mestre-ia/mestre/internal/store"
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

// GuildBound are the tables of maquina.db that hold what belongs to the Guild
// the machine was in: the messages waiting for its Citadel, the commands it
// ran for it, the addresses found for it, the rules and the characters it
// gave this machine. Leaving the Guild empties them
// (docs/guilda/guilda.md#saída-e-remoção); a new Guild must not receive the
// outbox of the old one.
var GuildBound = []string{
	"outbox", "processed_commands", "known_addresses",
	"agent_rules", "used_exceptions", "character_runtime", "character_births",
}

// Kept are the tables that stay when the machine leaves: machine_identity is
// erased by Forget itself; account_profiles and local_worktrees describe the
// provider logins in contas/ and the worktrees on disk, which stay; terminals
// is how the launcher finds the terminal processes still alive
// (docs/operacao/desinstalar.md). Every table is in one list or the other: a
// new migration has to decide.
var Kept = []string{"machine_identity", "account_profiles", "local_worktrees", "terminals"}
