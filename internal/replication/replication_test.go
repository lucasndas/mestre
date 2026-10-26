package replication_test

import (
	"bytes"
	"context"
	"os"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mestre-ia/mestre/internal/replication"
	"github.com/mestre-ia/mestre/internal/vault"
)

// A rig costs files (a database for the Citadel, one for each Tower, the full
// copies that travel between them), and on a Windows runner files are what the
// fast suite pays for. So each test below is a story told on one rig, with the
// criteria it covers named above it, not a rig per sentence.

// equal waits for the Tower to catch up and compares the tables.
func equal(t *testing.T, r *rig, tw *tower, skip ...string) {
	t.Helper()
	eventually(t, tw.id+" to catch up", func() bool { return tw.lsn() == r.lsn() })
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

func snapshots(tw *tower, n int32) func() bool {
	return func() bool { return tw.snapshots.Load() == n }
}

// Part of CA-REPL-01, CA-REPL-02 and CA-REPL-05 (the story with a thousand
// transactions, the lost frame and the record that does not fit is in the full
// suite): a Tower just accepted gets a full copy equal to the primary, the
// records that follow arrive in order and are acked, and nothing writes to the
// replica but the replication.
func TestATowerGetsTheFullCopyThenTheRecordsInOrderAndTakesNoOtherWrite(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{})
		ctx := context.Background()
		for i := range 5 {
			r.insertMachine(i)
		}
		tw := r.addTower("t1", true)
		eventually(t, "the full copy", snapshots(tw, 1))
		equal(t, r, tw)

		start := r.lsn()
		for i := range 20 {
			r.insertMachine(100 + i)
		}
		if r.lsn() != start+20 {
			t.Fatalf("the primary is at %d, want %d", r.lsn(), start+20)
		}
		equal(t, r, tw)
		frames := r.framesTo("t1")
		for i := 1; i < len(frames); i++ {
			if frames[i] <= frames[i-1] {
				t.Fatalf("frames out of order: %v", frames)
			}
		}
		eventuallyFor(t, "the last ack", 100*time.Millisecond, 60, func() bool { return statusOf(t, r, "t1").Acked == r.lsn() })

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

// Part of CA-REPL-04 and CA-REPL-10 (the story with the exclusion, the undone
// exclusion and the promoted Tower is in the full suite): the vault reaches the
// common machine and not the excluded one, a secret written later too, and the
// key changed reaches the common one.
func TestTheVaultReachesTheCommonMachineAndNotTheExcludedOne(t *testing.T) {
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
		equal(t, r, common)
		equal(t, r, excluded, "secrets", "site_login_attempts")
		if rows(common) != 1 {
			t.Fatalf("the common machine has %d secrets, want 1", rows(common))
		}
		if _, err := os.Stat(excluded.env); err == nil || holds(excluded) || rows(excluded) != 0 {
			t.Fatalf("the excluded machine received vault: holds=%v rows=%d", holds(excluded), rows(excluded))
		}
		if b, _ := os.ReadFile(excluded.dbPath); bytes.Contains(b, ciphertext) {
			t.Error("the file of the excluded replica contains the secret")
		}

		// A secret written now reaches the common machine and not the excluded one,
		// and so does the new key.
		put("later", "sk-ant-api03-Aa1Bb2Cc3Dd4Ee5Ff6")
		eventually(t, "the new secret", func() bool { return rows(common) == 2 })
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
		equal(t, r, excluded, "secrets", "site_login_attempts")
		if rows(excluded) != 0 || holds(excluded) {
			t.Fatalf("the excluded machine got the vault: %d secrets", rows(excluded))
		}
	})
}

