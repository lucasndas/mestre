//go:build spike

package dbcapture

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/store"
)

// logPair is a plan B primary with a full Tower replica and an excluded one.
type logPair struct {
	t       testing.TB
	ctx     context.Context
	dir     string
	p       *LogPrimary
	r, x    *LogReplica
	applyOn bool
	shipped []*LogRecord
}

func newLogPair(t testing.TB) *logPair {
	ctx := context.Background()
	dir := t.TempDir()
	lp := &logPair{t: t, ctx: ctx, dir: dir, applyOn: true}
	p, err := OpenLogPrimary(ctx, filepath.Join(dir, "p.db"), migrations(t, 1), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	lp.p = p
	p.Ship = lp.ship
	lp.r = lp.fullCopy("r.db", nil)
	lp.x = lp.fullCopy("x.db", map[string]bool{"secrets": true})
	t.Cleanup(func() {
		for _, r := range []*LogReplica{lp.r, lp.x} {
			if r != nil {
				r.DB.Close()
			}
		}
		lp.p.DB.Close()
	})
	return lp
}

// fullCopy is the documented one: VACUUM INTO on the writer (so the lsn is
// exact), then, for the excluded machine, the vault rows and the log go.
func (lp *logPair) fullCopy(name string, skip map[string]bool) *LogReplica {
	lp.t.Helper()
	dst := filepath.Join(lp.dir, name)
	for _, s := range []string{"", "-wal", "-shm"} {
		os.Remove(dst + s)
	}
	conn, _ := lp.p.DB.Writer().Conn(lp.ctx)
	if _, err := conn.ExecContext(lp.ctx, `VACUUM INTO ?`, dst); err != nil {
		lp.t.Fatal(err)
	}
	lsn := lp.p.LSN
	conn.Close()
	if len(skip) > 0 {
		db, err := sql.Open("sqlite", dst)
		if err != nil {
			lp.t.Fatal(err)
		}
		for tb := range skip {
			db.Exec(`DELETE FROM "` + tb + `"`)
		}
		db.Exec(`DELETE FROM replication_log`)
		db.Exec(`VACUUM`)
		db.Close()
	}
	db, err := store.Open(lp.ctx, dst, migrations(lp.t, 2), store.Options{Replica: true})
	if err != nil {
		lp.t.Fatal(err)
	}
	return &LogReplica{DB: db, LSN: lsn, Skip: skip}
}

func (lp *logPair) ship(r *LogRecord) {
	lp.shipped = append(lp.shipped, r)
	if !lp.applyOn {
		return
	}
	for _, rep := range []*LogReplica{lp.r, lp.x} {
		if rep == nil {
			continue
		}
		dec, err := DecodeLog(EncodeLog(r))
		if err != nil {
			lp.t.Fatal(err)
		}
		if err := rep.Apply(lp.ctx, dec); err != nil {
			lp.t.Fatalf("apply lsn %d: %v", r.LSN, err)
		}
	}
}

func (lp *logPair) write(fn func(tx *sql.Tx) error) {
	lp.t.Helper()
	if err := lp.p.Write(lp.ctx, fn); err != nil {
		lp.t.Fatal(err)
	}
}

func (lp *logPair) assertSame(label string) {
	lp.t.Helper()
	ctx := lp.ctx
	for _, c := range []struct {
		name string
		r    *LogReplica
		skip []string
	}{{"tower", lp.r, nil}, {"excluded", lp.x, []string{"secrets", "replication_log"}}} {
		if ic, err := IntegrityCheck(ctx, c.r.DB.Reader()); err != nil || ic != "ok" {
			lp.t.Fatalf("%s: %s integrity %q %v", label, c.name, ic, err)
		}
		a, err := LogicalHash(ctx, lp.p.DB.Reader(), c.skip...)
		if err != nil {
			lp.t.Fatal(err)
		}
		b, err := LogicalHash(ctx, c.r.DB.Reader(), c.skip...)
		if err != nil {
			lp.t.Fatal(err)
		}
		if a != b {
			lp.t.Fatalf("%s: %s logical dump differs", label, c.name)
		}
	}
	var n int
	lp.x.DB.Reader().QueryRow(`SELECT count(*) FROM secrets`).Scan(&n)
	if n != 0 {
		lp.t.Fatalf("%s: excluded replica has %d secrets", label, n)
	}
	lp.t.Logf("%s: tower and excluded logically identical (lsn %d; excluded without secrets)", label, lp.r.LSN)
}

// Q6: the plan B, through the same scenarios as the default.
func TestPlanBLogicalLog(t *testing.T) {
	t.Logf("platform %s", osInfo())
	lp := newLogPair(t)
	ctx := lp.ctx
	lp.write(func(tx *sql.Tx) error {
		for i := 1; i <= 100; i++ {
			if _, err := tx.Exec(`INSERT INTO accounts(id, owner, balance, updated_at) VALUES(?, ?, 1000, 0)`, i, fmt.Sprintf("u%03d", i)); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`INSERT INTO secrets(id, ciphertext, updated_at) VALUES('ca', x'00112233', 0)`)
		return err
	})
	lp.assertSame("seed")

	stop := make(chan struct{})
	var reads, bad atomic.Int64
	var wg sync.WaitGroup
	for _, db := range []*sql.DB{lp.p.DB.Reader(), lp.r.DB.Reader()} {
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
					if err := db.QueryRowContext(ctx, `SELECT coalesce(sum(balance),0) FROM accounts`).Scan(&total); err != nil || total != 100*1000 {
						bad.Add(1)
						continue
					}
					reads.Add(1)
				}
			}()
		}
	}
	for i := range 2000 {
		lp.write(func(tx *sql.Tx) error {
			a, b := i%100+1, (i*7)%100+1
			if _, err := tx.Exec(`UPDATE accounts SET balance = balance - 1, updated_at = ? WHERE id = ?`, i, a); err != nil {
				return err
			}
			_, err := tx.Exec(`UPDATE accounts SET balance = balance + 1, updated_at = ? WHERE id = ?`, i, b)
			return err
		})
	}
	lp.write(func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE secrets SET ciphertext = x'44556677', updated_at = 1 WHERE id = 'ca'`)
		return err
	})
	lp.assertSame("after 2000 transfers and a secret update")
	// Checkpoints and VACUUM change no rows: nothing travels.
	before := lp.p.LSN
	lp.p.DB.Writer().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	lp.write(func(tx *sql.Tx) error {
		for i := range 1000 {
			if _, err := tx.Exec(`INSERT INTO items(name, payload, created_at) VALUES(?, randomblob(300), 0)`, fmt.Sprintf("item-%04d", i)); err != nil {
				return err
			}
		}
		return nil
	})
	lp.write(func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM items WHERE id % 3 <> 0`)
		return err
	})
	if _, err := lp.p.DB.Writer().Exec(`VACUUM`); err != nil {
		t.Fatal(err)
	}
	t.Logf("checkpoint, batch 1000, delete, VACUUM: %d records (the two writes)", lp.p.LSN-before)
	lp.assertSame("after VACUUM")
	close(stop)
	wg.Wait()
	t.Logf("concurrent reads: %d ok, %d inconsistent or failed", reads.Load(), bad.Load())
	if bad.Load() != 0 {
		t.Fatal("inconsistent read")
	}
	// A migration travels as DDL plus its schema_migrations row.
	if err := lp.p.Migrate(ctx, 2, mig2); err != nil {
		t.Fatal(err)
	}
	lp.write(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO notes(item_id, body) SELECT id, 'n' FROM items LIMIT 10`)
		return err
	})
	if v, err := lp.r.DB.Version(ctx); err != nil || v != 2 {
		t.Fatalf("replica version %d %v", v, err)
	}
	lp.assertSame("after migration")

	// The discarded transactions of a returning old Citadel are its log rows
	// after the lsn the promoted Tower had.
	fork := lp.p.LSN
	lp.applyOn = false
	for i := range 3 {
		lp.write(func(tx *sql.Tx) error {
			_, err := tx.Exec(`UPDATE accounts SET owner = ? WHERE id = ?`, fmt.Sprintf("lost-%d", i), i+1)
			return err
		})
	}
	rows, _ := lp.p.DB.Reader().Query(`SELECT body FROM replication_log WHERE lsn > ? ORDER BY lsn`, fork)
	for rows.Next() {
		var b []byte
		rows.Scan(&b)
		rec, _ := DecodeLog(b)
		for _, c := range rec.Changes {
			t.Logf("discarded: lsn %d table %s op %d key %v", rec.LSN, c.Table, c.Op, c.Old[0])
		}
	}
	rows.Close()
}

// Q6 numbers: bytes and time per transaction for the same workloads, and
// the extra WAL the log costs the primary.
func TestPlanBThroughput(t *testing.T) {
	t.Logf("platform %s", osInfo())
	lp := newLogPair(t)
	lp.x.DB.Close()
	lp.x = nil
	lp.applyOn = false
	// Automatic checkpoints off, to count the frames the log adds.
	lp.p.DB.Writer().Exec(`PRAGMA wal_autocheckpoint = 0`)
	lp.write(func(tx *sql.Tx) error {
		for i := 1; i <= 1000; i++ {
			if _, err := tx.Exec(`INSERT INTO accounts(id, owner, balance, updated_at) VALUES(?, ?, 0, 0)`, i, fmt.Sprintf("user-%04d", i)); err != nil {
				return err
			}
		}
		return nil
	})
	lp.r.Apply(lp.ctx, lp.shipped[len(lp.shipped)-1])
	run := func(name string, n int, fn func(tx *sql.Tx, i int) error) {
		var commit, wire, apply durs
		bytes := 0
		walBefore := walFrames(t, lp.p.DB.Path())
		start := time.Now()
		for i := range n {
			t0 := time.Now()
			if err := lp.p.Write(lp.ctx, func(tx *sql.Tx) error { return fn(tx, i) }); err != nil {
				t.Fatal(err)
			}
			t1 := time.Now()
			enc := EncodeLog(lp.shipped[len(lp.shipped)-1])
			dec, _ := DecodeLog(enc)
			t2 := time.Now()
			if err := lp.r.Apply(lp.ctx, dec); err != nil {
				t.Fatal(err)
			}
			t3 := time.Now()
			commit, wire, apply = append(commit, t1.Sub(t0)), append(wire, t2.Sub(t1)), append(apply, t3.Sub(t2))
			bytes += len(enc)
		}
		el := time.Since(start)
		frames := walFrames(t, lp.p.DB.Path()) - walBefore
		t.Logf("%-20s %5d txns in %v (%.0f txn/s incl. apply) | primary WAL frames/txn %.1f | bytes/txn %d | commit+log %v | encode+decode %v | apply %v",
			name, n, el.Round(time.Millisecond), float64(n)/el.Seconds(), float64(frames)/float64(n), bytes/n, commit, wire, apply)
	}
	run("update 1 row", 2000, func(tx *sql.Tx, i int) error {
		_, err := tx.Exec(`UPDATE accounts SET balance = ?, updated_at = ? WHERE id = ?`, i+1, i, (i*37)%1000+1)
		return err
	})
	run("insert 1 row (100 B)", 2000, func(tx *sql.Tx, i int) error {
		_, err := tx.Exec(`INSERT INTO items(name, payload, created_at) VALUES(?, randomblob(100), 0)`, fmt.Sprintf("it-%06d", i))
		return err
	})
	run("batch 1000 rows", 20, func(tx *sql.Tx, i int) error {
		for j := range 1000 {
			if _, err := tx.Exec(`INSERT INTO items(name, payload, created_at) VALUES(?, randomblob(100), 0)`, fmt.Sprintf("b-%02d-%04d", i, j)); err != nil {
				return err
			}
		}
		return nil
	})
	var logBytes int64
	lp.p.DB.Reader().QueryRow(`SELECT sum(length(body)) FROM replication_log`).Scan(&logBytes)
	t.Logf("replication_log: %d records, %.1f MiB of bodies", lp.p.LSN, float64(logBytes)/(1<<20))
}

func walFrames(t *testing.T, path string) int64 {
	st, err := os.Stat(path + "-wal")
	if err != nil {
		return 0
	}
	return (st.Size() - walHeaderSize) / (frameHeaderSize + 4096)
}
