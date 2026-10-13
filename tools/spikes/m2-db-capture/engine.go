//go:build spike

package dbcapture

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/lucasndas/mestre/internal/store"
)

// Primary is the Citadel side: the production store with automatic
// checkpoints off and a capturer that runs after every commit.
type Primary struct {
	DB  *store.DB
	Cap *Capturer
	// Ship receives every captured record, in order.
	Ship func(*Record)
}

// OpenPrimary opens path with internal/store and turns automatic
// checkpoints off on its single writer connection.
func OpenPrimary(ctx context.Context, path string, set store.Set, opts store.Options) (*Primary, error) {
	db, err := store.Open(ctx, path, set, opts)
	if err != nil {
		return nil, err
	}
	if _, err := db.Writer().ExecContext(ctx, `PRAGMA wal_autocheckpoint = 0`); err != nil {
		db.Close()
		return nil, err
	}
	return &Primary{DB: db, Cap: &Capturer{WALPath: path + "-wal"}}, nil
}

// Write runs fn in one write transaction and ships what it committed.
func (p *Primary) Write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := p.DB.Writer().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return p.CaptureNow()
}

// CaptureNow reads and ships whatever is committed and not yet shipped.
func (p *Primary) CaptureNow() error {
	recs, err := p.Cap.Capture()
	if err != nil {
		return err
	}
	for _, r := range recs {
		if p.Ship != nil {
			p.Ship(r)
		}
	}
	return nil
}

// Checkpoint is the only checkpoint the engine runs: capture first, then
// checkpoint, holding the single writer so no commit slips in between.
func (p *Primary) Checkpoint(ctx context.Context, mode string) (busy, logFrames, ckpt int, err error) {
	conn, err := p.DB.Writer().Conn(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	defer conn.Close()
	if err := p.CaptureNow(); err != nil {
		return 0, 0, 0, err
	}
	if err := conn.QueryRowContext(ctx, `PRAGMA wal_checkpoint(`+mode+`)`).Scan(&busy, &logFrames, &ckpt); err != nil {
		return 0, 0, 0, err
	}
	p.Cap.Pos.ExpectReset = true
	return busy, logFrames, ckpt, nil
}

// Close captures the last commits, then closes: SQLite checkpoints and
// deletes the WAL when the last connection closes.
func (p *Primary) Close() error {
	if err := p.CaptureNow(); err != nil {
		p.DB.Close()
		return err
	}
	p.Cap.Pos.ExpectReset = true
	return p.DB.Close()
}

// Snapshot writes a page-exact copy of the database to dst, consistent with
// the last shipped record: it holds the writer (no commit can happen), ships
// what is pending, and reads every page through sqlite_dbpage. It returns
// the lsn the copy is at. VACUUM INTO cannot be used: it renumbers pages, and
// the page images of later records would land on the wrong pages.
func (p *Primary) Snapshot(ctx context.Context, dst string) (uint64, error) {
	conn, err := p.DB.Writer().Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil) // BEGIN IMMEDIATE (store's _txlock)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := p.CaptureNow(); err != nil {
		return 0, err
	}
	lsn := p.Cap.Pos.LSN
	if err := dumpPages(ctx, tx, dst); err != nil {
		return 0, err
	}
	return lsn, nil
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func dumpPages(ctx context.Context, q querier, dst string) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	rows, err := q.QueryContext(ctx, `SELECT pgno, data FROM sqlite_dbpage ORDER BY pgno`)
	if err != nil {
		f.Close()
		return err
	}
	defer rows.Close()
	want := int64(1)
	for rows.Next() {
		var pgno int64
		var data []byte
		if err := rows.Scan(&pgno, &data); err != nil {
			f.Close()
			return err
		}
		if pgno != want {
			f.Close()
			return fmt.Errorf("sqlite_dbpage: page %d, want %d", pgno, want)
		}
		want++
		if _, err := f.Write(data); err != nil {
			f.Close()
			return err
		}
	}
	if err := rows.Err(); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Replica is the Tower side: the production store opened as a replica; its
// writer only applies records.
type Replica struct {
	DB      *store.DB
	LSN     uint64 // last record applied
	PosFile string // the applied lsn, written after each commit
	// Crash points for the failure tests.
	BeforeCommit func()
	AfterCommit  func()
}

// OpenReplica opens the copy at path as a replica and reads the lsn file.
func OpenReplica(ctx context.Context, path string, set store.Set, opts store.Options) (*Replica, error) {
	opts.Replica = true
	db, err := store.Open(ctx, path, set, opts)
	if err != nil {
		return nil, err
	}
	r := &Replica{DB: db, PosFile: path + ".lsn"}
	if b, err := os.ReadFile(r.PosFile); err == nil {
		fmt.Sscan(string(b), &r.LSN)
	}
	return r, nil
}

// ErrOutOfOrder is a record that does not follow the last applied one.
type ErrOutOfOrder struct{ Have, Got uint64 }

func (e *ErrOutOfOrder) Error() string {
	return fmt.Sprintf("replica: record %d after %d (gap: ask again from %d)", e.Got, e.Have, e.Have+1)
}

// Apply writes the page images of rec in one transaction. A repeated record
// (lsn <= applied) is skipped; a gap is refused. Re-applying the record that
// follows the stored lsn is idempotent: page images are absolute.
func (r *Replica) Apply(ctx context.Context, rec *Record) error {
	if rec.LSN <= r.LSN {
		return nil
	}
	if rec.LSN != r.LSN+1 {
		return &ErrOutOfOrder{Have: r.LSN, Got: rec.LSN}
	}
	if err := ApplyPages(ctx, r.DB.Writer(), rec, r.BeforeCommit); err != nil {
		return err
	}
	if r.AfterCommit != nil {
		r.AfterCommit()
	}
	r.LSN = rec.LSN
	return os.WriteFile(r.PosFile, fmt.Appendf(nil, "%d", r.LSN), 0o600)
}

// ApplyPages writes the pages of rec through sqlite_dbpage in one
// transaction and truncates the database to rec.DBSize pages. beforeCommit,
// when set, runs before COMMIT (the crash test blocks there).
func ApplyPages(ctx context.Context, w *sql.DB, rec *Record, beforeCommit func()) error {
	tx, err := w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	pgnos := make([]uint32, 0, len(rec.Pages))
	for pg := range rec.Pages {
		pgnos = append(pgnos, pg)
	}
	sort.Slice(pgnos, func(i, j int) bool { return pgnos[i] < pgnos[j] })
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO sqlite_dbpage(pgno, data) VALUES(?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, pg := range pgnos {
		if pg > rec.DBSize {
			continue // a page beyond the end after this commit (truncated)
		}
		if _, err := stmt.ExecContext(ctx, int64(pg), rec.Pages[pg]); err != nil {
			return fmt.Errorf("apply page %d: %w", pg, err)
		}
	}
	var have int64
	if err := tx.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&have); err != nil {
		return err
	}
	if have > int64(rec.DBSize) {
		if _, err := stmt.ExecContext(ctx, int64(rec.DBSize)+1, nil); err != nil {
			return fmt.Errorf("truncate to %d pages: %w", rec.DBSize, err)
		}
	}
	if beforeCommit != nil {
		beforeCommit()
	}
	return tx.Commit()
}

// CopyFile copies a closed database file (a snapshot) into place.
func CopyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	for _, s := range []string{"-wal", "-shm"} {
		os.Remove(dst + s)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o600)
}
