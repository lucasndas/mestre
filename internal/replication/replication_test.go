package replication_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/replication"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	"github.com/lucasndas/mestre/internal/vault"
)

func equal(t *testing.T, r *rig, tw *tower, skip ...string) {
	t.Helper()
	if a, b := dump(t, r.cdb, skip...), dump(t, tw.db, skip...); a != b {
		t.Fatalf("the replica of %s differs from the primary\nprimary:\n%s\nreplica:\n%s", tw.id, a, b)
	}
}

func statusOf(t *testing.T, r *rig, id string) replication.PeerStatus {
	t.Helper()
	st, err := r.primary.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range st {
		if s.Machine == id {
			return s
		}
	}
	t.Fatalf("%s is not connected", id)
	return replication.PeerStatus{}
}

// CA-REPL-01
func TestAFreshTowerGetsAFullCopyEqualToThePrimaryAtTheNotedLSN(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{})
		for i := range 30 {
			r.insertMachine(i)
		}
		marker := bytes.Repeat([]byte("SECRET-CIPHERTEXT-"), 10)
		r.insertSecret("token", marker)

		common := r.addTower("common", true)
		excluded := r.addTower("excluded", true)
		r.roster.set("excluded", true)
		settle()
		// "excluded" connected before the roster said so: connect it again.
		excluded.stop()
		excluded.start(true)
		settle()

		if got := common.snapshots.Load(); got != 1 {
			t.Fatalf("snapshots received = %d, want 1", got)
		}
		if common.lsn() != r.lsn() {
			t.Fatalf("replica at lsn %d, primary at %d", common.lsn(), r.lsn())
		}
		equal(t, r, common)

		// The excluded machine has every table but the vault ones, which are empty,
		// and the secret is nowhere in its file.
		equal(t, r, excluded, "secrets", "site_login_attempts")
		var n int
		if err := excluded.db.Reader().QueryRow(`SELECT count(*) FROM secrets`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("the excluded replica has %d secrets (%v)", n, err)
		}
		if b, _ := os.ReadFile(excluded.dbPath); bytes.Contains(b, marker) {
			t.Error("the file of the excluded replica contains the secret")
		}
		if b, _ := os.ReadFile(common.dbPath); !bytes.Contains(b, marker) {
			t.Error("the common replica should hold the (encrypted) secret")
		}
	})
}

// CA-REPL-02
func TestAThousandTransactionsArriveInOrderAndTheLastAckCarriesTheCurrentLSN(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{})
		tw := r.addTower("t1", true)
		settle()
		start := r.lsn()
		for i := range 1000 {
			tx, err := r.cdb.Writer().BeginTx(context.Background(), nil)
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
			if i%100 == 0 {
				settle() // let it ship as it goes, as a Citadel that is busy would
			}
		}
		settle()
		advance(time.Second) // the Tower confirms every second

		if r.lsn() != start+1000 {
			t.Fatalf("the primary is at %d, want %d", r.lsn(), start+1000)
		}
		if tw.lsn() != r.lsn() {
			t.Fatalf("replica at %d, primary at %d", tw.lsn(), r.lsn())
		}
		equal(t, r, tw)
		// In order: the first lsn of each frame is exactly after the last of the one before.
		frames := r.framesTo("t1")
		for i := 1; i < len(frames); i++ {
			if frames[i] <= frames[i-1] {
				t.Fatalf("frames out of order: %v", frames)
			}
		}
		// The rows of the log the Tower stored are the primary's, record for record.
		var a, b string
		q := `SELECT group_concat(lsn || ':' || length(body), ',') FROM (SELECT * FROM replication_log WHERE lsn > ? ORDER BY lsn)`
		_ = r.cdb.Reader().QueryRow(q, start).Scan(&a)
		_ = tw.db.Reader().QueryRow(q, start).Scan(&b)
		if a == "" || a != b {
			t.Fatalf("the logs differ:\nprimary %.200s\nreplica %.200s", a, b)
		}
		if got := statusOf(t, r, "t1").Acked; got != r.lsn() {
			t.Errorf("the last replica.ack carried %d, want the current lsn %d", got, r.lsn())
		}
	})
}

