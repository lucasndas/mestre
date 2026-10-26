//go:build full

package scrolls_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/scrolls"
)

func copyOf(w *world, machine string, n scrolls.Note) string {
	return filepath.Join(w.root, "wt", machine, n.Table, filepath.FromSlash(scrolls.Rel(n.Table, n.ID)))
}

// CA-PERG-05
func TestWhenAScrollChangesInTheCitadelTheCopyInTheWorktreeOfTheMachineIsUpdated(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOptions{
		copies: scrolls.LocalCopies{Machine: "m1"},
		targets: func(w *world) scrolls.Targets {
			return scrolls.CharacterTargets{Registry: w.reg, Tables: fakeTables{root: filepath.Join(w.root, "wt")}}
		},
	})
	dev := w.dev("Dev") // a character of the table, on the machine m1
	n := w.create(dev, "# Plano\nversão 1")
	copyPath := copyOf(w, "m1", n)
	read := func() string {
		t.Helper()
		b, err := os.ReadFile(copyPath)
		if err != nil {
			t.Fatalf("the copy: %v", err)
		}
		return string(b)
	}
	if got := read(); got != "# Plano\nversão 1" {
		t.Fatalf("the copy of the new scroll = %q", got)
	}
	if _, err := w.write(dev, "Plano", "# Plano\nversão 2", nil); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "# Plano\nversão 2" {
		t.Fatalf("after the change the copy is %q, want the new content", got)
	}
	// the copy is read-only for the characters, and what they do to it has no
	// effect: the next change writes it again
	if fi, err := os.Stat(copyPath); err != nil || fi.Mode().Perm()&0o222 != 0 { // READONLY on Windows
		t.Errorf("the copy is writable: %v %v", fi, err)
	}
	if err := os.Chmod(copyPath, 0o644); err != nil { // what an agent can do to its own copy
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, []byte("editado por fora"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Edit(w.ctx, scrolls.EditRequest{Workspace: workspace, Table: table, Actor: by(dev), Name: "Plano", Old: "versão 2", New: "versão 3"}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "# Plano\nversão 3" {
		t.Fatalf("the edit by hand was not overwritten: %q", got)
	}
	// the original in the main clone is the source and was never touched
	if b, _ := os.ReadFile(w.fileOf(n)); string(b) != "# Plano\nversão 3" {
		t.Errorf("source = %q", b)
	}
	// a copy lost is made again for the whole table
	if err := os.Chmod(copyPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(copyPath); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Sync(w.ctx, workspace, table); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "# Plano\nversão 3" {
		t.Errorf("after Sync the copy is %q", got)
	}
	// a machine that cannot be reached does not stop the change
	far := w.char(characters.RoleCharacter, table, "Longe")
	if _, err := w.db.Writer().Exec(`UPDATE characters SET machine_id = 'm2' WHERE id = ?`, far.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.write(dev, "Plano", "# Plano\nversão 4", nil); err != nil {
		t.Fatalf("a Tower that is away stopped a change: %v", err)
	}
	if got := read(); got != "# Plano\nversão 4" {
		t.Errorf("the copy of the machine that is here = %q", got)
	}
	// deleting takes the copy away too
	if err := w.svc.Delete(w.ctx, scrolls.DeleteRequest{Workspace: workspace, Table: table, Actor: user(characters.LevelOwner), Name: "Plano"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(copyPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the copy of a deleted scroll: %v", err)
	}
}

// A link in the path of the copy is never followed: the worktree is a folder the
// agents write.
func TestACopyIsNotWrittenThroughALinkInTheWorktree(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOptions{
		copies: scrolls.LocalCopies{Machine: "m1"},
		targets: func(w *world) scrolls.Targets {
			return scrolls.CharacterTargets{Registry: w.reg, Tables: fakeTables{root: filepath.Join(w.root, "wt")}}
		},
	})
	dev := w.dev("Dev")
	outside := t.TempDir()
	wt := filepath.Join(w.root, "wt", "m1", table)
	if err := os.MkdirAll(filepath.Join(wt, ".mestre"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(wt, ".mestre", "mesas")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	n := w.create(dev, "# Plano")
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("a copy was written through the link: %v", entries)
	}
	if _, err := os.Stat(w.fileOf(n)); err != nil {
		t.Errorf("the source was not written: %v", err)
	}
}
