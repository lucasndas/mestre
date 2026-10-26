package replication_test

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/replication"
	"github.com/mestre-ia/mestre/internal/vault"
)

// Part of CA-REPL-04 (the story with two Towers, one finished by the order again
// and one by the retry of the replica, is in the full suite): an erasing that
// could not truncate the WAL (a reader holds it) is not forgotten; no receipt
// goes while the secrets may still be in the -wal file, and the next order to
// erase finishes it and sends the receipt.
func TestAnIncompleteErasingIsOwedAndNoReceiptGoesBeforeItIsFinished(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{vault: true, busy: 20 * time.Millisecond})
		ctx := context.Background()
		if err := r.vault.Put(ctx, "k", vault.TypeProviderAPIKey, []byte("sk-ant-api03-Zx8Cv7Bn6Mm5Ll4Kk3Jj2Hh1"), nil); err != nil {
			t.Fatal(err)
		}
		a := r.addTower("a", true)
		eventually(t, "the copy", func() bool { return a.lsn() == r.lsn() })
		audited := func() int {
			var n int
			if err := r.cdb.Reader().QueryRow(`SELECT count(*) FROM audit_log WHERE action = ? AND machine_id = 'a'`, replication.AuditVaultDropped).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		owed := func() bool {
			raw, err := os.ReadFile(a.dbPath + ".excluded")
			return err == nil && strings.Contains(string(raw), "purge incomplete")
		}

		// A reader holds the WAL of the replica while the order arrives.
		reader, err := a.db.Reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		var n int
		if err := reader.QueryRowContext(ctx, `SELECT count(*) FROM secrets`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		r.roster.set("a", true)
		r.primary.ExclusionChanged(guild.Machine{ID: "a", Excluded: true})
		eventually(t, "the incomplete state", owed)
		settle()
		if audited() != 0 {
			t.Fatal("a receipt went while the WAL still held what was erased")
		}

		// The reader is gone and the Citadel gives the order again (it does at every
		// connection of an excluded machine).
		if err := reader.Rollback(); err != nil {
			t.Fatal(err)
		}
		r.primary.ExclusionChanged(guild.Machine{ID: "a", Excluded: true})
		eventually(t, "the receipt", func() bool { return audited() == 1 })
		if owed() {
			t.Error("a finished erasing is still owed")
		}
	})
}
