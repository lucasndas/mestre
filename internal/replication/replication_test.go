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

// CA-REPL-01
// CA-REPL-02
// CA-REPL-05
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

		// A record bigger than a frame goes by full copy too.
		r.exec(`UPDATE machines SET sheet = ? WHERE machine_id = 'm0002'`, strings.Repeat("x", 1200*1024))
		eventually(t, "the third full copy", snapshots(tw, 3))
		r.insertMachine(2003)
		equal(t, r, tw)

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
		r := newRig(t, rigOpts{})
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

		// Another epoch (a promotion happened meanwhile): a full copy as well.
		tw.stop()
		r.exec(`UPDATE guild SET epoch = 2`)
		r.insertMachine(200)
		tw.start(true)
		eventually(t, "the third full copy", snapshots(tw, 3))
		equal(t, r, tw)
	})
}

// CA-REPL-01
// CA-REPL-04
// CA-REPL-05
// CA-REPL-10
func TestTheVaultReachesOnlyTheMachinesThatAreNotExcludedAndThePromotedTowerChangesTheKeyWithoutThePrivateRecoveryKey(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{vault: true})
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

		// A machine excluded while it was away erases the vault when it connects.
		common.stop()
		r.roster.set("common", true)
		common.start(true)
		eventually(t, "the vault to be erased on the connection", func() bool { return !holds(common) && rows(common) == 0 })
		common.stop()
		r.roster.set("common", false)
		common.start(true)
		eventually(t, "the vault to come back on the connection", func() bool { return holds(common) && rows(common) == 3 && common.lsn() == r.lsn() })

		// The Citadel is lost and the Tower is promoted: its file becomes the main
		// database, no copy, and the vault opens from its own copy. Changing the key
		// needs only the public recovery key, and the new envelope opens with the
		// recovery key.
		envelopeBefore, _ := os.ReadFile(common.env)
		common.stop()
		r.stop()
		promoted, err := guilddb.Open(ctx, common.dbPath, store.Options{Synchronous: "OFF", Clock: r.clk})
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

// CA-REPL-06, the part of milestone 2 (the checklist that reads the alert is
// milestone 7)
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

// CA-REPL-08, the part of milestone 2: a file that a record cites is on the
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
