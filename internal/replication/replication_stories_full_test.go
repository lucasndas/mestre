//go:build full

package replication_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/replication"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	"github.com/lucasndas/mestre/internal/testkit/fakegit"
	"github.com/lucasndas/mestre/internal/testkit/gitxsuite"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/internal/vault"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Part of CA-REPL-01
// CA-REPL-02
// Part of CA-REPL-05
func TestTheDatabaseStreamsToATowerFromTheFullCopyOnAndRefusesEveryOtherWrite(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{})
		ctx := context.Background()
		for i := range 30 {
			r.insertMachine(i)
		}
		tw := r.addTower("t1", true)

		// A Tower just accepted gets a full copy, equal to the primary at the lsn noted.
		eventually(t, "the full copy", snapshots(tw, 1))
		equal(t, r, tw)

		// A thousand transactions arrive in order, the replica is the primary's, and
		// the last replica.ack carries the current lsn.
		start := r.lsn()
		for i := range 1000 {
			tx, err := r.cdb.Writer().BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			for j := range 2 {
				if _, err := tx.Exec(`INSERT INTO machines (machine_id, name, instance, priority, excluded, state, names, joined_at) VALUES (?, 'n', '', 0, 0, 'active', '{}', 0)`, fmt.Sprintf("m%04d-%d", i, j)); err != nil {
					t.Fatal(err)
				}
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		}
		if r.lsn() != start+1000 {
			t.Fatalf("the primary is at %d, want %d", r.lsn(), start+1000)
		}
		equal(t, r, tw)
		frames := r.framesTo("t1")
		for i := 1; i < len(frames); i++ {
			if frames[i] <= frames[i-1] {
				t.Fatalf("frames out of order: %v", frames)
			}
		}
		var a, b string // the rows of the log the Tower stored are the primary's, record for record
		q := `SELECT group_concat(lsn || ':' || length(body), ',') FROM (SELECT * FROM replication_log WHERE lsn > ? ORDER BY lsn)`
		_ = r.cdb.Reader().QueryRow(q, start).Scan(&a)
		_ = tw.db.Reader().QueryRow(q, start).Scan(&b)
		if a == "" || a != b {
			t.Fatalf("the logs differ:\nprimary %.200s\nreplica %.200s", a, b)
		}
		eventuallyFor(t, "the last ack", 100*time.Millisecond, 60, func() bool { return statusOf(t, r, "t1").Acked == r.lsn() })

		// A hole: the frame of one record is lost, the next one arrives, and the Tower
		// asks again from the last record it has; no full copy for that.
		r.dropNextFor("t1")
		r.insertMachine(2001)
		eventually(t, "the frame to be lost", func() bool { return !r.dropping() })
		have := tw.lsn()
		r.insertMachine(2002)
		equal(t, r, tw)
		if tw.lsn() <= have+1 || tw.snapshots.Load() != 1 {
			t.Fatalf("after the hole: lsn %d (was %d), %d full copies; want the records, not a copy", tw.lsn(), have, tw.snapshots.Load())
		}

		// A record that does not fit the replica makes it ask for a full copy.
		if err := tw.db.Purge(ctx, "machines"); err != nil {
			t.Fatal(err)
		}
		r.exec(`UPDATE machines SET name = 'renamed' WHERE machine_id = 'm0001'`)
		eventually(t, "the second full copy", snapshots(tw, 2))
		equal(t, r, tw)
		if st := tw.rep.Status(); st.State != "ok" {
			t.Errorf("status = %+v", st)
		}

		// Nothing writes to the replica but the replication, while it replicates and after.
		for _, q := range []string{
			`INSERT INTO machines (machine_id, name, instance, priority, excluded, state, names, joined_at) VALUES ('x', 'x', '', 0, 0, 'active', '{}', 0)`,
			`DELETE FROM machines`,
			`UPDATE guild SET epoch = 99`,
		} {
			if _, err := tw.db.Writer().ExecContext(ctx, q); err == nil {
				t.Errorf("%q worked on the replica of a Tower", q)
			}
		}
		equal(t, r, tw)
	})
}