func TestARecordIsAckedAtOnceWhen256AreWaitingAndOtherwiseEverySecond(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{})
		tw := r.addTower("t1", true)
		settle()
		base := r.lsn()
		for i := range 255 {
			r.insertMachine(i)
		}
		settle()
		if got := statusOf(t, r, "t1").Acked; got != base {
			t.Fatalf("acked %d before a second and fewer than 256 records, want %d", got, base)
		}
		r.insertMachine(255)
		settle()
		if got := statusOf(t, r, "t1").Acked; got != base+256 {
			t.Fatalf("acked %d after 256 records, want %d", got, base+256)
		}
		r.insertMachine(256)
		settle()
		advance(time.Second)
		if got := statusOf(t, r, "t1").Acked; got != r.lsn() {
			t.Fatalf("acked %d after a second, want %d", got, r.lsn())
		}
		_ = tw
	})
}

// CA-REPL-03: back within the retention, only the records that are missing;
// back after it, a full copy.
func TestATowerThatComesBackGetsOnlyWhatIsMissingOrAFullCopyAfterTheRetention(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{})
		for i := range 10 {
			r.insertMachine(i)
		}
		tw := r.addTower("t1", true)
		settle()
		equal(t, r, tw)
		had := tw.lsn()

		tw.stop()
		for i := 10; i < 40; i++ {
			r.insertMachine(i)
		}
		advance(10 * time.Minute) // inside the hour
		before := len(r.framesTo("t1"))
		tw.start(true)
		settle()
		equal(t, r, tw)
		if got := tw.snapshots.Load(); got != 1 {
			t.Fatalf("snapshots = %d, want only the first: it was back within the retention", got)
		}
		after := r.framesTo("t1")[before:]
		if len(after) == 0 || after[0] != had+1 {
			t.Fatalf("the first record after the reconnection was %v, want %d (right after what the Tower had)", after, had+1)
		}

		// Away for longer than the retention: the log no longer reaches it.
		had = tw.lsn()
		tw.stop()
		r.insertMachine(100)
		r.insertMachine(101)
		advance(2 * time.Hour) // the Citadel prunes every minute
		lo, _, err := r.cdb.LogBounds(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if lo <= had+1 {
			t.Fatalf("the log still holds lsn %d (lowest %d): the retention did not run", had+1, lo)
		}
		tw.start(true)
		settle()
		equal(t, r, tw)
		if got := tw.snapshots.Load(); got != 2 {
			t.Fatalf("snapshots = %d, want a second full copy after the retention", got)
		}
	})
}

func TestATowerOfAnotherEpochGetsAFullCopy(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{})
		for i := range 5 {
			r.insertMachine(i)
		}
		tw := r.addTower("t1", true)
		settle()
		tw.stop()
		r.exec(`UPDATE guild SET epoch = 2`) // a promotion happened meanwhile
		r.insertMachine(50)
		tw.start(true)
		settle()
		if got := tw.snapshots.Load(); got != 2 {
			t.Fatalf("snapshots = %d, want a full copy because the epoch changed", got)
		}
		equal(t, r, tw)
	})
}

func TestAHoleAsksAgainFromTheLastRecordAndTheStreamGoesOn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{})
		tw := r.addTower("t1", true)
		settle()
		r.insertMachine(1)
		settle()
		have := tw.lsn()
		r.dropNext.Store(true) // the frame of the next record is lost on the way
		r.insertMachine(2)
		settle()
		if tw.lsn() != have {
			t.Fatal("the lost record arrived")
		}
		r.insertMachine(3) // arrives, after a hole
		settle()
		advance(time.Second)
		equal(t, r, tw)
		if tw.lsn() != r.lsn() {
			t.Fatalf("replica at %d, primary at %d", tw.lsn(), r.lsn())
		}
		if got := tw.snapshots.Load(); got != 1 {
			t.Errorf("snapshots = %d: a hole is answered with the records, not a copy", got)
		}
	})
}

