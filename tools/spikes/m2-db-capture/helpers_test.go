//go:build spike

package dbcapture

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/lucasndas/mestre/internal/store"
)

const (
	mig1 = `CREATE TABLE accounts (id INTEGER PRIMARY KEY, owner TEXT NOT NULL, balance INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT NOT NULL, payload BLOB, created_at INTEGER NOT NULL);
CREATE INDEX items_name ON items(name);
CREATE TABLE secrets (id TEXT PRIMARY KEY, ciphertext BLOB NOT NULL, updated_at INTEGER NOT NULL) WITHOUT ROWID;`
	mig2 = `CREATE TABLE notes (id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES items(id), body TEXT NOT NULL);
ALTER TABLE accounts ADD COLUMN status TEXT NOT NULL DEFAULT 'active';
CREATE INDEX accounts_owner ON accounts(owner);`
)

func migrations(t testing.TB, n int) store.Set {
	t.Helper()
	fsys := fstest.MapFS{"m/0001_base.sql": {Data: []byte(mig1)}}
	if n >= 2 {
		fsys["m/0002_notes.sql"] = &fstest.MapFile{Data: []byte(mig2)}
	}
	set, err := store.Load(fsys, "m")
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// pair is a primary and a replica fed synchronously by the shipped records.
type pair struct {
	t        testing.TB
	ctx      context.Context
	dir      string
	sync     string
	p        *Primary
	r        *Replica
	shipped  []*Record
	applyOn  bool
	rawDiffs int
}

func newPair(t testing.TB, syncLevel string) *pair {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "p"), 0o700)
	os.MkdirAll(filepath.Join(dir, "r"), 0o700)
	pr := &pair{t: t, ctx: ctx, dir: dir, sync: syncLevel, applyOn: true}
	p, err := OpenPrimary(ctx, filepath.Join(dir, "p", "mestre.db"), migrations(t, 1), store.Options{Synchronous: syncLevel})
	if err != nil {
		t.Fatal(err)
	}
	pr.p = p
	p.Ship = pr.ship
	pr.fullCopy()
	t.Cleanup(func() {
		if pr.r != nil {
			pr.r.DB.Close()
		}
		if pr.p != nil {
			pr.p.DB.Close()
		}
	})
	return pr
}

func (pr *pair) ship(r *Record) {
	pr.shipped = append(pr.shipped, r)
	if !pr.applyOn || pr.r == nil {
		return
	}
	if err := pr.r.Apply(pr.ctx, r); err != nil {
		pr.t.Fatalf("apply lsn %d: %v", r.LSN, err)
	}
}

// fullCopy takes a page-exact snapshot of the primary and replaces the
// replica with it (closing the replica first).
func (pr *pair) fullCopy() uint64 {
	pr.t.Helper()
	if pr.r != nil {
		pr.r.DB.Close()
		pr.r = nil
	}
	snap := filepath.Join(pr.dir, "snapshot.db")
	lsn, err := pr.p.Snapshot(pr.ctx, snap)
	if err != nil {
		pr.t.Fatal(err)
	}
	rpath := filepath.Join(pr.dir, "r", "mestre.db")
	if err := CopyFile(snap, rpath); err != nil {
		pr.t.Fatal(err)
	}
	os.WriteFile(rpath+".lsn", fmt.Appendf(nil, "%d", lsn), 0o600)
	pr.openReplica()
	return lsn
}

func (pr *pair) openReplica() {
	pr.t.Helper()
	r, err := OpenReplica(pr.ctx, filepath.Join(pr.dir, "r", "mestre.db"), migrations(pr.t, 2), store.Options{Synchronous: pr.sync})
	if err != nil {
		pr.t.Fatal(err)
	}
	pr.r = r
}

func (pr *pair) write(fn func(tx *sql.Tx) error) {
	pr.t.Helper()
	if err := pr.p.Write(pr.ctx, fn); err != nil {
		pr.t.Fatal(err)
	}
}

// assertSame checks integrity, logical identity and page identity.
func (pr *pair) assertSame(label string) {
	pr.t.Helper()
	ctx := pr.ctx
	if ic, err := IntegrityCheck(ctx, pr.r.DB.Reader()); err != nil || ic != "ok" {
		pr.t.Fatalf("%s: replica integrity_check = %q, %v", label, ic, err)
	}
	lp, err := LogicalHash(ctx, pr.p.DB.Reader())
	if err != nil {
		pr.t.Fatal(err)
	}
	lr, err := LogicalHash(ctx, pr.r.DB.Reader())
	if err != nil {
		pr.t.Fatal(err)
	}
	hp, np, err := MaskedPagesHash(ctx, pr.p.DB.Reader())
	if err != nil {
		pr.t.Fatal(err)
	}
	hr, nr, err := MaskedPagesHash(ctx, pr.r.DB.Reader())
	if err != nil {
		pr.t.Fatal(err)
	}
	if lp != lr {
		pr.t.Fatalf("%s: logical dump differs", label)
	}
	if hp != hr {
		diff, hdr, _ := DiffPages(ctx, pr.p.DB.Reader(), pr.r.DB.Reader())
		pr.t.Fatalf("%s: pages differ (%d vs %d pages): pages %v, header bytes %v", label, np, nr, diff, hdr)
	}
	rawP, _, _ := PagesHash(ctx, pr.p.DB.Reader())
	rawR, _, _ := PagesHash(ctx, pr.r.DB.Reader())
	raw := "raw identical"
	if rawP != rawR {
		_, hdr, _ := DiffPages(ctx, pr.p.DB.Reader(), pr.r.DB.Reader())
		raw = fmt.Sprintf("raw differs only in page-1 header bytes %v", hdr)
		pr.rawDiffs++
	}
	pr.t.Logf("%s: identical (%d pages, lsn %d, logical %s…, pages %s…; %s)", label, np, pr.r.LSN, lp[:12], hp[:12], raw)
}

func osInfo() string { return runtime.GOOS + "/" + runtime.GOARCH + " " + runtime.Version() }

var logMu sync.Mutex

func logf(t testing.TB, format string, args ...any) {
	logMu.Lock()
	defer logMu.Unlock()
	t.Logf(format, args...)
}

func storeOpts(sync string) store.Options { return store.Options{Synchronous: sync} }