// Part of CA-REPL-01
// Part of CA-REPL-04
// Part of CA-REPL-05
// CA-REPL-10
func TestTheVaultReachesOnlyTheMachinesThatAreNotExcludedAndThePromotedTowerChangesTheKeyWithoutThePrivateRecoveryKey(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{vault: true, durable: true})
		ctx := context.Background()
		put := func(name, value string) {
			t.Helper()
			if err := r.vault.Put(ctx, name, vault.TypeProviderAPIKey, []byte(value), nil); err != nil {
				t.Fatal(err)
			}
		}
		put("provider_claude_api", "sk-ant-api03-Zx8Cv7Bn6Mm5Ll4Kk3Jj2Hh1")
		for i := range 5 {
			r.insertMachine(i)
		}
		var ciphertext []byte
		if err := r.cdb.Reader().QueryRow(`SELECT ciphertext FROM secrets`).Scan(&ciphertext); err != nil {
			t.Fatal(err)
		}
		r.roster.set("excluded", true)
		common := r.addTower("common", true)
		excluded := r.addTower("excluded", true)

		holds := func(tw *tower) bool {
			return (&vault.Copier{Keys: tw.keys, EnvelopePath: tw.env}).Holds(ctx)
		}
		rows := func(tw *tower) int {
			var n int
			_ = tw.db.Reader().QueryRow(`SELECT count(*) FROM secrets`).Scan(&n)
			return n
		}
		eventually(t, "both copies", func() bool { return common.lsn() == r.lsn() && excluded.lsn() == r.lsn() && holds(common) })

		// The common machine has everything; the excluded one every table but the
		// vault ones, which are empty, and not the secret anywhere in its file.
		equal(t, r, common)
		equal(t, r, excluded, "secrets", "site_login_attempts")
		if !holds(common) || rows(common) != 1 {
			t.Fatalf("the common machine: key and envelope %v, %d secrets; want all of it", holds(common), rows(common))
		}
		if _, err := os.Stat(excluded.env); err == nil || holds(excluded) || rows(excluded) != 0 {
			t.Fatalf("the excluded machine received vault: holds=%v rows=%d", holds(excluded), rows(excluded))
		}
		if b, _ := os.ReadFile(excluded.dbPath); bytes.Contains(b, ciphertext) {
			t.Error("the file of the excluded replica contains the secret")
		}
		if b, _ := os.ReadFile(common.dbPath); !bytes.Contains(b, ciphertext) {
			t.Error("the common replica should hold the (encrypted) secret")
		}

		// A secret written now reaches the common machine and not the excluded one.
		put("later", "sk-ant-api03-Aa1Bb2Cc3Dd4Ee5Ff6")
		eventually(t, "the new secret", func() bool { return rows(common) == 2 })
		equal(t, r, excluded, "secrets", "site_login_attempts")
		if rows(excluded) != 0 {
			t.Fatalf("the excluded machine got %d secrets", rows(excluded))
		}

		// The Owner excludes the common machine: it erases the three things and gives a receipt.
		r.roster.set("common", true)
		r.primary.ExclusionChanged(guild.Machine{ID: "common", Excluded: true})
		eventually(t, "the vault to be erased", func() bool { return !holds(common) && rows(common) == 0 })
		if _, err := os.Stat(common.env); err == nil {
			t.Error("the envelope is still there")
		}
		eventually(t, "the receipt", func() bool { return len(r.receipts()) >= 1 })
		if got := r.receipts(); len(got) != 1 || got[0] != "common" {
			t.Errorf("receipts = %v, want one from common", got)
		}
		put("after", "sk-ant-api03-Gg7Hh8Ii9Jj0")
		equal(t, r, common, "secrets", "site_login_attempts")
		if rows(common) != 0 {
			t.Errorf("an excluded machine got %d secrets", rows(common))
		}

		// Undoing the exclusion gives it the vault back, with a full copy that has the tables.
		r.roster.set("common", false)
		r.primary.ExclusionChanged(guild.Machine{ID: "common", Excluded: false})
		eventually(t, "the vault to come back", func() bool { return holds(common) && rows(common) == 3 && common.lsn() == r.lsn() })
		equal(t, r, common)

		// The key is changed and the Towers get the new vault.
		before, _ := (&vault.Copier{Keys: common.keys, EnvelopePath: common.env}).Export(ctx)
		if err := r.vault.Rotate(ctx); err != nil {
			t.Fatal(err)
		}
		r.primary.VaultChanged(ctx)
		want, _ := (&vault.Copier{Keys: r.ckeys, EnvelopePath: r.envelope}).Export(ctx)
		eventually(t, "the new vault", func() bool {
			got, err := (&vault.Copier{Keys: common.keys, EnvelopePath: common.env}).Export(ctx)
			return err == nil && bytes.Equal(got.Keyring, want.Keyring) && bytes.Equal(got.Envelope, want.Envelope)
		})
		if bytes.Equal(before.Keyring, want.Keyring) {
			t.Fatal("the key did not change")
		}
		equal(t, r, common)

		// The vault does not open on the replica of a Tower.
		cfg := vault.Config{DB: common.db, Keys: common.keys, EnvelopePath: common.env, RecoveryPublicKey: bytes.Repeat([]byte{1}, 32), Masker: logging.NewMasker()}
		if _, err := vault.Open(ctx, cfg); !errors.Is(err, vault.ErrReplica) {
			t.Errorf("vault.Open on a replica = %v, want ErrReplica", err)
		}
		if _, err := vault.Create(ctx, cfg); !errors.Is(err, vault.ErrReplica) {
			t.Errorf("vault.Create on a replica = %v, want ErrReplica", err)
		}

		// The Citadel is lost and the Tower is promoted: its file becomes the main
		// database, no copy, and the vault opens from its own copy. Changing the key
		// needs only the public recovery key, and the new envelope opens with the
		// recovery key.
		envelopeBefore, _ := os.ReadFile(common.env)
		common.stop()
		r.stop()
		promoted, err := guildtpl.Open(ctx, common.dbPath, store.Options{Clock: r.clk}) // flushes: the key rotates
		if err != nil {
			t.Fatal(err)
		}
		defer promoted.Close()
		v, err := vault.Open(ctx, vault.Config{
			DB: promoted, Keys: common.keys, EnvelopePath: common.env, RecoveryPublicKey: r.rk.PublicKey(), Masker: logging.NewMasker(), Clock: r.clk,
		})
		if err != nil || v.Err() != nil {
			t.Fatalf("the promoted Tower cannot open the vault from its copy: %v / %v", err, v.Err())
		}
		if err := v.Rotate(ctx); err != nil {
			t.Fatal(err)
		}
		envelopeAfter, _ := os.ReadFile(common.env)
		if bytes.Equal(envelopeBefore, envelopeAfter) {
			t.Fatal("the envelope did not change with the key")
		}
		restored := &memKeys{}
		if err := vault.RestoreKey(ctx, envelopeAfter, r.rk, restored); err != nil {
			t.Fatalf("the new envelope does not open with the recovery key: %v", err)
		}
		v2, err := vault.Open(ctx, vault.Config{
			DB: promoted, Keys: restored, EnvelopePath: filepath.Join(t.TempDir(), "e.bin"), RecoveryPublicKey: r.rk.PublicKey(), Masker: logging.NewMasker(), Clock: r.clk,
		})
		if err != nil || v2.Err() != nil {
			t.Fatalf("open with the restored key: %v / %v", err, v2.Err())
		}
		var got []byte
		if err := v2.Use(ctx, "provider_claude_api", "test", func(b []byte) error { got = append(got, b...); return nil }); err != nil || string(got) != "sk-ant-api03-Zx8Cv7Bn6Mm5Ll4Kk3Jj2Hh1" {
			t.Fatalf("the secret read with the restored key = %q, %v", got, err)
		}
	})
}