func TestARecordThatDoesNotFitMakesTheTowerThrowItsReplicaAwayAndAskForACopy(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{})
		for i := range 5 {
			r.insertMachine(i)
		}
		tw := r.addTower("t1", true)
		settle()
		// The replica loses rows it should have: it is not the primary's copy any more.
		if err := tw.db.Purge(context.Background(), "machines"); err != nil {
			t.Fatal(err)
		}
		r.exec(`UPDATE machines SET name = 'renamed' WHERE machine_id = 'm0001'`)
		settle()
		advance(time.Second)
		if got := tw.snapshots.Load(); got != 2 {
			t.Fatalf("snapshots = %d, want a new full copy", got)
		}
		equal(t, r, tw)
		if st := tw.rep.Status(); st.State != "ok" {
			t.Errorf("status = %+v", st)
		}
	})
}

func TestARecordBiggerThanAFrameGoesByFullCopy(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{})
		tw := r.addTower("t1", true)
		settle()
		r.exec(`UPDATE machines SET sheet = ? WHERE 0`, "x") // no row: nothing
		r.insertMachine(1)
		r.exec(`UPDATE machines SET sheet = ?`, strings.Repeat("x", 1200*1024))
		settle()
		advance(time.Second)
		if got := tw.snapshots.Load(); got != 2 {
			t.Fatalf("snapshots = %d, want the big record to go by a full copy", got)
		}
		equal(t, r, tw)
		r.insertMachine(2)
		settle()
		equal(t, r, tw)
	})
}

// CA-REPL-04
func TestOnlyAMachineThatIsNotExcludedGetsTheVaultAndExcludingItTakesItAway(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{vault: true})
		ctx := context.Background()
		if err := r.vault.Put(ctx, "provider_claude_api", vault.TypeProviderAPIKey, []byte("sk-ant-api03-Zx8Cv7Bn6Mm5Ll4Kk3Jj2Hh1"), nil); err != nil {
			t.Fatal(err)
		}
		r.roster.set("excluded", true)
		common := r.addTower("common", true)
		excluded := r.addTower("excluded", true)
		settle()

		holds := func(tw *tower) bool {
			return (&vault.Copier{Keys: tw.keys, EnvelopePath: tw.env}).Holds(ctx)
		}
		rows := func(tw *tower) int {
			var n int
			if err := tw.db.Reader().QueryRow(`SELECT count(*) FROM secrets`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		if !holds(common) || rows(common) != 1 {
			t.Fatalf("the common machine: key and envelope %v, %d secrets; want all of it", holds(common), rows(common))
		}
		if _, err := os.Stat(excluded.env); err == nil || holds(excluded) || rows(excluded) != 0 {
			t.Fatalf("the excluded machine received vault: holds=%v rows=%d", holds(excluded), rows(excluded))
		}
		// A secret written now reaches the common machine and not the excluded one.
		if err := r.vault.Put(ctx, "later", vault.TypeProviderAPIKey, []byte("sk-ant-api03-Aa1Bb2Cc3Dd4Ee5Ff6"), nil); err != nil {
			t.Fatal(err)
		}
		settle()
		if rows(common) != 2 || rows(excluded) != 0 {
			t.Fatalf("after a new secret: %d on the common machine, %d on the excluded one", rows(common), rows(excluded))
		}

		// The Owner excludes the common machine: it erases the three things and gives a receipt.
		r.roster.set("common", true)
		r.primary.ExclusionChanged(guild.Machine{ID: "common", Excluded: true})
		settle()
		if holds(common) || rows(common) != 0 {
			t.Fatalf("after the exclusion: holds=%v rows=%d", holds(common), rows(common))
		}
		if _, err := os.Stat(common.env); err == nil {
			t.Error("the envelope is still there")
		}
		if len(r.dropped) != 1 || r.dropped[0] != "common" {
			t.Errorf("receipts = %v, want one from common", r.dropped)
		}
		// And it does not get the vault again by the records that follow.
		if err := r.vault.Put(ctx, "after", vault.TypeProviderAPIKey, []byte("sk-ant-api03-Gg7Hh8Ii9Jj0"), nil); err != nil {
			t.Fatal(err)
		}
		settle()
		if rows(common) != 0 {
			t.Errorf("an excluded machine got %d secrets", rows(common))
		}
		equal(t, r, common, "secrets", "site_login_attempts")

		// Undoing the exclusion gives it the vault back, with a full copy that has the tables.
		r.roster.set("common", false)
		r.primary.ExclusionChanged(guild.Machine{ID: "common", Excluded: false})
		settle()
		advance(time.Second)
		if !holds(common) || rows(common) != 3 {
			t.Fatalf("after undoing: holds=%v rows=%d, want the key, the envelope and 3 secrets", holds(common), rows(common))
		}
		equal(t, r, common)
	})
}

