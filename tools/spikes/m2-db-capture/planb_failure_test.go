//go:build spike

package dbcapture

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// planBHelper runs the plan B modes of TestHelperProcess.
func planBHelper(t *testing.T, mode string, block func(string)) bool {
	ctx := context.Background()
	path := os.Getenv("MESTRE_SPIKE_DB")
	switch mode {
	case "planb-primary-crash":
		p, err := OpenLogPrimary(ctx, path, migrations(t, 1), store0())
		if err != nil {
			t.Fatal(err)
		}
		p.DB.Writer().Exec(`PRAGMA cache_size = 20`)
		for i := range 20 {
			if err := p.Write(ctx, func(tx *sql.Tx) error {
				_, err := tx.Exec(`UPDATE accounts SET balance = balance + 1 WHERE id = ?`, i%10+1)
				return err
			}); err != nil {
				t.Fatal(err)
			}
		}
		p.Write(ctx, func(tx *sql.Tx) error {
			for i := range 3000 {
				tx.Exec(`INSERT INTO items(name, payload, created_at) VALUES(?, randomblob(500), 0)`, fmt.Sprintf("uncommitted-%d", i))
			}
			block(fmt.Sprintf("SPILLED %d pending changes", len(p.pending)))
			return nil
		})
	case "planb-replica-crash":
		r, err := OpenLogReplica(ctx, path, migrations(t, 2), store0())
		if err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(os.Getenv("MESTRE_SPIKE_RECORD"))
		rec, err := DecodeLog(b)
		if err != nil {
			t.Fatal(err)
		}
		r.BeforeCommit = func() { block("BEFORE-COMMIT") }
		r.Apply(ctx, rec)
	default:
		return false
	}
	return true
}

// Q6 / Q5 for the plan B: the primary dies mid-transaction (the log rows
// die with it), the replica dies mid-apply (its lsn is in the same
// transaction), a lost record is refused.
func TestPlanBFailures(t *testing.T) {
	lp := newLogPair(t)
	ctx := lp.ctx
	lp.write(func(tx *sql.Tx) error {
		for i := 1; i <= 10; i++ {
			if _, err := tx.Exec(`INSERT INTO accounts(id, owner, balance, updated_at) VALUES(?, 'x', 0, 0)`, i); err != nil {
				return err
			}
		}
		return nil
	})
	path := lp.p.DB.Path()
	lp.p.DB.Close()
	line := runUntil(t, "MESTRE_SPIKE_HELPER=planb-primary-crash", "MESTRE_SPIKE_DB="+path)
	p2, err := OpenLogPrimary(ctx, path, migrations(t, 1), store0())
	if err != nil {
		t.Fatal(err)
	}
	lp.p = p2
	p2.Ship = lp.ship
	var unc, logged int
	p2.DB.Reader().QueryRow(`SELECT count(*) FROM items WHERE name LIKE 'uncommitted-%'`).Scan(&unc)
	p2.DB.Reader().QueryRow(`SELECT count(*) FROM replication_log`).Scan(&logged)
	t.Logf("primary crash: child %q; after restart lsn %d, %d log rows, %d uncommitted rows", line, p2.LSN, logged, unc)
	if p2.LSN != 21 || unc != 0 {
		t.Fatal("unexpected primary state")
	}
	// The Towers catch up from the log table (what a reconnect does).
	rows, _ := p2.DB.Reader().Query(`SELECT body FROM replication_log WHERE lsn > ? ORDER BY lsn`, lp.r.LSN)
	for rows.Next() {
		var b []byte
		rows.Scan(&b)
		rec, _ := DecodeLog(b)
		lp.ship(rec)
	}
	rows.Close()
	lp.assertSame("plan B after primary crash")

	// Replica crash before commit.
	lp.applyOn = false
	lp.write(func(tx *sql.Tx) error {
		for i := range 1000 {
			if _, err := tx.Exec(`INSERT INTO items(name, payload, created_at) VALUES(?, randomblob(200), 0)`, fmt.Sprintf("b%d", i)); err != nil {
				return err
			}
		}
		return nil
	})
	lp.applyOn = true
	rec := lp.shipped[len(lp.shipped)-1]
	recFile := filepath.Join(lp.dir, "rec.gob")
	os.WriteFile(recFile, EncodeLog(rec), 0o600)
	rpath := lp.r.DB.Path()
	lp.r.DB.Close()
	line = runUntil(t, "MESTRE_SPIKE_HELPER=planb-replica-crash", "MESTRE_SPIKE_DB="+rpath, "MESTRE_SPIKE_RECORD="+recFile)
	r2, err := OpenLogReplica(ctx, rpath, migrations(t, 2), store0())
	if err != nil {
		t.Fatal(err)
	}
	lp.r = r2
	ic, _ := IntegrityCheck(ctx, r2.DB.Reader())
	var n int
	r2.DB.Reader().QueryRow(`SELECT count(*) FROM items WHERE name LIKE 'b%'`).Scan(&n)
	t.Logf("replica crash: child %q; after restart integrity %s, lsn %d (record %d), batch rows %d", line, ic, r2.LSN, rec.LSN, n)
	if ic != "ok" || r2.LSN != rec.LSN-1 || n != 0 {
		t.Fatal("unexpected replica state")
	}
	if err := r2.Apply(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err := lp.x.Apply(ctx, rec); err != nil {
		t.Fatal(err)
	}
	lp.assertSame("plan B after replica crash and re-apply")

	// Lost in transit.
	lp.applyOn = false
	for i := range 2 {
		lp.write(func(tx *sql.Tx) error {
			_, err := tx.Exec(`UPDATE accounts SET balance = ? WHERE id = 1`, 100+i)
			return err
		})
	}
	lp.applyOn = true
	a, b := lp.shipped[len(lp.shipped)-2], lp.shipped[len(lp.shipped)-1]
	var ooo *ErrOutOfOrder
	if err := lp.r.Apply(ctx, b); !errors.As(err, &ooo) {
		t.Fatalf("gap not detected: %v", err)
	}
	t.Logf("plan B lost record: %v", ooo)
	for _, r := range []*LogReplica{lp.r, lp.x} {
		for _, rec := range []*LogRecord{a, b, a} {
			if err := r.Apply(ctx, rec); err != nil {
				t.Fatal(err)
			}
		}
	}
	lp.assertSame("plan B after asking again")
}