// An erasing that could not truncate the WAL (a reader holds it) is not
// forgotten: the state stays on disk, no receipt goes while the secrets may
// still be in the -wal file, and the next order to erase (or the retry of the
// replica) finishes it and sends the receipt.
func TestAnIncompleteErasingIsFinishedAndThenAudited(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{vault: true, busy: 20 * time.Millisecond})
		ctx := context.Background()
		if err := r.vault.Put(ctx, "k", vault.TypeProviderAPIKey, []byte("sk-ant-api03-Zx8Cv7Bn6Mm5Ll4Kk3Jj2Hh1"), nil); err != nil {
			t.Fatal(err)
		}
		a := r.addTower("a", true)
		b := r.addTower("b", true)
		eventually(t, "both copies", func() bool { return a.lsn() == r.lsn() && b.lsn() == r.lsn() })
		audited := func(id string) int {
			var n int
			if err := r.cdb.Reader().QueryRow(`SELECT count(*) FROM audit_log WHERE action = ? AND machine_id = ?`, replication.AuditVaultDropped, id).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		hold := func(tw *tower) *sql.Tx {
			t.Helper()
			rd, err := tw.db.Reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			var n int
			if err := rd.QueryRowContext(ctx, `SELECT count(*) FROM secrets`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return rd
		}
		owed := func(tw *tower) bool {
			raw, err := os.ReadFile(tw.dbPath + ".excluded")
			return err == nil && strings.Contains(string(raw), "purge incomplete")
		}

		// A reader holds the WAL of each replica while the order arrives.
		readerA, readerB := hold(a), hold(b)
		for _, id := range []string{"a", "b"} {
			r.roster.set(id, true)
			r.primary.ExclusionChanged(guild.Machine{ID: id, Excluded: true})
		}
		eventually(t, "the incomplete state of both", func() bool { return owed(a) && owed(b) })
		settle()
		if audited("a") != 0 || audited("b") != 0 {
			t.Fatal("a receipt went while the WAL still held what was erased")
		}

		// a: the reader is gone and the Citadel gives the order again (it does
		// at every connection of an excluded machine).
		if err := readerA.Rollback(); err != nil {
			t.Fatal(err)
		}
		r.primary.ExclusionChanged(guild.Machine{ID: "a", Excluded: true})
		eventually(t, "the receipt of a", func() bool { return audited("a") == 1 })
		if owed(a) {
			t.Error("a finished erasing is still owed")
		}
		settle()
		if audited("b") != 0 {
			t.Error("the receipt of b went with the reader still there")
		}

		// b: nothing is sent again; the retry of the replica finishes it.
		if err := readerB.Rollback(); err != nil {
			t.Fatal(err)
		}
		eventuallyFor(t, "the receipt of b by the retry", time.Second, int(2*replication.DefaultResyncRetry/time.Second), func() bool { return audited("b") == 1 })
		if owed(b) {
			t.Error("a finished erasing is still owed")
		}
	})
}