// CA-REPL-04: a machine that is excluded while it is away is told when it connects.
func TestAMachineExcludedWhileAwayErasesTheVaultWhenItConnects(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{vault: true})
		ctx := context.Background()
		if err := r.vault.Put(ctx, "k", vault.TypeProviderAPIKey, []byte("sk-ant-api03-Zx8Cv7Bn6Mm5Ll4Kk3Jj2Hh1"), nil); err != nil {
			t.Fatal(err)
		}
		tw := r.addTower("t1", true)
		settle()
		cp := &vault.Copier{Keys: tw.keys, EnvelopePath: tw.env}
		if !cp.Holds(ctx) {
			t.Fatal("setup: the Tower should hold the vault")
		}
		tw.stop()
		r.roster.set("t1", true)
		tw.start(true)
		settle()
		if cp.Holds(ctx) {
			t.Error("the Tower still holds the vault")
		}
		var n int
		_ = tw.db.Reader().QueryRow(`SELECT count(*) FROM secrets`).Scan(&n)
		if n != 0 {
			t.Errorf("%d secrets left", n)
		}
	})
}

// CA-REPL-05
func TestATowerRefusesWritesAndTheVaultOutsideTheApplyOfTheRecords(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{vault: true})
		tw := r.addTower("t1", true)
		settle()
		ctx := context.Background()
		for _, q := range []string{
			`INSERT INTO machines (machine_id, name, instance, priority, excluded, state, names, joined_at) VALUES ('x', 'x', '', 0, 0, 'active', '{}', 0)`,
			`DELETE FROM machines`,
			`UPDATE guild SET epoch = 99`,
		} {
			if _, err := tw.db.Writer().ExecContext(ctx, q); err == nil {
				t.Errorf("%q worked on the replica of a Tower", q)
			}
		}
		// It is also so while the replication is applying, and after.
		r.insertMachine(1)
		settle()
		if _, err := tw.db.Writer().ExecContext(ctx, `DELETE FROM machines`); err == nil {
			t.Error("a write worked on the replica after an apply")
		}
		equal(t, r, tw)
		// The vault does not open on a replica.
		_, err := vault.Open(ctx, vault.Config{DB: tw.db, Keys: tw.keys, EnvelopePath: tw.env, RecoveryPublicKey: bytes.Repeat([]byte{1}, 32), Masker: logging.NewMasker()})
		if !errors.Is(err, vault.ErrReplica) {
			t.Errorf("vault.Open on a replica = %v, want ErrReplica", err)
		}
		_, err = vault.Create(ctx, vault.Config{DB: tw.db, Keys: &memKeys{}, EnvelopePath: tw.env, RecoveryPublicKey: bytes.Repeat([]byte{1}, 32), Masker: logging.NewMasker()})
		if !errors.Is(err, vault.ErrReplica) {
			t.Errorf("vault.Create on a replica = %v, want ErrReplica", err)
		}
	})
}

