package app

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/replication"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/pkg/clock"
)

// discardedEngine is the engine of a machine that stopped being the Citadel of
// the epoch 3 twice (the second archive finds the name taken and keeps the
// database under another one), with what goes with each database: the files
// the crash of a WAL leaves, the report of what was lost and the external
// actions dropped; and a full copy that was being sent and a transfer that was
// arriving. Every name comes from the engine's own functions.
func discardedEngine(t *testing.T) *Engine {
	t.Helper()
	e := &Engine{paths: DirPaths(t.TempDir()), clk: clock.NewFake(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)), log: slog.New(slog.DiscardHandler)}
	if err := e.paths.ensure(); err != nil {
		t.Fatal(err)
	}
	write := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("whole copy of the main database"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		db, err := guildtpl.Open(t.Context(), e.paths.guildDB(), store.Options{Clock: e.clk, Synchronous: "OFF"})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.archive(db, 3); err != nil {
			t.Fatal(err)
		}
	}
	for db := range e.discardedAll() {
		write(db + "-wal")
		write(db + "-shm")
	}
	write(strings.TrimSuffix(e.paths.discarded(3), ".db") + ".json")
	if err := saveDropped(e.paths.droppedActions(3), []string{"telegram"}); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(e.paths.snapshots(), "snapshot-01J9Z0.db"))
	write(filepath.Join(e.paths.transfers(), "01J9Z1.part"))
	return e
}