// Part of CA-REPL-04
// The vault is gone from a machine that was told to erase it, and a full copy
// made before the exclusion does not bring it back (CA-REPL-04).
func TestAFullCopyThatArrivesAfterVaultDropDoesNotRestoreTheVault(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{vault: true})
		ctx := context.Background()
		if err := r.vault.Put(ctx, "k", vault.TypeProviderAPIKey, []byte("sk-ant-api03-Zx8Cv7Bn6Mm5Ll4Kk3Jj2Hh1"), nil); err != nil {
			t.Fatal(err)
		}
		for i := range 5 {
			r.insertMachine(i)
		}
		var ciphertext []byte
		if err := r.cdb.Reader().QueryRow(`SELECT ciphertext FROM secrets`).Scan(&ciphertext); err != nil {
			t.Fatal(err)
		}
		tw := r.addTower("t1", true)
		cp := &vault.Copier{Keys: tw.keys, EnvelopePath: tw.env}
		secrets := func() int {
			var n int
			_ = tw.db.Reader().QueryRow(`SELECT count(*) FROM secrets`).Scan(&n)
			return n
		}
		eventually(t, "the vault", func() bool { return tw.lsn() == r.lsn() && cp.Holds(ctx) && secrets() == 1 })

		// A copy that was being sent when the exclusion came is on the disk of the
		// Tower in parts; so is a transfer of something else.
		if err := os.MkdirAll(tw.xfer, 0o700); err != nil {
			t.Fatal(err)
		}
		pending := func(id, kind string) {
			raw, _ := json.Marshal(protocol.XferBegin{XferID: id, Kind: kind, Size: 10, SHA256: "00"})
			for name, body := range map[string][]byte{id + ".json": raw, id + ".part": []byte("half of a copy")} {
				if err := os.WriteFile(filepath.Join(tw.xfer, name), body, 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
		pending("copy1", protocol.KindDBSnapshot)
		pending("file1", protocol.KindFile)

		// The order to erase comes; the Citadel does not know of it, so the copy it
		// makes next still has the vault tables.
		if _, err := r.server.Link("t1").Send(ctx, protocol.Outgoing{Type: protocol.TypeVaultDrop, Payload: protocol.Empty{}}); err != nil {
			t.Fatal(err)
		}
		eventually(t, "the vault to be erased", func() bool { return !cp.Holds(ctx) && secrets() == 0 && tw.rep.Status().VaultDropped })
		for _, name := range []string{"copy1.json", "copy1.part"} {
			if _, err := os.Stat(filepath.Join(tw.xfer, name)); err == nil {
				t.Errorf("%s: a full copy that was arriving is still on the disk", name)
			}
		}
		for _, name := range []string{"file1.json", "file1.part"} {
			if _, err := os.Stat(filepath.Join(tw.xfer, name)); err != nil {
				t.Errorf("%s: another kind of transfer was removed: %v", name, err)
			}
		}

		// The records that follow already come without the vault tables.
		before := tw.snapshots.Load()
		diverge(t, tw, "m0002")
		r.exec(`UPDATE machines SET priority = 9 WHERE machine_id = 'm0002'`)
		eventually(t, "the full copy", func() bool { return tw.snapshots.Load() == before+1 })
		if n := secrets(); n != 0 {
			t.Fatalf("the full copy put %d secrets back", n)
		}
		if cp.Holds(ctx) {
			t.Error("the key is back")
		}
		equal(t, r, tw, "secrets", "site_login_attempts")
		for _, f := range []string{tw.dbPath, tw.dbPath + "-wal"} {
			if b, err := os.ReadFile(f); err == nil && bytes.Contains(b, ciphertext) {
				t.Errorf("%s contains the secret", filepath.Base(f))
			}
		}

		// The order is remembered: a restart before the next connection still
		// treats the machine as excluded.
		tw.stop()
		db, err := guilddb.Open(ctx, tw.dbPath, store.Options{Replica: true, Synchronous: "OFF"})
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		rep, err := replication.NewReplica(ctx, replication.ReplicaConfig{DB: db, GuildID: guildULID})
		if err != nil {
			t.Fatal(err)
		}
		if !rep.Status().VaultDropped {
			t.Error("the order to erase the vault was forgotten by a restart")
		}
	})
}

