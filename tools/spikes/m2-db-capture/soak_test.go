//go:build spike

package dbcapture

import (
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Q1 soak: a random mix of writes, rollbacks of transactions that already
// spilled pages into the WAL, and checkpoints of every mode, with readers on
// both sides; the replica is compared every 2500 operations.
func TestSoak(t *testing.T) {
	t.Logf("platform %s", osInfo())
	pr := newPair(t, "")
	ctx := pr.ctx
	w := pr.p.DB.Writer()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var reads atomic.Int64
	for _, db := range []*sql.DB{pr.p.DB.Reader(), pr.r.DB.Reader()} {
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					var n int
					db.QueryRowContext(ctx, `SELECT max(id) FROM items`).Scan(&n)
					reads.Add(1)
				}
			}()
		}
	}
	rng := uint64(42)
	rnd := func(n int) int {
		rng ^= rng << 13
		rng ^= rng >> 7
		rng ^= rng << 17
		return int(rng % uint64(n))
	}
	const total = 10000
	rollbacks, ckpts, spilledRollbacks, walResets := 0, 0, 0, 0
	t0 := time.Now()
	for i := range total {
		switch k := rnd(100); {
		case k < 50:
			pr.write(func(tx *sql.Tx) error {
				_, err := tx.Exec(`INSERT INTO items(name, payload, created_at) VALUES(?, randomblob(?), 0)`, fmt.Sprintf("s%d", i), rnd(2000))
				return err
			})
		case k < 75:
			pr.write(func(tx *sql.Tx) error {
				_, err := tx.Exec(`UPDATE items SET name = ?, payload = randomblob(?) WHERE id = (SELECT id FROM items WHERE id >= ? LIMIT 1)`, fmt.Sprintf("u%d", i), rnd(3000), rnd(i+1))
				return err
			})
		case k < 88:
			pr.write(func(tx *sql.Tx) error {
				_, err := tx.Exec(`DELETE FROM items WHERE id IN (SELECT id FROM items WHERE id >= ? LIMIT ?)`, rnd(i+1), rnd(20))
				return err
			})
		case k < 93:
			// A big transaction that spills, then rolls back.
			before := pr.p.Cap.Pos
			tx, err := w.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			tx.Exec(`PRAGMA cache_size = 30`) // spill early
			for j := range 300 {
				tx.Exec(`INSERT INTO items(name, payload, created_at) VALUES(?, randomblob(800), 0)`, fmt.Sprintf("rb%d-%d", i, j))
			}
			recs, err := pr.p.Cap.Capture()
			if err != nil || len(recs) != 0 {
				t.Fatalf("capture during an open transaction: %d records, %v", len(recs), err)
			}
			if pr.p.Cap.LastPartialTail > 0 {
				spilledRollbacks++
			}
			tx.Rollback()
			w.Exec(`PRAGMA cache_size = -2000`)
			rollbacks++
			if after := pr.p.Cap.Pos; after.Salt1 == before.Salt1 && after.NextFrame != before.NextFrame {
				t.Fatal("position moved inside one WAL generation")
			} else if after.Salt1 != before.Salt1 {
				walResets++ // the spilled frames started a new WAL generation
			}
		case k < 98:
			pr.write(func(tx *sql.Tx) error {
				for j := range 200 + rnd(800) {
					if _, err := tx.Exec(`INSERT INTO items(name, payload, created_at) VALUES(?, randomblob(50), 0)`, fmt.Sprintf("bt%d-%d", i, j)); err != nil {
						return err
					}
				}
				return nil
			})
		default:
			modes := []string{"PASSIVE", "FULL", "RESTART", "TRUNCATE"}
			if _, _, _, err := pr.p.Checkpoint(ctx, modes[rnd(len(modes))]); err != nil {
				t.Fatal(err)
			}
			ckpts++
		}
		if i%2500 == 2499 {
			pr.assertSame(fmt.Sprintf("soak %d", i+1))
		}
	}
	close(stop)
	wg.Wait()
	t.Logf("soak: %d operations in %v: %d records, %d rollbacks (%d with spilled frames in the WAL, %d of them after a WAL reset), %d checkpoints, %d concurrent reads",
		total, time.Since(t0).Round(time.Millisecond), len(pr.shipped), rollbacks, spilledRollbacks, walResets, ckpts, reads.Load())
	pr.assertSame("soak end")
}
