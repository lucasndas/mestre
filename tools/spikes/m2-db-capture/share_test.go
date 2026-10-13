//go:build spike

package dbcapture

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"testing"
	"time"
)

func tryRead(open func(string) (*os.File, error), path string) string {
	f, err := open(path)
	if err != nil {
		return "open: " + err.Error()
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		return fmt.Sprintf("read after %d bytes: %v", len(b), err)
	}
	return fmt.Sprintf("ok (%d bytes)", len(b))
}

func size(path string) string {
	st, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "absent"
	}
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("%d bytes", st.Size())
}

// Q4: another handle reading the -wal and -shm while SQLite holds them, and
// a handle kept open across a checkpoint and across the close (which
// deletes the WAL).
func TestFileSharing(t *testing.T) {
	t.Logf("platform %s", osInfo())
	pr := newPair(t, "")
	ctx := pr.ctx
	pr.write(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO accounts(id, owner, balance, updated_at) VALUES(1, 'x', 0, 0)`)
		return err
	})
	path := pr.p.DB.Path()

	// A reader inside a read transaction and the writer inside a write
	// transaction (WAL write lock held, a page already written).
	rconn, _ := pr.p.DB.Reader().Conn(ctx)
	rtx, err := rconn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	rtx.QueryRow(`SELECT count(*) FROM accounts`).Scan(&n)
	wtx, err := pr.p.DB.Writer().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	wtx.Exec(`PRAGMA cache_size = 1`) // no effect inside a txn on the cache already in use
	for i := range 300 {
		if _, err := wtx.Exec(`INSERT INTO items(name, payload, created_at) VALUES(?, randomblob(1000), 0)`, fmt.Sprintf("open-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	files := []string{path + "-wal", path + "-shm"}
	if runtime.GOOS == "windows" {
		// On Linux, opening and closing the database file in this process
		// would drop SQLite's POSIX locks on it: never done.
		files = append(files, path)
	}
	for _, f := range files {
		t.Logf("with a read txn and a write txn open: %s: os.Open %s; share-delete %s", f[len(pr.dir):], tryRead(os.Open, f), tryRead(OpenShareDelete, f))
	}
	recs, err := pr.p.Cap.Capture()
	t.Logf("capture during the open write txn: %d records, %d frames read, %d uncommitted valid frames, err %v", len(recs), pr.p.Cap.LastFramesRead, pr.p.Cap.LastPartialTail, err)
	if err != nil || len(recs) != 0 {
		t.Fatal("capture during an open write txn must ship nothing")
	}
	if err := wtx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := pr.p.CaptureNow(); err != nil {
		t.Fatal(err)
	}
	rtx.Rollback()
	rconn.Close()
	pr.assertSame("after the open transactions")

	for _, h := range []struct {
		name string
		open func(string) (*os.File, error)
	}{{"os.Open", os.Open}, {"share-delete", OpenShareDelete}} {
		// A handle kept open across a TRUNCATE checkpoint.
		f, err := h.open(path + "-wal")
		if err != nil {
			t.Fatal(err)
		}
		busy, lg, ck, err := pr.p.Checkpoint(ctx, "TRUNCATE")
		t.Logf("[%s] WAL held open across checkpoint(TRUNCATE): err %v, busy %d log %d ckpt %d, WAL now %s", h.name, err, busy, lg, ck, size(path+"-wal"))
		f.Close()
		pr.write(func(tx *sql.Tx) error {
			_, err := tx.Exec(`UPDATE accounts SET balance = balance + 1 WHERE id = 1`)
			return err
		})
		pr.assertSame("[" + h.name + "] after checkpoint with the WAL held")

		// A handle kept open across the close, which deletes the WAL.
		f, err = h.open(path + "-wal")
		if err != nil {
			t.Fatal(err)
		}
		pos := pr.p.Cap.Pos
		t0 := time.Now()
		cerr := pr.p.Close()
		el := time.Since(t0)
		walAfter := size(path + "-wal")
		f.Close()
		t.Logf("[%s] WAL held open across Close: close took %v, err %v; WAL after close (handle still open) %s; after the handle closed %s", h.name, el.Round(time.Millisecond), cerr, walAfter, size(path+"-wal"))
		p2, err := OpenPrimary(ctx, path, migrations(t, 1), store0())
		if err != nil {
			t.Fatalf("[%s] reopen: %v", h.name, err)
		}
		p2.Cap.Pos = pos
		p2.Cap.Pos.ExpectReset = true
		p2.Ship = pr.ship
		pr.p = p2
		pr.write(func(tx *sql.Tx) error {
			_, err := tx.Exec(`UPDATE accounts SET balance = balance + 1 WHERE id = 1`)
			return err
		})
		pr.assertSame("[" + h.name + "] after close/reopen with the WAL held")
	}
}
