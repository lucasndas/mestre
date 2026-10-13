package machine_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/store/machine"
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