// CA-REPL-06 (the lag and the alert; the checklist is milestone 7)
func TestALateTowerIsAnAlertAfterAMinuteOverTheThreshold(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{})
		ctx := context.Background()
		late := r.addTower("late", false) // connected, but applies and confirms nothing
		fine := r.addTower("fine", true)
		settle()
		r.insertMachine(1)
		settle()

		advance(4 * time.Second)
		if a, _ := r.primary.LagAlerts(ctx); len(a) != 0 {
			t.Fatalf("alerts at 4 s of lag: %+v", a)
		}
		advance(2 * time.Second) // 6 s
		if st := statusOf(t, r, "late"); st.Lag < 6*time.Second || st.Behind != 1 {
			t.Fatalf("late: %+v, want 6 s and 1 record behind", st)
		}
		if st := statusOf(t, r, "fine"); st.Lag != 0 || st.Behind != 0 {
			t.Fatalf("fine: %+v", st)
		}
		if a, _ := r.primary.LagAlerts(ctx); len(a) != 0 {
			t.Fatalf("alerts the first time it is over the threshold: %+v", a)
		}
		advance(59 * time.Second)
		if a, _ := r.primary.LagAlerts(ctx); len(a) != 0 {
			t.Fatalf("alerts before a minute: %+v", a)
		}
		advance(2 * time.Second)
		alerts, err := r.primary.LagAlerts(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(alerts) != 1 || alerts[0].Machine != "late" || alerts[0].Lag < 6*time.Second {
			t.Fatalf("alerts = %+v, want one for the late Tower", alerts)
		}
		// It catches up and the alert goes away.
		late.stop()
		late.start(true)
		settle()
		advance(time.Second)
		if a, _ := r.primary.LagAlerts(ctx); len(a) != 0 {
			t.Errorf("alerts after catching up: %+v", a)
		}
		_ = fine
	})
}

// CA-REPL-10: the new Citadel re-envelopes with only the public recovery key.
func TestAfterAPromotionTheKeyIsChangedWithoutThePrivateRecoveryKeyAndTheEnvelopeOpens(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{vault: true})
		ctx := context.Background()
		secret := []byte("sk-ant-api03-Zx8Cv7Bn6Mm5Ll4Kk3Jj2Hh1")
		if err := r.vault.Put(ctx, "provider_claude_api", vault.TypeProviderAPIKey, secret, nil); err != nil {
			t.Fatal(err)
		}
		tw := r.addTower("t1", true)
		settle()
		rk := r.rk
		oldEnvelope, _ := os.ReadFile(tw.env)

		// The Citadel is gone and the Tower is promoted: its file becomes the
		// main database (no copy), and the vault opens from its own copy.
		tw.stop()
		r.stop()
		promoted, err := guilddb.Open(ctx, tw.dbPath, store.Options{Synchronous: "OFF", Clock: r.clk})
		if err != nil {
			t.Fatal(err)
		}
		defer promoted.Close()
		var pub []byte
		if err := promoted.Reader().QueryRow(`SELECT recovery_public_key FROM guild`).Scan(&pub); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(pub, bytes.Repeat([]byte{7}, 32)) {
			t.Fatal("setup: the Guild keeps the public key")
		}
		// The Guild of this test keeps the public key of the real recovery key.
		if _, err := promoted.Writer().Exec(`UPDATE guild SET recovery_public_key = ?`, rk.PublicKey()); err != nil {
			t.Fatal(err)
		}
		v, err := vault.Open(ctx, vault.Config{
			DB: promoted, Keys: tw.keys, EnvelopePath: tw.env, RecoveryPublicKey: rk.PublicKey(), Masker: logging.NewMasker(), Clock: r.clk,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := v.Err(); err != nil {
			t.Fatalf("the promoted Tower cannot open the vault from its copy: %v", err)
		}
		if err := v.Rotate(ctx); err != nil { // only the public key is used
			t.Fatal(err)
		}
		newEnvelope, _ := os.ReadFile(tw.env)
		if bytes.Equal(oldEnvelope, newEnvelope) {
			t.Fatal("the envelope did not change with the key")
		}
		// The envelope opens with the recovery key, and the secrets read with what it gives.
		restored := &memKeys{}
		if err := vault.RestoreKey(ctx, newEnvelope, rk, restored); err != nil {
			t.Fatalf("the new envelope does not open with the recovery key: %v", err)
		}
		v2, err := vault.Open(ctx, vault.Config{
			DB: promoted, Keys: restored, EnvelopePath: filepath.Join(t.TempDir(), "e.bin"), RecoveryPublicKey: rk.PublicKey(), Masker: logging.NewMasker(), Clock: r.clk,
		})
		if err != nil || v2.Err() != nil {
			t.Fatalf("open with the restored key: %v / %v", err, v2.Err())
		}
		var got []byte
		if err := v2.Use(ctx, "provider_claude_api", "test", func(b []byte) error { got = append(got, b...); return nil }); err != nil || !bytes.Equal(got, secret) {
			t.Fatalf("the secret read with the restored key = %q, %v", got, err)
		}
	})
}

