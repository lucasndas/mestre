package replication_test

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/replication"
	"github.com/lucasndas/mestre/internal/vault"
)

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