// banco lists what is in the folder of the database, folders by their names.
func banco(t *testing.T, e *Engine) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(e.paths.Banco, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(e.paths.Banco, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// CA-REPL-12
// What the Tower wipes when the machine leaves covers every file the engine
// keeps from its time as the Citadel, under the names it gives them
// (docs/guilda/replicacao.md#cofre-em-toda-máquina, "Sair da Guilda").
func TestWhatAMachineErasesWhenItLeavesCoversEveryFileOfItsDiscardedDatabases(t *testing.T) {
	t.Parallel()
	e := discardedEngine(t)
	if got := banco(t, e); len(got) < 10 {
		t.Fatalf("the set-up left only %v", got)
	}
	// the replica of the Tower lives where the main database was
	db, err := guilddb.Open(t.Context(), e.paths.guildDB(), store.Options{Clock: e.clk, Synchronous: "OFF", Replica: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r, err := replication.NewReplica(t.Context(), e.replicaConfig(db))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Wipe(t.Context(), "removed"); err != nil {
		t.Fatal(err)
	}
	if got := banco(t, e); len(got) != 0 {
		t.Errorf("the machine left the Guild and still has %v", got)
	}
}

// CA-REPL-13
// The databases an old Citadel discarded go after the retention, the ones kept
// under another name when the epoch was archived twice included, with what
// goes with each; the ones inside the retention stay.
func TestDiscardedDatabasesLeaveAfterTheRetentionTheRenamedOnesToo(t *testing.T) {
	t.Parallel()
	e := discardedEngine(t)
	old := e.clk.Now().Add(-DiscardedRetention - time.Hour)
	for db := range e.discardedAll() {
		if err := os.Chtimes(db, old, old); err != nil {
			t.Fatal(err)
		}
	}
	// a database of another epoch, still inside the retention
	fresh, err := guildtpl.Open(t.Context(), e.paths.guildDB(), store.Options{Clock: e.clk, Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.archive(fresh, 4); err != nil {
		t.Fatal(err)
	}
	young := e.clk.Now().Add(-DiscardedRetention + time.Hour)
	if err := os.Chtimes(e.paths.discarded(4), young, young); err != nil {
		t.Fatal(err)
	}

	e.pruneDiscarded()
	var left []string
	for _, f := range banco(t, e) {
		if strings.HasPrefix(f, "descartado-") {
			left = append(left, f)
		}
	}
	if len(left) != 1 || left[0] != "descartado-4.db" {
		t.Errorf("left after the retention: %v; want only descartado-4.db", left)
	}
}

type wipeFunc func(ctx context.Context, reason string) error

func (f wipeFunc) Wipe(ctx context.Context, reason string) error { return f(ctx, reason) }

// CA-REPL-12
// The report of the discarded databases reads the files the Wipe takes and
// writes its record next to them once the Citadel acknowledged: it stops, and
// is gone, before the Wipe erases, or it would write the record back
// (docs/guilda/replicacao.md#cofre-em-toda-máquina).
func TestATowerStopsReportingTheDiscardedDatabasesBeforeItErasesThem(t *testing.T) {
	t.Parallel()
	var order []string
	w := towerWiper{
		stopReport: func() { order = append(order, "stop the report") },
		replica:    wipeFunc(func(context.Context, string) error { order = append(order, "wipe"); return nil }),
	}
	if err := w.Wipe(t.Context(), "removed"); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "stop the report" || order[1] != "wipe" {
		t.Errorf("the Tower did %v, want the report stopped and then the wipe", order)
	}
}

// CA-REPL-13
// The retention counts from the day the database was discarded, not from the
// last write it had as the main database (the rename keeps that time): a
// Citadel that stopped weeks ago and comes back as a Tower would otherwise lose
// what it discarded at once, before it reported what the Guild lost.
func TestADiscardedDatabaseIsKeptForTheRetentionFromTheDayItWasDiscarded(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	e := &Engine{paths: DirPaths(t.TempDir()), clk: clk, log: slog.New(slog.DiscardHandler)}
	if err := e.paths.ensure(); err != nil {
		t.Fatal(err)
	}
	db, err := guildtpl.Open(t.Context(), e.paths.guildDB(), store.Options{Clock: clk, Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil { // closing writes to the file: the old time goes after
		t.Fatal(err)
	}
	stopped := clk.Now().Add(-90 * 24 * time.Hour)
	if err := os.Chtimes(e.paths.guildDB(), stopped, stopped); err != nil {
		t.Fatal(err)
	}
	if err := e.archive(db, 3); err != nil {
		t.Fatal(err)
	}
	kept := func() bool {
		_, err := os.Stat(e.paths.discarded(3))
		return err == nil
	}

	e.pruneDiscarded()
	if !kept() {
		t.Fatal("the database was removed the day it was discarded, for the date of its last write")
	}
	clk.Advance(DiscardedRetention - time.Hour)
	e.pruneDiscarded()
	if !kept() {
		t.Error("the database left before the retention")
	}
	clk.Advance(2 * time.Hour)
	e.pruneDiscarded()
	if !kept() {
		t.Error("the database left before it was reported")
	}
	if err := os.WriteFile(strings.TrimSuffix(e.paths.discarded(3), ".db")+".json", []byte("{}"), 0o600); err != nil { // the report was sent
		t.Fatal(err)
	}
	e.pruneDiscarded()
	if kept() {
		t.Error("the database is still there after the retention and the report")
	}
}

// CA-REPL-13
// An epoch archived twice has two databases, and one report: the report is of
// the first (the one under the plain name), and the copy kept under another name
// would otherwise make a report of its own, named after the copy, that the
// retention does not know to remove.
func TestAnEpochArchivedTwiceHasOneReportAndTheCopyKeepsItsOwnName(t *testing.T) {
	t.Parallel()
	e := discardedEngine(t)
	if got := len(e.discardedAll()); got != 2 {
		t.Fatalf("the set-up has %d databases of the epoch, want the two", got)
	}
	files := e.discardedFiles()
	if len(files) != 1 || files[3] != e.paths.discarded(3) {
		t.Errorf("databases to report = %v, want only %s for the epoch 3", files, e.paths.discarded(3))
	}
}

// A discarded database that was not reported to the new Citadel yet stays after
// the retention: the report is the audit of what the change of Citadel lost.
func TestADiscardedDatabaseThatWasNotReportedStaysAfterTheRetention(t *testing.T) {
	t.Parallel()
	e := discardedEngine(t)
	db, err := guildtpl.Open(t.Context(), e.paths.guildDB(), store.Options{Clock: e.clk, Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.archive(db, 5); err != nil {
		t.Fatal(err)
	}
	old := e.clk.Now().Add(-DiscardedRetention - time.Hour)
	if err := os.Chtimes(e.paths.discarded(5), old, old); err != nil {
		t.Fatal(err)
	}
	e.pruneDiscarded()
	if _, err := os.Stat(e.paths.discarded(5)); err != nil {
		t.Errorf("the database of the epoch 5, not reported, is gone: %v", err)
	}
}