// Part of CA-REPL-14
// A machine that left the Guild takes nothing more of it while its engine still
// runs and the Citadel, which has not revoked it yet, goes on sending: the key
// of the vault does not come back, nor a file, nor the replica. The Citadel
// sends the key again after a rotation and files at every change.
func TestACitadelThatStillSendsToAMachineThatLeftChangesNothingThere(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{files: true, vault: true})
		ctx := context.Background()
		if err := r.vault.Put(ctx, "k", vault.TypeProviderAPIKey, []byte("sk-ant-api03-Zx8Cv7Bn6Mm5Ll4Kk3Jj2Hh1"), nil); err != nil {
			t.Fatal(err)
		}
		tw := r.addTower("t1", true)
		copier := &vault.Copier{Keys: tw.keys, EnvelopePath: tw.env}
		eventually(t, "the Tower to have the replica and the vault", func() bool { return tw.lsn() == r.lsn() && copier.Holds(ctx) })

		var w guild.Wiper = tw.rep
		if err := w.Wipe(ctx, "removed"); err != nil {
			t.Fatal(err)
		}
		if copier.Holds(ctx) {
			t.Fatal("set-up: the Wipe left the key")
		}

		if err := os.MkdirAll(r.filesDir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(r.filesDir, "late.md"), []byte("late"), 0o640); err != nil {
			t.Fatal(err)
		}
		r.primary.VaultChanged(ctx)
		r.primary.FileChanged("classes", "late.md")
		r.insertMachine(1)
		advance(time.Minute)

		for _, p := range []string{tw.env, tw.dbPath, filepath.Join(tw.files, "late.md")} {
			if _, err := os.Stat(p); err == nil {
				t.Errorf("%s is back after the Wipe", p)
			}
		}
		if copier.Holds(ctx) {
			t.Error("the key of the vault is back after the Wipe")
		}
		if entries, _ := os.ReadDir(tw.xfer); len(entries) != 0 {
			t.Errorf("the transfers folder has %v after the Wipe", entries)
		}
	})
}

