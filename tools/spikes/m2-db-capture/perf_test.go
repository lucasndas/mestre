//go:build spike

package dbcapture

import (
	"database/sql"
	"fmt"
	"sort"
	"testing"
	"time"
)

type durs []time.Duration

func (d durs) pct(p float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := append(durs(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[min(len(s)-1, int(float64(len(s))*p))]
}

func (d durs) String() string {
	return fmt.Sprintf("p50 %v p99 %v max %v", d.pct(0.5).Round(time.Microsecond), d.pct(0.99).Round(time.Microsecond), d.pct(1).Round(time.Microsecond))
}

type workload struct {
	name string
	n    int
	fn   func(tx *sql.Tx, i int) error
}

// stepped runs each transaction, then times capture, encode+decode (the
// wire) and apply separately.
func stepped(t *testing.T, pr *pair, w workload) {
	pr.applyOn = false
	var commit, capt, wire, apply, e2e durs
	frames, pages, bytes, deflated, recs := 0, 0, 0, 0, 0
	start := time.Now()
	for i := range w.n {
		t0 := time.Now()
		tx, err := pr.p.DB.Writer().BeginTx(pr.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.fn(tx, i); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		t1 := time.Now()
		got, err := pr.p.Cap.Capture()
		if err != nil {
			t.Fatal(err)
		}
		t2 := time.Now()
		if len(got) != 1 {
			t.Fatalf("txn %d: %d records right after commit, want 1 (read %d, tail %d, scanned %d, pos %+v)", i, len(got), pr.p.Cap.LastFramesRead, pr.p.Cap.LastPartialTail, pr.p.Cap.LastBytesScanned, pr.p.Cap.Pos)
		}
		r := got[0]
		enc := r.Encode()
		dec, err := DecodeRecord(enc)
		if err != nil {
			t.Fatal(err)
		}
		t3 := time.Now()
		if err := pr.r.Apply(pr.ctx, dec); err != nil {
			t.Fatal(err)
		}
		t4 := time.Now()
		commit = append(commit, t1.Sub(t0))
		capt = append(capt, t2.Sub(t1))
		wire = append(wire, t3.Sub(t2))
		apply = append(apply, t4.Sub(t3))
		e2e = append(e2e, t4.Sub(t1))
		frames += r.Frames
		pages += len(r.Pages)
		bytes += len(enc)
		if i%10 == 0 || w.n <= 20 {
			deflated += Deflated(enc)
			recs++
		}
	}
	el := time.Since(start)
	n := w.n
	t.Logf("%-22s %5d txns in %v (%.0f txn/s incl. apply) | frames/txn %.1f, pages/txn %.1f, bytes/txn %d (deflate ~%d) | commit %v | capture %v | encode+decode %v | apply %v | commit->applied %v",
		w.name, n, el.Round(time.Millisecond), float64(n)/el.Seconds(), float64(frames)/float64(n), float64(pages)/float64(n),
		bytes/n, deflated/max(recs, 1), commit, capt, wire, apply, e2e)
	pr.applyOn = true
}

// Q3: frames, bytes and time per transaction for typical writes.
func TestThroughput(t *testing.T) {
	t.Logf("platform %s", osInfo())
	pr := newPair(t, "")
	now := time.Now().UnixMilli()
	pr.write(func(tx *sql.Tx) error {
		for i := 1; i <= 1000; i++ {
			if _, err := tx.Exec(`INSERT INTO accounts(id, owner, balance, updated_at) VALUES(?, ?, 0, ?)`, i, fmt.Sprintf("user-%04d", i), now); err != nil {
				return err
			}
		}
		return nil
	})
	stepped(t, pr, workload{"update 1 row", 2000, func(tx *sql.Tx, i int) error {
		_, err := tx.Exec(`UPDATE accounts SET balance = ?, updated_at = ? WHERE id = ?`, i+1, now+int64(i), (i*37)%1000+1)
		return err
	}})
	stepped(t, pr, workload{"insert 1 row (100 B)", 2000, func(tx *sql.Tx, i int) error {
		_, err := tx.Exec(`INSERT INTO items(name, payload, created_at) VALUES(?, randomblob(100), ?)`, fmt.Sprintf("it-%06d", i), now)
		return err
	}})
	stepped(t, pr, workload{"batch 1000 rows", 20, func(tx *sql.Tx, i int) error {
		for j := range 1000 {
			if _, err := tx.Exec(`INSERT INTO items(name, payload, created_at) VALUES(?, randomblob(100), ?)`, fmt.Sprintf("b-%02d-%04d", i, j), now); err != nil {
				return err
			}
		}
		return nil
	}})
	// The capture reads only the new frames: its cost does not grow with the
	// WAL (no checkpoint has run so far).
	st := pr.p.Cap.Pos
	t.Logf("WAL before checkpoint: %d frames (%.1f MiB)", st.NextFrame, float64(st.NextFrame*int64(frameHeaderSize+int(st.PageSize)))/(1<<20))
	t0 := time.Now()
	busy, lg, ck, err := pr.p.Checkpoint(pr.ctx, "TRUNCATE")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("checkpoint TRUNCATE of that WAL: %v (busy %d, log %d, ckpt %d)", time.Since(t0).Round(time.Microsecond), busy, lg, ck)
	stepped(t, pr, workload{"update 1 row (new WAL)", 500, func(tx *sql.Tx, i int) error {
		_, err := tx.Exec(`UPDATE accounts SET balance = ?, updated_at = ? WHERE id = ?`, -i-1, now+int64(i), (i*37)%1000+1)
		return err
	}})
	pr.assertSame("after throughput")
	var pagesN int
	pr.p.DB.Reader().QueryRow(`PRAGMA page_count`).Scan(&pagesN)
	t.Logf("database: %d pages (%.1f MiB)", pagesN, float64(pagesN*4096)/(1<<20))

	// A full copy of this database (page-exact snapshot through sqlite_dbpage).
	t0 = time.Now()
	lsn := pr.fullCopy()
	t.Logf("full copy (snapshot + replace + reopen): %v at lsn %d", time.Since(t0).Round(time.Millisecond), lsn)
}

// Q1: synchronous levels. The commit frame is visible to the capture right
// after Commit returns at every level (it is in the OS cache; fsync only
// matters for a power loss); the level changes only the cost of the commit.
func TestSyncLevels(t *testing.T) {
	t.Logf("platform %s", osInfo())
	for _, lvl := range []string{"OFF", "NORMAL", "FULL"} {
		pr := newPair(t, lvl)
		pr.write(func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO accounts(id, owner, balance, updated_at) VALUES(1, 'x', 0, 0)`)
			return err
		})
		stepped(t, pr, workload{"sync=" + lvl, 300, func(tx *sql.Tx, i int) error {
			_, err := tx.Exec(`UPDATE accounts SET balance = ? WHERE id = 1`, i+1)
			return err
		}})
	}
}
