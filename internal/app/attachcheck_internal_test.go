package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func present(p string) bool { _, err := os.Stat(p); return err == nil }

// A file that is not what the channel kept (size and SHA-256 of the row) is not
// spread to the tables, one that is missing does not keep the others of the
// table from being copied, and a copy that is there is not read again.
func TestACopyChecksTheFileAgainstItsRowAndOneMissingFileDoesNotStopTheOthers(t *testing.T) {
	t.Parallel()
	r := newAttachRig(t)
	const a, b, c = ".mestre/anexos/2026/10/a-1.png", ".mestre/anexos/2026/10/b-2.png", ".mestre/anexos/2026/10/c-3.png"
	r.keep(t, a, "AAA")
	r.keep(t, b, "BBB")
	r.keep(t, c, "CCC")
	principal := filepath.Join(r.root, attWS, "principal")
	if err := os.Remove(filepath.Join(principal, filepath.FromSlash(a))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(principal, filepath.FromSlash(b)), []byte("XXX"), 0o644); err != nil { // changed after it was kept
		t.Fatal(err)
	}
	if err := r.copies.mark(t.Context(), attWS, attTable, []string{a, b, c}); err != nil {
		t.Fatal(err)
	}
	dir := func(_, table string) string { return filepath.Join(filepath.Dir(r.towerDir), table) }
	if err := r.copies.catchUp(t.Context(), dir); err == nil {
		t.Error("a file that is not the one the channel kept was copied without a word")
	}
	if !present(filepath.Join(r.towerDir, filepath.FromSlash(c))) {
		t.Error("the file after a missing one was not copied")
	}
	if present(filepath.Join(r.towerDir, filepath.FromSlash(b))) {
		t.Error("a changed file was spread to the table")
	}
	// For reads no attachment whose copy is there
	r.copies.dirOf = func(_ context.Context, _, _ string) string { return r.towerDir }
	got, err := r.copies.For(t.Context(), attWS, attTable)
	if err != nil || len(got) != 0 {
		t.Errorf("For = %+v, %v", got, err)
	}
}
