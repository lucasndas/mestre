package tables_test

import (
	"slices"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
)

// charactersOf is a character of the table, as the observers are told of it.
func charactersOf(w *world, tbl tables.Table) characters.Character {
	return characters.Character{Workspace: w.slug(), Table: tbl.ID}
}

// The answer of `mestre pr` warns when a Tower of the table is disconnected.
func TestTheTowersThatAreDisconnectedAreListed(t *testing.T) {
	t.Parallel()
	w, _, tbl := towerWorld(t, nil)
	if got, err := w.svc.UnreachableTowers(w.ctx, w.slug(), tbl.ID); err != nil || len(got) != 0 {
		t.Fatalf("connected: %v, %v", got, err)
	}
	w.peers.setDown("torre-1", true)
	if got, err := w.svc.UnreachableTowers(w.ctx, w.slug(), tbl.ID); err != nil || !slices.Equal(got, []string{"torre-1"}) {
		t.Fatalf("disconnected: %v, %v", got, err)
	}
}

// Every character that starts on a Tower gets the commits of the Citadel
// before it begins: asking for the folder of the table there syncs it.
func TestTheFolderOfACharacterOnATowerIsBroughtToTheTipOfTheCitadel(t *testing.T) {
	t.Parallel()
	w, tw, tbl := towerWorld(t, nil)
	tip := commit(t, w.be, w.git, w.repoOf(tbl), "cidadela.go", "package cidadela\n", "feat: da cidadela")
	if err := w.svc.Done(w.ctx, charactersOf(w, tbl), ""); err != nil {
		t.Fatal(err)
	}
	dir, err := w.svc.Worktree(w.ctx, w.slug(), tbl.ID, "torre-1")
	if err != nil || dir != tw.dir(w.slug(), tbl.ID) {
		t.Fatalf("Worktree = %q, %v", dir, err)
	}
	if got := headOf(t, tw.repo(tbl.ID)); got != tip {
		t.Errorf("the Tower is at %s; want the tip of the Citadel %s", got.Short(), tip.Short())
	}
}
