package quests_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
)

type deniedCeiling struct{ sheet string }

func (d deniedCeiling) Allows(_ context.Context, _, sheet, _ string) error {
	if sheet == d.sheet {
		return fmt.Errorf("%w: %s", characters.ErrCeiling, sheet)
	}
	return nil
}

// A step whose sheet is above the ceiling pauses the quest with the reason and
// tells the Master of the table, like the other failures of recruit.
func TestAStepWhoseSheetIsAboveTheCeilingPausesTheQuestAndTellsTheMaster(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{ceiling: deniedCeiling{sheet: "Dev Claude"}})
	q := w.run(w.create("name: Teto\nsteps:\n  - {id: a, sheet: Dev Claude, instructions: \"faça\"}\n"))
	got := w.quest(q.ID)
	if got.State != quests.Paused || !strings.Contains(got.PausedReason, "teto") {
		t.Fatalf("the quest: %s (%q)", got.State, got.PausedReason)
	}
	if !w.told(w.master, "acima do teto") {
		t.Error("the Master was not told")
	}
}
