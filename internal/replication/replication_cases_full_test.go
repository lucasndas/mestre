//go:build full

package replication_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/lucasndas/mestre/internal/vault"
)

// Cases of the replication that the fast suite leaves to the full one: each
// costs database files and full copies, and on a Windows runner that is what
// the 30 s of the fast suite are spent on.

// A full copy goes when a record is bigger than a frame and when the epoch of
// the Tower is not the one of the Citadel (a promotion happened meanwhile).
func TestAFullCopyIsSentWhenARecordIsBiggerThanAFrameAndWhenTheEpochChanges(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{})
		for i := range 5 {
			r.insertMachine(i)
		}
		tw := r.addTower("t1", true)
		eventually(t, "the full copy", snapshots(tw, 1))
		equal(t, r, tw)

		r.exec(`UPDATE machines SET sheet = ? WHERE machine_id = 'm0002'`, strings.Repeat("x", 1200*1024))
		eventually(t, "the second full copy", snapshots(tw, 2))
		r.insertMachine(100)
		equal(t, r, tw)

		tw.stop()
		r.exec(`UPDATE guild SET epoch = 2`)
		r.insertMachine(200)
		tw.start(true)
		eventually(t, "the third full copy", snapshots(tw, 3))
		equal(t, r, tw)
	})
}

// A machine excluded while it was away erases the vault when it connects, and
// gets it back, with a full copy that has the rows, when the exclusion is
// undone while it is away (the exclusion itself, with the machine connected,
// is criterion 04 of the replication, in the fast suite).
func TestAMachineExcludedWhileAwayErasesTheVaultWhenItConnectsAndGetsItBackWhenTheExclusionIsUndone(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{vault: true})
		ctx := context.Background()
		if err := r.vault.Put(ctx, "k", vault.TypeProviderAPIKey, []byte("sk-ant-api03-Zx8Cv7Bn6Mm5Ll4Kk3Jj2Hh1"), nil); err != nil {
			t.Fatal(err)
		}
		tw := r.addTower("t1", true)
		cp := &vault.Copier{Keys: tw.keys, EnvelopePath: tw.env}
		rows := func() int {
			var n int
			_ = tw.db.Reader().QueryRow(`SELECT count(*) FROM secrets`).Scan(&n)
			return n
		}
		eventually(t, "the vault", func() bool { return cp.Holds(ctx) && rows() == 1 && tw.lsn() == r.lsn() })

		tw.stop()
		r.roster.set("t1", true)
		tw.start(true)
		eventually(t, "the vault to be erased on the connection", func() bool { return !cp.Holds(ctx) && rows() == 0 })
		if _, err := os.Stat(tw.env); err == nil {
			t.Error("the envelope is still there")
		}

		tw.stop()
		r.roster.set("t1", false)
		tw.start(true)
		eventually(t, "the vault to come back on the connection", func() bool { return cp.Holds(ctx) && rows() == 1 && tw.lsn() == r.lsn() })
		equal(t, r, tw)
	})
}