// After the Citadel changes the key, the Towers get the vault again.
func TestWhenTheKeyIsChangedTheTowersReceiveTheNewVault(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{vault: true})
		ctx := context.Background()
		if err := r.vault.Put(ctx, "k", vault.TypeProviderAPIKey, []byte("sk-ant-api03-Zx8Cv7Bn6Mm5Ll4Kk3Jj2Hh1"), nil); err != nil {
			t.Fatal(err)
		}
		tw := r.addTower("t1", true)
		settle()
		cp := &vault.Copier{Keys: tw.keys, EnvelopePath: tw.env}
		before, err := cp.Export(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.vault.Rotate(ctx); err != nil {
			t.Fatal(err)
		}
		r.primary.VaultChanged(ctx)
		settle()
		after, err := cp.Export(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := (&vault.Copier{Keys: r.ckeys, EnvelopePath: r.envelope}).Export(ctx)
		if bytes.Equal(before.Keyring, after.Keyring) || !bytes.Equal(after.Keyring, want.Keyring) || !bytes.Equal(after.Envelope, want.Envelope) {
			t.Fatal("the Tower does not have the new key and envelope of the Citadel")
		}
		equal(t, r, tw)
	})
}

// CA-REPL-08, the part of milestone 2: a file that a record cites is on the
// Tower before the record is applied there.
func TestAFileThatARecordCitesArrivesBeforeTheRecord(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		cites := func(c store.Change) []replication.FileRef {
			if c.Table == "machines" && c.Op == store.OpInsert {
				return []replication.FileRef{{Root: "classes", Path: c.New[1].(string)}} // the name column stands for a path
			}
			return nil
		}
		r := newRig(t, rigOpts{files: true, cites: cites})
		tw := r.addTower("t1", true)
		settle()

		write := func(rel, body string) {
			p := filepath.Join(r.filesDir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o640); err != nil {
				t.Fatal(err)
			}
		}
		write("qa/classe.md", "# classe QA\n")
		write("dev/classe.md", "# classe Dev\n")
		r.exec(`INSERT INTO machines (machine_id, name, instance, priority, excluded, state, names, joined_at) VALUES ('m1', 'qa/classe.md', '', 0, 0, 'active', '{}', 0)`)
		lsn1 := r.lsn()
		r.exec(`INSERT INTO machines (machine_id, name, instance, priority, excluded, state, names, joined_at) VALUES ('m2', 'dev/classe.md', '', 0, 0, 'active', '{}', 0)`)
		lsn2 := r.lsn()
		settle()

		for rel, body := range map[string]string{"qa/classe.md": "# classe QA\n", "dev/classe.md": "# classe Dev\n"} {
			got, err := os.ReadFile(filepath.Join(tw.files, filepath.FromSlash(rel)))
			if err != nil || string(got) != body {
				t.Errorf("%s on the Tower = %q, %v", rel, got, err)
			}
		}
		equal(t, r, tw)
		ev := tw.log()
		if len(ev) < 3 || ev[0] != "snapshot" {
			t.Fatalf("events = %v", ev)
		}
		// Before each record that cites a file, the file: the Citadel sends the
		// record only after the transfer ended.
		for _, c := range []struct {
			file string
			lsn  uint64
		}{{"qa/classe.md", lsn1}, {"dev/classe.md", lsn2}} {
			f, a := indexOf(ev, "file "+c.file), appliedAt(ev, c.lsn)
			if f < 0 || a < 0 || f > a {
				t.Fatalf("%s arrived at event %d and the record %d that cites it was applied at %d; events = %v", c.file, f, c.lsn, a, ev)
			}
		}

		// A file that did not change is not sent again.
		n := countPrefix(tw.log(), "file ")
		r.exec(`UPDATE machines SET priority = 5 WHERE machine_id = 'm1'`)
		settle()
		if got := countPrefix(tw.log(), "file "); got != n {
			t.Errorf("files received grew from %d to %d: an update that cites no new file sent one", n, got)
		}
		// A new content for the same path is sent.
		write("qa/classe.md", "# classe QA 2\n")
		r.exec(`INSERT INTO machines (machine_id, name, instance, priority, excluded, state, names, joined_at) VALUES ('m3', 'qa/classe.md', '', 0, 0, 'active', '{}', 0)`)
		settle()
		if got, _ := os.ReadFile(filepath.Join(tw.files, "qa", "classe.md")); string(got) != "# classe QA 2\n" {
			t.Errorf("the changed file on the Tower = %q", got)
		}
	})
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

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

