package store_test

import (
	"path/filepath"
	"testing"

	"github.com/lucasndas/mestre/internal/store"
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
