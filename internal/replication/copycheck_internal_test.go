package replication

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
)

// CA-REPL-01
// A full copy is checked against what the Citadel announces: its Guild and its
// epoch (CA-REPL-01).
func TestAFullCopyOfAnotherEpochOrGuildIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const guildID = "01J9Z0000000000000000000G1"
	primary, err := guilddb.Open(ctx, filepath.Join(t.TempDir(), "p.db"), store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { primary.Close() })
	if _, err := primary.Writer().ExecContext(ctx, `INSERT INTO guild (id, guild_id, ca_cert, epoch, epoch_certificate, timezone, recovery_public_key, created_at)
		VALUES (1, ?, x'00', 3, '{}', 'UTC', x'07', 0)`, guildID); err != nil {
		t.Fatal(err)
	}
	replica, err := guilddb.Open(ctx, filepath.Join(t.TempDir(), "r.db"), store.Options{Replica: true, Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { replica.Close() })

	try := func(guild string, announced uint64) error {
		t.Helper()
		file := filepath.Join(t.TempDir(), "copy.db")
		lsn, err := primary.Snapshot(ctx, file, store.SnapshotOptions{})
		if err != nil {
			t.Fatal(err)
		}
		r, err := NewReplica(ctx, ReplicaConfig{DB: replica, GuildID: guild})
		if err != nil {
			t.Fatal(err)
		}
		meta := protocol.Object{"lsn": json.RawMessage(strconv.FormatUint(lsn, 10)), "epoch": json.RawMessage(strconv.FormatUint(announced, 10))}
		return r.finalizeSnapshot(ctx, protocol.XferBegin{Kind: protocol.KindDBSnapshot, Meta: meta}, file)
	}
	if err := try(guildID, 2); err == nil {
		t.Error("a copy of epoch 3 was taken for an announced 2")
	}
	if err := try("01J9Z0000000000000000000G2", 3); err == nil {
		t.Error("a copy of another Guild was taken")
	}
	if got, _ := replica.LastLSN(ctx); got != 0 {
		t.Errorf("a refused copy changed the replica (lsn %d)", got)
	}
	if err := try(guildID, 3); err != nil {
		t.Errorf("the right copy was refused: %v", err)
	}
}
