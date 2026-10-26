package replication_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/replication"
	"github.com/mestre-ia/mestre/internal/vault"
)

// diverge changes the replica behind the replication's back, as a bug or a
// restored backup would: the next record that touches the row does not fit.
func diverge(t *testing.T, tw *tower, machine string) {
	t.Helper()
	raw, err := sql.Open("sqlite", tw.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`DELETE FROM machines WHERE machine_id = ?`, machine); err != nil {
		t.Fatal(err)
	}
}

// CA-REPL-03
// A Tower that asked for a full copy and never got the answer asks again; one
// that has to reconnect says in the hello that it has nothing, and gets the
// copy at once.
func TestATowerWaitingForAFullCopyIsNotStuck(t *testing.T) {
	t.Parallel()
	t.Run("the request is lost", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t, rigOpts{lagThreshold: 24 * time.Hour})
			for i := range 5 {
				r.insertMachine(i)
			}
			tw := r.addTower("t1", true)
			eventually(t, "the full copy", snapshots(tw, 1))
			equal(t, r, tw)

			diverge(t, tw, "m0002")
			r.dropResyncs.Store(1)
			r.exec(`UPDATE machines SET priority = 9 WHERE machine_id = 'm0002'`)
			eventually(t, "the request for a copy", func() bool { return r.resyncs.Load() == 1 })
			advance(10 * time.Second)
			if st := tw.rep.Status(); st.State != "waiting-copy" || tw.snapshots.Load() != 1 {
				t.Fatalf("status %+v, snapshots %d: the request was lost, so no copy yet", st, tw.snapshots.Load())
			}
			// The request is made again after a while.
			eventuallyFor(t, "the second full copy", time.Second, int((2*replication.DefaultResyncRetry)/time.Second), snapshots(tw, 2))
			if got := r.resyncs.Load(); got != 2 {
				t.Errorf("%d requests, want the lost one and one more", got)
			}
			equal(t, r, tw)
			if st := tw.rep.Status(); st.State != "ok" {
				t.Errorf("status = %+v", st)
			}
		})
	})
	t.Run("the connection is lost", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t, rigOpts{lagThreshold: 24 * time.Hour})
			for i := range 5 {
				r.insertMachine(i)
			}
			tw := r.addTower("t1", true)
			eventually(t, "the full copy", snapshots(tw, 1))
			diverge(t, tw, "m0002")
			r.dropResyncs.Store(100)
			r.exec(`UPDATE machines SET priority = 9 WHERE machine_id = 'm0002'`)
			eventually(t, "the request for a copy", func() bool { return r.resyncs.Load() == 1 })
			if tw.snapshots.Load() != 1 {
				t.Fatal("the copy came, the request was meant to be lost")
			}
			// The Tower reconnects well before it would ask again: its hello says
			// it has no replica, which is what it is worth until the copy comes.
			r.net.Cut("t1", "citadel")
			eventuallyFor(t, "the copy after the reconnection", time.Second, int(replication.DefaultResyncRetry/time.Second)-5, snapshots(tw, 2))
			equal(t, r, tw)
			if got := r.resyncsDelivered.Load(); got != 0 {
				t.Errorf("%d requests got through: the hello alone should have been enough", got)
			}
		})
	})
}

// CA-REPL-04
// The receipt of vault.drop goes in the audit of the Guild, once, and only from
// a machine that is excluded (CA-REPL-04).
func TestTheReceiptOfVaultDropIsAudited(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{vault: true})
		ctx := context.Background()
		if err := r.vault.Put(ctx, "k", vault.TypeProviderAPIKey, []byte("sk-ant-api03-Zx8Cv7Bn6Mm5Ll4Kk3Jj2Hh1"), nil); err != nil {
			t.Fatal(err)
		}
		a := r.addTower("a", true)
		b := r.addTower("b", true)
		cp := &vault.Copier{Keys: a.keys, EnvelopePath: a.env}
		eventually(t, "both copies", func() bool { return a.lsn() == r.lsn() && b.lsn() == r.lsn() && cp.Holds(ctx) })
		audited := func() []string {
			rows, err := r.cdb.Reader().Query(`SELECT machine_id FROM audit_log WHERE action = ? ORDER BY at, id`, replication.AuditVaultDropped)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var out []string
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					t.Fatal(err)
				}
				out = append(out, id)
			}
			return out
		}

		// A machine that is not excluded claims it erased the vault.
		if _, err := b.client.Link().Send(ctx, protocol.Outgoing{Type: protocol.TypeVaultDropped, Payload: protocol.Empty{}}); err != nil {
			t.Fatal(err)
		}
		settle()
		if got := audited(); len(got) != 0 {
			t.Fatalf("audited %v: a receipt from a machine that is not excluded", got)
		}
		if got := r.receipts(); len(got) != 0 {
			t.Fatalf("receipts = %v", got)
		}

		r.roster.set("a", true)
		r.primary.ExclusionChanged(guild.Machine{ID: "a", Excluded: true})
		eventually(t, "the receipt", func() bool { return len(audited()) == 1 })
		if got := audited(); got[0] != "a" {
			t.Errorf("audited %v, want a", got)
		}
		if got := r.receipts(); len(got) != 1 || got[0] != "a" {
			t.Errorf("OnDropped got %v", got)
		}
		var detail string
		if err := r.cdb.Reader().QueryRow(`SELECT detail FROM audit_log WHERE action = ?`, replication.AuditVaultDropped).Scan(&detail); err != nil || strings.Contains(detail, "sk-") {
			t.Errorf("detail = %q, %v", detail, err)
		}
	})
}
