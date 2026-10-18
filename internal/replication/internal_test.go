package replication

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
)

// CA-REPL-04
// A migration is SQL, and the records of an excluded machine are stripped of
// the changes of the vault tables, not of what a script does to them: what a
// migration seeds there is erased again (CA-REPL-04).
func TestAMigrationThatSeedsTheVaultTablesOfAnExcludedReplicaIsUndone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mestre.db")
	primary, err := guildtpl.Open(ctx, path, store.Options{Synchronous: "OFF"})
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
	db, err := guildtpl.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF"})
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

// CA-REPL-04
// The order to erase removes the full copies that were arriving when it came,
// and only those: the Citadel sends the order at every connection of an excluded
// machine and then makes a full copy without the vault tables, whose xfer.begin
// comes after the order. Erasing in the background used to remove that copy as
// well, whenever it ran after the begin, and the connection was closed
// (CA-REPL-04).
func TestTheOrderToEraseDoesNotRemoveTheFullCopyThatBeginsAfterIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "mestre.db")
	primary, err := guildtpl.Open(ctx, path, store.Options{Synchronous: "OFF"})
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
	xfer := filepath.Join(dir, "xfer")
	if err := os.MkdirAll(xfer, 0o700); err != nil {
		t.Fatal(err)
	}
	r, err := NewReplica(ctx, ReplicaConfig{DB: db, TransferDir: xfer})
	if err != nil {
		t.Fatal(err)
	}
	copyOf := func(id string) {
		t.Helper()
		raw, _ := json.Marshal(protocol.XferBegin{XferID: id, Kind: protocol.KindDBSnapshot, Size: 10, SHA256: "00"})
		for name, body := range map[string][]byte{id + ".json": raw, id + ".part": []byte("half of a copy")} {
			if err := os.WriteFile(filepath.Join(xfer, name), body, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	exists := func(name string) bool { _, err := os.Stat(filepath.Join(xfer, name)); return err == nil }

	copyOf("before")
	if _, err := r.handleVaultDrop(ctx, &protocol.Message{}); err != nil {
		t.Fatal(err)
	}
	// The order has been handled; the copy begins after it, and only then does
	// the erasing in the background get to run.
	copyOf("after")
	r.bg.Wait()

	for _, name := range []string{"before.json", "before.part"} {
		if exists(name) {
			t.Errorf("%s: the copy that was arriving when the order came is still on the disk", name)
		}
	}
	for _, name := range []string{"after.json", "after.part"} {
		if !exists(name) {
			t.Errorf("%s: the copy that began after the order was removed", name)
		}
	}
}

// CA-REPL-12
// A machine that leaves the Guild erases what it kept from the time it was the
// Citadel, besides the replica: the databases it discarded when the epoch
// changed (each is a whole copy of the main database, with the hashes of the
// passwords and of the PINs, whatever its name or suffix), the full copies it
// made and the transfers that were arriving. What is not in those places
// stays (CA-REPL-12).
func TestWipeErasesWhatTheMachineKeptFromWhenItWasTheCitadel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "mestre.db")
	primary, err := guildtpl.Open(ctx, path, store.Options{Synchronous: "OFF"})
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
	xfer, copies := filepath.Join(dir, "transferencias"), filepath.Join(dir, "copias")
	for _, d := range []string{xfer, copies} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	erased := []string{
		"descartado-3.db", "descartado-3.db-wal", "descartado-3.db-shm", "descartado-3.json", "descartado-3-acoes.json",
		"descartado-3.db.1760000000000", "descartado-3.db.1760000000000-wal", "descartado-3.db.1760000000000-shm",
		"copias/snapshot-01J9Z0.db", "transferencias/01J9Z1.part", "transferencias/01J9Z1.json", "transferencias/01J9Z2.part",
	}
	kept := []string{"unrelated.txt", "descartar-nao.txt"}
	for _, name := range append(slices.Clone(erased), kept...) {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte("a copy of the main database"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r, err := NewReplica(ctx, ReplicaConfig{
		DB: db, TransferDir: xfer, Leftovers: []Leftover{{Dir: dir, Prefix: "descartado-"}, {Dir: copies}, {Dir: filepath.Join(dir, "no-such-folder")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Wipe(ctx, "removed"); err != nil {
		t.Fatal(err)
	}
	for _, name := range erased {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name))); err == nil {
			t.Errorf("%s is still there", name)
		}
	}
	for _, name := range kept {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s was erased: %v", name, err)
		}
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("the replica is still there")
	}
}
