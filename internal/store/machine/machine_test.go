package machine_test

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/store/machine"
)

// CA-PERSIST-08
func TestEmbeddedMigrationsBuildTheMachineSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// Replica is meaningless here: the machine database always migrates.
	db, err := machine.Open(ctx, filepath.Join(t.TempDir(), "maquina.db"), store.Options{Replica: true, Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if v, err := db.Version(ctx); err != nil || v != machine.Migrations.Latest() || v < 1 {
		t.Fatalf("Version = %d, %v; want %d", v, err, machine.Migrations.Latest())
	}
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO terminals (id, character_id, machine_id, dir, status,
		socket_path, handshake_secret, pty_version, started_at, last_seen_at)
		VALUES ('t1', 'c1', 'm1', '/work', 'starting', '/run/t1.sock', x'00', '1', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	// The PID and its start identity are written together, never one alone.
	if _, err := db.Writer().ExecContext(ctx, `UPDATE terminals SET pty_pid = 42 WHERE id = 't1'`); err == nil {
		t.Error("pty_pid without pty_started was accepted")
	}
	if _, err := db.Writer().ExecContext(ctx, `UPDATE terminals SET pty_pid = 42, pty_started = 'boot:123' WHERE id = 't1'`); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := db.Reader().QueryRowContext(ctx, `SELECT status FROM terminals WHERE id = 't1'`).Scan(&status); err != nil || status != "starting" {
		t.Errorf("terminal status = %q, err %v", status, err)
	}
}

// CA-GUILDA-16
// Every table of the machine database is classified: a migration that adds one
// has to say whether leaving the Guild empties it (CA-GUILDA-16).
func TestEveryMachineTableIsEitherEmptiedWhenLeavingTheGuildOrKept(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := machine.Open(ctx, filepath.Join(t.TempDir(), "maquina.db"), store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Reader().QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE '%schema_migrations%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	known := map[string]bool{}
	for _, n := range append(slices.Clone(machine.GuildBound), machine.Kept...) {
		known[n] = true
	}
	seen := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		seen[n] = true
		if !known[n] {
			t.Errorf("table %q is in neither machine.GuildBound nor machine.Kept", n)
		}
	}
	for n := range known {
		if !seen[n] {
			t.Errorf("table %q is classified but does not exist", n)
		}
	}
}
