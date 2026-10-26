package quests_test

import (
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/quests"
)

// Two dones of the same character at the same time record one done: the second
// finds no step.
func TestTwoSimultaneousDonesOfACharacterRecordOne(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.recruit("Dev Claude", "Dev Claude 1")
	w.run(w.create(exampleDef))
	c := w.char("Arquiteto Claude 1")
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = w.orc.Quests.Finish(w.ctx, quests.DoneRequest{Character: c, Text: "feito"})
		}()
	}
	wg.Wait()
	dones, err := w.orc.Registry.Dones(w.ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(dones) != 1 {
		t.Fatalf("dones = %d; want 1", len(dones))
	}
}
