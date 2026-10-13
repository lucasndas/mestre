package replication

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
)

// CA-REPL-04
// A migration is SQL, and the records of an excluded machine are stripped of
// the changes of the vault tables, not of what a script does to them: what a
// migration seeds there is erased again (CA-REPL-04).
func TestAMigrationThatSeedsTheVaultTablesOfAnExcludedReplicaIsUndone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mestre.db")
	primary, err := guilddb.Open(ctx, path, store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	if err := primary.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := guilddb.Open(ctx, path, store.Options{Replica: true, Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	last, err := db.LastLSN(ctx)
	if err != nil || last == 0 {
		t.Fatalf("last lsn %d, %v", last, err)
	}
	r, err := NewReplica(ctx, ReplicaConfig{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	r.excluded = true
	seed := store.EncodeChanges([]store.Change{{Op: store.OpSQL, SQL: `INSERT INTO secrets (name, type, ciphertext, nonce, key_version, fingerprint, created_at, updated_at) VALUES ('seeded', 'x', x'01', x'00', 1, 'f', 0, 0)`}})
	r.queue = []store.LogRow{{LSN: last + 1, At: 1, Body: seed}}
	r.lastQueued = last + 1
	r.drain(ctx)

	if got, _ := db.LastLSN(ctx); got != last+1 {
		t.Fatalf("the record was not applied: lsn %d", got)
	}
	var n int
	if err := db.Reader().QueryRowContext(ctx, `SELECT count(*) FROM secrets`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the migration left %d secrets on an excluded replica (%v)", n, err)
	}
}

// CA-REPL-03
// A replica.resync that comes before the stream of its Tower exists is kept for
// the stream, not dropped.
func TestAResyncThatComesBeforeTheStreamIsKept(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := guilddb.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO guild (id, guild_id, ca_cert, epoch, epoch_certificate, timezone, recovery_public_key, created_at)
		VALUES (1, '01J9Z0000000000000000000G1', x'00', 1, '{}', 'UTC', x'07', 0)`); err != nil {
		t.Fatal(err)
	}
	p, err := NewPrimary(ctx, PrimaryConfig{DB: db, Roster: RosterFunc(func(context.Context, string) (bool, error) { return false, nil }), Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	from := uint64(7)
	m := &protocol.Message{Peer: protocol.Peer{ID: "t1"}, Payload: &protocol.ReplicaResync{FromLSN: &from}}
	if _, err := p.handleResync(ctx, m); err != nil {
		t.Fatal(err)
	}
	req, ok := p.early["t1"]
	if !ok || req.from == nil || *req.from != 7 {
		t.Fatalf("the request was not kept: %+v", p.early)
	}
}
