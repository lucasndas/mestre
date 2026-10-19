package quests_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/scrolls"
)

// In the real assembly the scrolls of a table are copied, read-only, into the
// worktree of the machine where its characters live: at each change, and for a
// character that is born (the item of a quest cites that path).
// CA-PERG-09
func TestTheScrollsOfATableAreCopiedToTheWorktreeOfItsCharacters(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	n, err := w.orc.Scrolls.Create(w.ctx, scrolls.CreateRequest{Workspace: w.slug, Table: w.table.ID, Actor: scrolls.Actor{Kind: scrolls.ByCharacter, ID: w.master.ID, Name: w.master.Name}, Content: "# Plano\numa"})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := w.orc.Tables.Worktree(w.ctx, w.slug, w.table.ID, w.master.Machine)
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(dir, filepath.FromSlash(scrolls.Rel(n.Table, n.ID)))
	if b, err := os.ReadFile(copyPath); err != nil || string(b) != "# Plano\numa" {
		t.Fatalf("the copy after the change = %q, %v", b, err)
	}
	// a copy lost is made again when a character is born
	if err := os.Remove(copyPath); err != nil {
		t.Fatal(err)
	}
	w.recruit("Dev Claude", "Dev Claude 1")
	if _, err := os.Stat(copyPath); err != nil {
		t.Fatalf("the copy was not made for the new character: %v", err)
	}
}