// A new recovery key: the connected Tower gets the new envelope as soon as the panel
// tells the Primary the vault changed, not at its next connection.
func TestAConnectedTowerGetsTheEnvelopeOfANewRecoveryKey(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{vault: true, durable: true})
		ctx := context.Background()
		common := r.addTower("common", true)
		holds := func() bool { return (&vault.Copier{Keys: common.keys, EnvelopePath: common.env}).Holds(ctx) }
		eventually(t, "the copy", func() bool { return common.lsn() == r.lsn() && holds() })
		before, _ := (&vault.Copier{Keys: common.keys, EnvelopePath: common.env}).Export(ctx)
		fresh, err := vault.NewRecoveryKey()
		if err != nil {
			t.Fatal(err)
		}
		if err := r.vault.RewrapRecovery(ctx, fresh.PublicKey()); err != nil {
			t.Fatal(err)
		}
		r.primary.VaultChanged(ctx)
		want, _ := (&vault.Copier{Keys: r.ckeys, EnvelopePath: r.envelope}).Export(ctx)
		if bytes.Equal(before.Envelope, want.Envelope) {
			t.Fatal("the envelope did not change")
		}
		eventually(t, "the new envelope", func() bool {
			got, err := (&vault.Copier{Keys: common.keys, EnvelopePath: common.env}).Export(ctx)
			return err == nil && bytes.Equal(got.Envelope, want.Envelope)
		})
	})
}

// The rule of the confirmations: at once when 256 records are waiting, otherwise every second.
func TestARecordIsAckedAtOnceWhen256AreWaitingAndOtherwiseEverySecond(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{})
		r.ackEvery["t1"] = time.Hour // only the 256 rule can confirm
		tw := r.addTower("t1", true)
		eventually(t, "the full copy", snapshots(tw, 1))
		base := r.lsn()
		eventually(t, "the first ack", func() bool { return statusOf(t, r, "t1").Acked == base })
		for i := range 255 {
			r.insertMachine(i)
		}
		eventually(t, "the 255 records", func() bool { return tw.lsn() == base+255 })
		if got := statusOf(t, r, "t1").Acked; got != base {
			t.Fatalf("acked %d with fewer than 256 records waiting, want %d", got, base)
		}
		r.insertMachine(255)
		eventually(t, "the ack of 256", func() bool { return statusOf(t, r, "t1").Acked == base+256 })
	})
}

// CA-REPL-03: back within the retention, only the records that are missing; back
// after it, or on another epoch, a full copy.
func TestATowerThatComesBackGetsOnlyWhatIsMissingOrAFullCopyAfterTheRetention(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{lagThreshold: 24 * time.Hour})
		for i := range 10 {
			r.insertMachine(i)
		}
		tw := r.addTower("t1", true)
		eventually(t, "the full copy", snapshots(tw, 1))
		equal(t, r, tw)
		had := tw.lsn()

		tw.stop()
		for i := 10; i < 40; i++ {
			r.insertMachine(i)
		}
		advance(10 * time.Minute) // inside the hour
		before := len(r.framesTo("t1"))
		tw.start(true)
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
		eventually(t, "the second full copy", snapshots(tw, 2))
		equal(t, r, tw)
	})
}

// Part of CA-REPL-06 (fecha no M7, com o checklist que lê o alerta): the alert
// of the late Tower, the part of milestone 2
func TestALateTowerIsAnAlertAfterAMinuteOverTheThreshold(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{})
		ctx := context.Background()
		late := r.addTower("late", false) // connected, but applies and confirms nothing
		eventually(t, "the connection", func() bool {
			st, _ := r.primary.Status(ctx)
			return len(st) == 1
		})
		r.insertMachine(1)
		settle()

		advance(4 * time.Second)
		if a, _ := r.primary.LagAlerts(ctx); len(a) != 0 {
			t.Fatalf("alerts at 4 s of lag: %+v", a)
		}
		advance(2 * time.Second) // 6 s
		if st := statusOf(t, r, "late"); st.Lag < 6*time.Second || st.Behind == 0 {
			t.Fatalf("late: %+v, want 6 s and records behind", st)
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
		eventually(t, "the alert to go", func() bool {
			a, _ := r.primary.LagAlerts(ctx)
			return len(a) == 0 && late.lsn() == r.lsn()
		})
	})
}
