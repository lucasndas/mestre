//go:build spike

package dbcapture

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/store"
)

// TestHelperProcess is the child of the crash tests: it is killed (SIGKILL
// on Linux, TerminateProcess on Windows) at the point it reports.
func TestHelperProcess(t *testing.T) {
	mode := os.Getenv("MESTRE_SPIKE_HELPER")
	if mode == "" {
		t.Skip("helper only")
	}
	ctx := context.Background()
	path := os.Getenv("MESTRE_SPIKE_DB")
	block := func(msg string) {
		fmt.Println(msg)
		io.Copy(io.Discard, os.Stdin) // until killed
		os.Exit(3)
	}
	if planBHelper(t, mode, block) {
		return
	}
	switch mode {
	case "primary-crash":
		p, err := OpenPrimary(ctx, path, migrations(t, 1), store0())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.DB.Writer().Exec(`PRAGMA cache_size = 20`); err != nil {
			t.Fatal(err)
		}
		for i := range 20 {
			if _, err := p.DB.Writer().Exec(`UPDATE accounts SET balance = balance + 1, updated_at = ? WHERE id = ?`, i, i%10+1); err != nil {
				t.Fatal(err)
			}
		}
		tx, err := p.DB.Writer().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		for i := range 3000 {
			if _, err := tx.Exec(`INSERT INTO items(name, payload, created_at) VALUES(?, randomblob(500), 0)`, fmt.Sprintf("uncommitted-%d", i)); err != nil {
				t.Fatal(err)
			}
		}
		st, _ := os.Stat(path + "-wal")
		block(fmt.Sprintf("SPILLED %d", st.Size()))
	case "replica-crash":
		r, err := OpenReplica(ctx, path, migrations(t, 2), store0())
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(os.Getenv("MESTRE_SPIKE_RECORD"))
		if err != nil {
			t.Fatal(err)
		}
		rec, err := DecodeRecord(b)
		if err != nil {
			t.Fatal(err)
		}
		switch os.Getenv("MESTRE_SPIKE_POINT") {
		case "before-commit":
			r.BeforeCommit = func() { block("BEFORE-COMMIT") }
		case "after-commit":
			r.AfterCommit = func() { block("AFTER-COMMIT") }
		}
		if err := r.Apply(ctx, rec); err != nil {
			t.Fatal(err)
		}
		block("DONE")
	}
}

func fileSHA(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) // only with every connection closed
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func store0() store.Options { return storeOpts("") }

// runUntil starts the helper, waits for its line and kills it.
func runUntil(t *testing.T, env ...string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$", "-test.v=false")
	cmd.Env = append(os.Environ(), env...)
	stdin, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("helper: %v (%q)", err, line)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	stdin.Close()
	return strings.TrimSpace(line)
}