// Part of CA-REPL-08, the part of milestone 2: a file that a record cites is on the
// Tower before the record is applied there.
func TestFilesReachTheTowerBeforeTheRecordsThatCiteThemAndAMachineThatLeavesErasesThem(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		cites := func(c store.Change) []replication.FileRef {
			if c.Table == "machines" && c.Op == store.OpInsert && strings.HasSuffix(c.New[1].(string), ".md") {
				return []replication.FileRef{{Root: "classes", Path: c.New[1].(string)}} // the name column stands for a path
			}
			return nil
		}
		r := newRig(t, rigOpts{files: true, cites: cites, vault: true})
		ctx := context.Background()
		write := func(rel, body string) {
			t.Helper()
			p := filepath.Join(r.filesDir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o640); err != nil {
				t.Fatal(err)
			}
		}
		read := func(tw *tower, rel string) string {
			b, _ := os.ReadFile(filepath.Join(tw.files, filepath.FromSlash(rel)))
			return string(b)
		}
		// What is in the folder when the Tower connects comes first.
		write("a.md", "a")
		write("dir/b.md", "b")
		write("dir/deep/c.md", "c")
		if err := r.vault.Put(ctx, "k", vault.TypeProviderAPIKey, []byte("sk-ant-api03-Zx8Cv7Bn6Mm5Ll4Kk3Jj2Hh1"), nil); err != nil {
			t.Fatal(err)
		}
		tw := r.addTower("t1", true)
		eventually(t, "the folder", func() bool { return read(tw, "dir/deep/c.md") == "c" && tw.lsn() == r.lsn() })
		if read(tw, "a.md") != "a" || read(tw, "dir/b.md") != "b" {
			t.Fatal("the files of the folder did not all arrive")
		}

		// A record that cites a file finds it on the Tower when it is applied.
		write("qa/classe.md", "# classe QA\n")
		write("dev/classe.md", "# classe Dev\n")
		insert := func(id, name string) uint64 {
			r.exec(`INSERT INTO machines (machine_id, name, instance, priority, excluded, state, names, joined_at) VALUES (?, ?, '', 0, 0, 'active', '{}', 0)`, id, name)
			return r.lsn()
		}
		lsn1 := insert("m1", "qa/classe.md")
		eventually(t, "the first record", func() bool { return tw.lsn() == lsn1 })
		lsn2 := insert("m2", "dev/classe.md")
		equal(t, r, tw)
		ev := tw.log()
		for _, c := range []struct {
			file string
			lsn  uint64
		}{{"qa/classe.md", lsn1}, {"dev/classe.md", lsn2}} {
			f, a := indexOf(ev, "file "+c.file), appliedAt(ev, c.lsn)
			if f < 0 || a < 0 || f > a || read(tw, c.file) == "" {
				t.Fatalf("%s arrived at event %d and the record %d that cites it was applied at %d; events = %v", c.file, f, c.lsn, a, ev)
			}
		}

		// A file that did not change is not sent again; a new content for the same path is.
		n := countPrefix(tw.log(), "file ")
		r.exec(`UPDATE machines SET priority = 5 WHERE machine_id = 'm1'`)
		equal(t, r, tw)
		if got := countPrefix(tw.log(), "file "); got != n {
			t.Errorf("files received grew from %d to %d: an update that cites no new file sent one", n, got)
		}
		write("qa/classe.md", "# classe QA 2\n")
		insert("m3", "qa/classe.md")
		eventually(t, "the new content", func() bool { return read(tw, "qa/classe.md") == "# classe QA 2\n" })

		// A reconnection sends nothing again: the Tower has the content.
		equal(t, r, tw)
		n = countPrefix(tw.log(), "file ")
		tw.stop()
		tw.start(true)
		eventually(t, "the reconnection", func() bool { return tw.lsn() == r.lsn() && statusOf(t, r, "t1").Acked == r.lsn() })
		if got := countPrefix(tw.log(), "file "); got != n {
			t.Errorf("%d files sent again to a Tower that had them", got-n)
		}

		// A file nobody cites goes when the Citadel says so, and a path that leaves
		// the folder goes nowhere.
		write("new.md", "new")
		r.primary.FileChanged("classes", "new.md")
		eventually(t, "new.md", func() bool { return read(tw, "new.md") == "new" })
		r.primary.FileChanged("classes", "../escape.md")
		settle()
		if _, err := os.Stat(filepath.Join(filepath.Dir(tw.files), "escape.md")); err == nil {
			t.Error("a file escaped the folder")
		}

		// The machine leaves the Guild: vault, replica and files are erased.
		var w guild.Wiper = tw.rep
		tw.stop()
		if err := w.Wipe(ctx, "removed"); err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{tw.dbPath, tw.env, filepath.Join(tw.files, "a.md"), filepath.Join(tw.files, "dir")} {
			if _, err := os.Stat(p); err == nil {
				t.Errorf("%s is still there", p)
			}
		}
		if (&vault.Copier{Keys: tw.keys, EnvelopePath: tw.env}).Holds(ctx) {
			t.Error("the key is still there")
		}
	})
}