func TestTheFilesOfTheFoldersMarkedToWalkReachANewTowerAndHostilePathsAreRefused(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{files: true})
		for rel, body := range map[string]string{"a.md": "a", "dir/b.md": "b", "dir/deep/c.md": "c"} {
			p := filepath.Join(r.filesDir, filepath.FromSlash(rel))
			_ = os.MkdirAll(filepath.Dir(p), 0o750)
			if err := os.WriteFile(p, []byte(body), 0o640); err != nil {
				t.Fatal(err)
			}
		}
		tw := r.addTower("t1", true)
		settle()
		for rel, body := range map[string]string{"a.md": "a", "dir/b.md": "b", "dir/deep/c.md": "c"} {
			got, err := os.ReadFile(filepath.Join(tw.files, filepath.FromSlash(rel)))
			if err != nil || string(got) != body {
				t.Errorf("%s = %q, %v", rel, got, err)
			}
		}
		// A reconnection sends nothing again: the Tower has the content.
		n := len(tw.log())
		tw.stop()
		tw.start(true)
		settle()
		for _, e := range tw.log()[n:] {
			if strings.HasPrefix(e, "file ") {
				t.Errorf("sent again after reconnecting: %s", e)
			}
		}
		// A file that appears later is sent when the Citadel says so.
		if err := os.WriteFile(filepath.Join(r.filesDir, "new.md"), []byte("new"), 0o640); err != nil {
			t.Fatal(err)
		}
		r.primary.FileChanged("classes", "new.md")
		settle()
		if got, _ := os.ReadFile(filepath.Join(tw.files, "new.md")); string(got) != "new" {
			t.Errorf("new.md on the Tower = %q", got)
		}
		// A path that leaves the folder is refused, on either side.
		r.primary.FileChanged("classes", "../escape.md")
		settle()
		if _, err := os.Stat(filepath.Join(filepath.Dir(tw.files), "escape.md")); err == nil {
			t.Error("a file escaped the folder")
		}
	})
}

// A Tower that leaves the Guild erases its share: vault, replica, files.
func TestLeavingTheGuildErasesTheShareOfTheMachine(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{vault: true, files: true})
		ctx := context.Background()
		if err := r.vault.Put(ctx, "k", vault.TypeProviderAPIKey, []byte("sk-ant-api03-Zx8Cv7Bn6Mm5Ll4Kk3Jj2Hh1"), nil); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(r.filesDir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(r.filesDir, "a.md"), []byte("a"), 0o640); err != nil {
			t.Fatal(err)
		}
		tw := r.addTower("t1", true)
		settle()
		if _, err := os.Stat(filepath.Join(tw.files, "a.md")); err != nil {
			t.Fatal("setup: the file should be on the Tower")
		}
		var w guild.Wiper = tw.rep
		tw.cancel()
		tw.wg.Wait()
		if err := w.Wipe(ctx, "removed"); err != nil {
			t.Fatal(err)
		}
		tw.cancel = nil
		for _, p := range []string{tw.dbPath, tw.env, filepath.Join(tw.files, "a.md")} {
			if _, err := os.Stat(p); err == nil {
				t.Errorf("%s is still there", p)
			}
		}
		if (&vault.Copier{Keys: tw.keys, EnvelopePath: tw.env}).Holds(ctx) {
			t.Error("the key is still there")
		}
	})
}