// Q5a: the primary dies in the middle of a transaction whose pages already
// spilled into the WAL. The capture ships only the committed transactions.
func TestPrimaryCrashMidTransaction(t *testing.T) {
	pr := newPair(t, "")
	ctx := pr.ctx
	pr.write(func(tx *sql.Tx) error {
		for i := 1; i <= 10; i++ {
			if _, err := tx.Exec(`INSERT INTO accounts(id, owner, balance, updated_at) VALUES(?, 'x', 0, 0)`, i); err != nil {
				return err
			}
		}
		return nil
	})
	path := pr.p.DB.Path()
	if err := pr.p.Close(); err != nil {
		t.Fatal(err)
	}
	capt := pr.p.Cap // the position the engine persisted
	before := len(pr.shipped)

	line := runUntil(t, "MESTRE_SPIKE_HELPER=primary-crash", "MESTRE_SPIKE_DB="+path)
	t.Logf("child: %s, then killed", line)

	// The engine restarts: it captures before anything else.
	recs, err := capt.Capture()
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path + "-wal")
	t.Logf("after the crash: %d records, %d frames read, %d valid frames after the last commit (uncommitted, not shipped), WAL %d bytes, committed end at frame %d",
		len(recs), capt.LastFramesRead, capt.LastPartialTail, st.Size(), capt.Pos.NextFrame)
	if len(recs) != 20 || capt.LastPartialTail == 0 {
		t.Fatalf("want 20 records and an uncommitted tail, got %d and %d", len(recs), capt.LastPartialTail)
	}
	for _, r := range recs {
		pr.ship(r)
	}
	p2, err := OpenPrimary(ctx, path, migrations(t, 1), store0())
	if err != nil {
		t.Fatal(err)
	}
	p2.Cap = capt
	p2.Ship = pr.ship
	pr.p = p2
	if err := p2.CaptureNow(); err != nil { // recovery must not add anything
		t.Fatal(err)
	}
	if len(pr.shipped) != before+20 {
		t.Fatalf("reopening shipped %d more records", len(pr.shipped)-before-20)
	}
	pr.write(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO items(name, created_at) VALUES('after-crash', 0)`)
		return err
	})
	var n int
	pr.r.DB.Reader().QueryRow(`SELECT count(*) FROM items WHERE name LIKE 'uncommitted-%'`).Scan(&n)
	if n != 0 {
		t.Fatalf("replica has %d uncommitted rows", n)
	}
	pr.assertSame("after primary crash and restart")
}

// Q5b: the replica dies in the middle of applying a record (before the
// commit), and between the commit and the lsn file (after the commit).
func TestReplicaCrashMidApply(t *testing.T) {
	for _, point := range []string{"before-commit", "after-commit"} {
		t.Run(point, func(t *testing.T) {
			t.Parallel()
			pr := newPair(t, "")
			pr.write(func(tx *sql.Tx) error {
				_, err := tx.Exec(`INSERT INTO accounts(id, owner, balance, updated_at) VALUES(1, 'x', 0, 0)`)
				return err
			})
			want, _ := LogicalHash(pr.ctx, pr.r.DB.Reader())
			pr.applyOn = false
			pr.write(func(tx *sql.Tx) error {
				for i := range 1000 {
					if _, err := tx.Exec(`INSERT INTO items(name, payload, created_at) VALUES(?, randomblob(200), 0)`, fmt.Sprintf("b%d", i)); err != nil {
						return err
					}
				}
				return nil
			})
			pr.applyOn = true
			rec := pr.shipped[len(pr.shipped)-1]
			recFile := filepath.Join(pr.dir, "rec.bin")
			os.WriteFile(recFile, rec.Encode(), 0o600)
			rpath := pr.r.DB.Path()
			pr.r.DB.Close()
			pr.r = nil

			line := runUntil(t, "MESTRE_SPIKE_HELPER=replica-crash", "MESTRE_SPIKE_DB="+rpath,
				"MESTRE_SPIKE_RECORD="+recFile, "MESTRE_SPIKE_POINT="+point)
			t.Logf("child: %s, then killed (record lsn %d, %d pages)", line, rec.LSN, len(rec.Pages))

			pr.openReplica()
			ic, _ := IntegrityCheck(pr.ctx, pr.r.DB.Reader())
			got, _ := LogicalHash(pr.ctx, pr.r.DB.Reader())
			var n int
			pr.r.DB.Reader().QueryRow(`SELECT count(*) FROM items`).Scan(&n)
			t.Logf("replica after restart: integrity %s, lsn file %d, items %d, same as before the record: %v", ic, pr.r.LSN, n, got == want)
			if ic != "ok" || pr.r.LSN != rec.LSN-1 {
				t.Fatal("unexpected replica state")
			}
			if point == "before-commit" && (got != want || n != 0) {
				t.Fatal("a rolled-back apply left changes")
			}
			if point == "after-commit" && n != 1000 {
				t.Fatal("the committed apply is missing")
			}
			// The Tower asks again from lsn+1; re-applying is idempotent.
			if err := pr.r.Apply(pr.ctx, rec); err != nil {
				t.Fatal(err)
			}
			pr.assertSame("after re-apply")
		})
	}
}

// Q5c: gaps. A record lost in transit is refused and asked again; a WAL reset
// the capture did not drive (an engine bug, a journal_mode switch) is
// detected and solved with a full copy; a VACUUM INTO copy is not a valid
// base for page records.
func TestGaps(t *testing.T) {
	pr := newPair(t, "")
	upd := func(i int) {
		pr.write(func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO accounts(id, owner, balance, updated_at) VALUES(?, 'g', ?, 0)
				ON CONFLICT(id) DO UPDATE SET balance = excluded.balance`, i%50+1, i)
			return err
		})
	}
	for i := range 100 {
		upd(i)
	}
	// Lost in transit.
	pr.applyOn = false
	upd(1000)
	pr.applyOn = true
	lost := pr.shipped[len(pr.shipped)-1]
	pr.applyOn = false
	upd(1001)
	pr.applyOn = true
	next := pr.shipped[len(pr.shipped)-1]
	var ooo *ErrOutOfOrder
	if err := pr.r.Apply(pr.ctx, next); !errors.As(err, &ooo) {
		t.Fatalf("gap not detected: %v", err)
	}
	t.Logf("lost record: %v", ooo)
	for _, r := range []*Record{lost, next} {
		if err := pr.r.Apply(pr.ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := pr.r.Apply(pr.ctx, lost); err != nil { // a repeat is ignored
		t.Fatal(err)
	}
	pr.assertSame("after asking again")

	// A checkpoint the capture did not drive, after a commit it did not see.
	if _, err := pr.p.DB.Writer().Exec(`UPDATE accounts SET balance = -1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.p.DB.Writer().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	err := pr.p.Write(pr.ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE accounts SET balance = -2 WHERE id = 2`)
		return err
	})
	if !errors.Is(err, ErrGap) {
		t.Fatalf("hidden checkpoint not detected: %v", err)
	}
	t.Logf("hidden checkpoint: %v", err)
	pr.p.Cap.Pos = Position{LSN: pr.p.Cap.Pos.LSN} // a new base
	lsn := pr.fullCopy()
	t.Logf("full copy at lsn %d", lsn)
	upd(2000)
	pr.assertSame("after full copy (hidden checkpoint)")

	// journal_mode=DELETE (the only way to change page_size) is refused
	// while the read pool is open: it needs exclusive access.
	w := pr.p.DB.Writer()
	_, err = w.Exec(`PRAGMA journal_mode = DELETE`)
	t.Logf("journal_mode=DELETE with the read pool open: %v", err)
	// With the engine stopped, another program switches to DELETE, changes
	// page_size and vacuums, then back to WAL: no frame ever reaches a WAL.
	// The only defense is a fingerprint of the file taken at the clean close.
	ppath := pr.p.DB.Path()
	if err := pr.p.Close(); err != nil {
		t.Fatal(err)
	}
	fp := fileSHA(t, ppath)
	raw, err := sql.Open("sqlite", ppath)
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	for _, q := range []string{`PRAGMA journal_mode = DELETE`, `PRAGMA page_size = 8192`, `VACUUM`, `PRAGMA journal_mode = WAL`} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	raw.Close()
	fp2 := fileSHA(t, ppath)
	pos := pr.p.Cap.Pos
	p2, err := OpenPrimary(pr.ctx, ppath, migrations(t, 1), store0())
	if err != nil {
		t.Fatal(err)
	}
	p2.Cap.Pos = pos
	p2.Ship = pr.ship
	pr.p = p2
	var ps int
	p2.DB.Reader().QueryRow(`PRAGMA page_size`).Scan(&ps)
	pr.applyOn = false
	pr.write(func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE accounts SET balance = -3 WHERE id = 3`)
		return err
	})
	pr.applyOn = true
	applyErr := pr.r.Apply(pr.ctx, pr.shipped[len(pr.shipped)-1])
	ic, _ := IntegrityCheck(pr.ctx, pr.r.DB.Reader())
	t.Logf("external page_size change to %d while the engine was down: capture accepted the new WAL (ExpectReset after close); apply on the replica: %v; replica integrity %.80q; fingerprint at close %s… vs at open %s… -> full copy",
		ps, applyErr, ic, fp[:12], fp2[:12])
	if fp == fp2 {
		t.Fatal("fingerprint did not change")
	}
	pr.p.Cap.Pos = Position{LSN: pr.p.Cap.Pos.LSN}
	pr.fullCopy()
	upd(3000)
	pr.assertSame("after full copy (page_size 8192)")

	// VACUUM INTO as the base of a replica: pages are renumbered, and the
	// next page records corrupt it.
	pr.applyOn = false
	for i := range 200 {
		pr.write(func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO items(name, payload, created_at) VALUES(?, randomblob(400), 0)`, fmt.Sprintf("v%d", i))
			return err
		})
	}
	pr.write(func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM items WHERE id % 2 = 0`)
		return err
	})
	pr.applyOn = true
	vpath := filepath.Join(pr.dir, "vacuum-into.db")
	lsnV := pr.p.Cap.Pos.LSN
	if _, err := pr.p.DB.Writer().Exec(`VACUUM INTO ?`, vpath); err != nil {
		t.Fatal(err)
	}
	pr.r.DB.Close()
	rpath := filepath.Join(pr.dir, "r", "mestre.db")
	CopyFile(vpath, rpath)
	os.WriteFile(rpath+".lsn", fmt.Appendf(nil, "%d", lsnV), 0o600)
	pr.openReplica()
	for i := range 50 {
		pr.write(func(tx *sql.Tx) error {
			_, err := tx.Exec(`UPDATE items SET name = ? WHERE id = (SELECT max(id) FROM items)`, fmt.Sprintf("w%d", i))
			return err
		})
	}
	ic, _ = IntegrityCheck(pr.ctx, pr.r.DB.Reader())
	lp, _ := LogicalHash(pr.ctx, pr.p.DB.Reader())
	lr, _ := LogicalHash(pr.ctx, pr.r.DB.Reader())
	if len(ic) > 200 {
		ic = ic[:200] + "…"
	}
	t.Logf("replica based on VACUUM INTO, after 50 page records: integrity_check %q; logical equal %v", ic, lp == lr)
	if ic == "ok" && lp == lr {
		t.Fatal("expected VACUUM INTO to be an invalid base")
	}
}
