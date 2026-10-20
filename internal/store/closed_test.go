package store_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/pkg/clock"
)

// A database that was closed (a machine that left the Guild wipes its
// replica while the loop of the replica still runs) answers with an error,
// never with a nil pool.
func TestAClosedDatabaseAnswersWithAnError(t *testing.T) {
	t.Parallel()
	db := open(t, filepath.Join(t.TempDir(), "x.db"), load(t, createItems), store.Options{})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer().ExecContext(t.Context(), `INSERT INTO items (id, name) VALUES (1, 'a')`); err == nil {
		t.Error("a closed database wrote")
	}
	if err := db.Reader().PingContext(t.Context()); err == nil {
		t.Error("a closed database was read")
	}
}

// Close returns once the file is free: a query that was running keeps its
// connection, and on Windows the file with it, until it ends. The wait ends
// with the query, not with the bound.
func TestCloseWaitsForTheQueryThatWasRunning(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	db := open(t, filepath.Join(t.TempDir(), "x.db"), load(t, createItems), store.Options{Clock: clk})
	rows, err := db.Reader().QueryContext(t.Context(), `SELECT 1`)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- db.Close() }()
	clk.BlockUntil(1) // Close is waiting for the connection of the query
	select {
	case <-done:
		t.Fatal("Close returned while a query held the file")
	default:
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// A query that never ends does not hold the close for good: Close says how
// many connections are still open, and a second Close does not wait again.
func TestCloseGivesUpOnAQueryThatNeverEnds(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	db := open(t, filepath.Join(t.TempDir(), "x.db"), load(t, createItems), store.Options{Clock: clk})
	rows, err := db.Reader().QueryContext(t.Context(), `SELECT 1`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rows.Close() })
	done := make(chan error, 1)
	go func() { done <- db.Close() }()
	clk.BlockUntil(1)
	clk.Advance(time.Minute)
	if err := <-done; err == nil || !strings.Contains(err.Error(), "1 connection(s) still open") {
		t.Fatalf("Close = %v, want the count of open connections", err)
	}
	if err := db.Close(); err != nil { // returns at once: the fake clock does not move
		t.Fatalf("second Close = %v", err)
	}
}

// While Close waits, a goroutine that still holds a query can ask for the
// pools: the wait is not under the lock.
func TestCloseWaitsWithoutHoldingTheLockOfThePools(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	db := open(t, filepath.Join(t.TempDir(), "x.db"), load(t, createItems), store.Options{Clock: clk})
	rows, err := db.Reader().QueryContext(t.Context(), `SELECT 1`)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- db.Close() }()
	clk.BlockUntil(1)
	if db.Writer() == nil || db.Reader() == nil { // would block under the lock
		t.Fatal("no pool")
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// A transaction that is open when the database closes holds the file too.
func TestCloseWaitsForAnOpenTransaction(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	db := open(t, filepath.Join(t.TempDir(), "x.db"), load(t, createItems), store.Options{Clock: clk})
	tx, err := db.Reader().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- db.Close() }()
	clk.BlockUntil(1)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
