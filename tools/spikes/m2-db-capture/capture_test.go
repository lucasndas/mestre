//go:build spike

package dbcapture

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Q1: capture after every commit while the read pools keep reading; Q2: the
// replica stays page-identical across a checkpoint, VACUUM and a migration.
func TestCaptureAndApply(t *testing.T) {
	t.Logf("platform %s", osInfo())
	pr := newPair(t, "")
	ctx := pr.ctx
	now := time.Now().UnixMilli()
	pr.write(func(tx *sql.Tx) error {
		for i := 1; i <= 100; i++ {
			if _, err := tx.Exec(`INSERT INTO accounts(id, owner, balance, updated_at) VALUES(?, ?, 1000, ?)`, i, fmt.Sprintf("u%03d", i), now); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`INSERT INTO secrets(id, ciphertext, updated_at) VALUES('ca', x'00112233', ?)`, now)
		return err
	})
	pr.assertSame("seed")

	// Readers on both sides check an invariant (the total balance) the whole
	// time: a torn read would break it.
	stop := make(chan struct{})
	var reads, bad, failed atomic.Int64
	var firstErr atomic.Value
	var wg sync.WaitGroup
	for _, db := range []*sql.DB{pr.p.DB.Reader(), pr.r.DB.Reader()} {
		for range 3 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					var total int64
					if err := db.QueryRowContext(ctx, `SELECT coalesce(sum(balance),0) FROM accounts`).Scan(&total); err != nil {
						if !errors.Is(err, context.Canceled) {
							failed.Add(1)
							firstErr.CompareAndSwap(nil, err.Error())
						}
						continue
					}
					if total != 100*1000 {
						bad.Add(1)
					}
					reads.Add(1)
				}
			}()
		}
	}

	transfer := func(i int) {
		pr.write(func(tx *sql.Tx) error {
			a, b := i%100+1, (i*7)%100+1
			if _, err := tx.Exec(`UPDATE accounts SET balance = balance - 1, updated_at = ? WHERE id = ?`, now+int64(i), a); err != nil {
				return err
			}
			_, err := tx.Exec(`UPDATE accounts SET balance = balance + 1, updated_at = ? WHERE id = ?`, now+int64(i), b)
			return err
		})
	}
	for i := range 2000 {
		transfer(i)
		if i%500 == 499 {
			pr.assertSame(fmt.Sprintf("after %d transfers", i+1))
		}
	}

	// Checkpoint with readers active (PASSIVE), then TRUNCATE; the next
	// write restarts the WAL with new salts.
	for _, mode := range []string{"PASSIVE", "RESTART", "TRUNCATE"} {
		before := pr.p.Cap.Pos
		busy, logf_, ck, err := pr.p.Checkpoint(ctx, mode)
		if err != nil {
			t.Fatal(err)
		}
		for i := range 50 {
			transfer(10000 + i)
		}
		after := pr.p.Cap.Pos
		t.Logf("checkpoint %s: busy=%d log=%d ckpt=%d; salts %08x/%08x seq %d -> %08x/%08x seq %d", mode, busy, logf_, ck,
			before.Salt1, before.Salt2, before.CkptSeq, after.Salt1, after.Salt2, after.CkptSeq)
		pr.assertSame("after checkpoint " + mode)
	}

	// A 1000-row batch with blobs, then deletes, then VACUUM (shrinks the
	// file: the commit frame carries the smaller size).
	pr.write(func(tx *sql.Tx) error {
		for i := range 1000 {
			if _, err := tx.Exec(`INSERT INTO items(name, payload, created_at) VALUES(?, randomblob(300), ?)`, fmt.Sprintf("item-%04d", i), now); err != nil {
				return err
			}
		}
		return nil
	})
	pr.assertSame("batch 1000")
	pr.write(func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM items WHERE id % 3 <> 0`)
		return err
	})
	var pagesBefore int
	pr.p.DB.Reader().QueryRow(`PRAGMA page_count`).Scan(&pagesBefore)
	if _, err := pr.p.DB.Writer().Exec(`VACUUM`); err != nil {
		t.Fatal(err)
	}
	if err := pr.p.CaptureNow(); err != nil {
		t.Fatal(err)
	}
	var pagesAfter int
	pr.p.DB.Reader().QueryRow(`PRAGMA page_count`).Scan(&pagesAfter)
	last := pr.shipped[len(pr.shipped)-1]
	t.Logf("VACUUM: %d -> %d pages; record lsn %d: %d frames, %d distinct pages, db size %d", pagesBefore, pagesAfter, last.LSN, last.Frames, len(last.Pages), last.DBSize)
	pr.assertSame("after VACUUM")

	// page_size cannot change in WAL mode: PRAGMA page_size + VACUUM is a
	// no-op (https://www.sqlite.org/pragma.html#pragma_page_size).
	var ps1, ps2 int
	pr.p.DB.Reader().QueryRow(`PRAGMA page_size`).Scan(&ps1)
	if _, err := pr.p.DB.Writer().Exec(`PRAGMA page_size = 8192`); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.p.DB.Writer().Exec(`VACUUM`); err != nil {
		t.Fatal(err)
	}
	if err := pr.p.CaptureNow(); err != nil {
		t.Fatal(err)
	}
	pr.p.DB.Writer().QueryRow(`PRAGMA page_size`).Scan(&ps2)
	t.Logf("page_size in WAL mode: %d -> PRAGMA page_size=8192; VACUUM -> %d", ps1, ps2)
	pr.assertSame("after page_size+VACUUM")

	close(stop)
	wg.Wait()
	t.Logf("concurrent reads during transfers, checkpoints, batch and VACUUM: %d ok, %d inconsistent totals, %d errors (first: %v)", reads.Load(), bad.Load(), failed.Load(), firstErr.Load())
	if bad.Load() != 0 || failed.Load() != 0 {
		t.Fatal("a reader saw an inconsistent total or failed")
	}

	// Migration: the engine closes (capturing first), the new binary opens
	// with one more migration, which commits in its own transaction.
	ppath := pr.p.DB.Path()
	if err := pr.p.Close(); err != nil {
		t.Fatal(err)
	}
	pos := pr.p.Cap.Pos
	p2, err := OpenPrimary(ctx, ppath, migrations(t, 2), storeOpts(pr.sync))
	if err != nil {
		t.Fatal(err)
	}
	p2.Cap.Pos = pos
	p2.Ship = pr.ship
	pr.p = p2
	if err := p2.CaptureNow(); err != nil {
		t.Fatal(err)
	}
	pr.write(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO notes(item_id, body) SELECT id, 'n' FROM items LIMIT 10`)
		return err
	})
	var nnotes int
	if err := pr.r.DB.Reader().QueryRow(`SELECT count(*) FROM notes`).Scan(&nnotes); err != nil || nnotes != 10 {
		t.Fatalf("replica reads the migrated table: %d, %v", nnotes, err)
	}
	if v, err := pr.r.DB.Version(ctx); err != nil || v != 2 {
		t.Fatalf("replica schema version %d, %v", v, err)
	}
	pr.assertSame("after migration")

	t.Logf("records shipped: %d", len(pr.shipped))
}