// Part of CA-REPL-08 (the mirrors; the criterion with real git and real sockets is in
// the full suite, and the mirror of a workspace itself is milestone 4)
func TestTheMirrorFollowsTheCitadelByBundleWithTheFakeGit(t *testing.T) {
	t.Parallel()
	mirrorScenario(t, fakegit.NewGit(clock.NewFake(gitxsuite.Now)), func(_ testing.TB, r gitx.Repo, rel, content string) {
		r.(*fakegit.Handle).SetFile(rel, content)
	})
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

func countPrefix(s []string, prefix string) int {
	n := 0
	for _, x := range s {
		if strings.HasPrefix(x, prefix) {
			n++
		}
	}
	return n
}

func (tw *tower) log() []string {
	tw.evMu.Lock()
	defer tw.evMu.Unlock()
	return slices.Clone(tw.events)
}

// dropNextFor loses the next frame of records the Citadel sends to the Tower.
func (r *rig) dropNextFor(id string) { r.dropTarget.Store(id) }

// dropping says whether the frame to lose is still to come.
func (r *rig) dropping() bool { return r.dropTarget.Load().(string) != "" }

// appliedAt is the position in the events of the batch that applied lsn, or -1.
func appliedAt(events []string, lsn uint64) int {
	for i, e := range events {
		var first, last uint64
		if n, _ := fmt.Sscanf(e, "applied %d-%d", &first, &last); n == 2 && first <= lsn && lsn <= last {
			return i
		}
	}
	return -1
}
