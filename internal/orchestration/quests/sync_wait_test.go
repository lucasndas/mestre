package quests_test

import (
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/waits"
)

// The step of a character on a Tower that cannot be synchronized is a real
// wait: of kind sync, with the 30 minutes of the table of waits, listed in
// `mestre waits`; and it ends when the branch gets there
// (docs/orquestracao/esperas-e-caixa-de-entrada.md#tempos-limite).
// CA-ESP-14
func TestTheStepThatWaitsForTheSyncIsAWaitWithItsTimeLimit(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	res, err := w.orc.Characters.Create(w.ctx, characters.CreateRequest{Name: "QA-1", Sheet: "QA Antigravity", Machine: "torre", Workspace: w.slug, Table: w.table.ID,
		Role: characters.RoleCharacter, Creator: characters.Creator{Kind: characters.CreatedByCharacter, ID: w.master.ID}})
	if err != nil {
		t.Fatal(err)
	}
	qa := res.Character // runs on the Tower
	if qa.Machine != "torre" {
		t.Fatalf("QA-1 runs on %s", qa.Machine)
	}
	w.peers.SetDown(true)
	q := w.run(w.create("name: Remota\nsteps:\n  - {id: a, character: QA-1}\n"))
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepPreparing})
	ws, err := w.orc.Waits.Store().ActiveOf(w.ctx, qa.ID)
	if err != nil || len(ws) != 1 || ws[0].Kind != waits.KindSync {
		t.Fatalf("waits of QA-1 = %+v, %v; want one of kind sync", ws, err)
	}
	if got := ws[0].TimeoutAt.Sub(ws[0].CreatedAt); got != 30*time.Minute {
		t.Errorf("the time limit of the sync is %v, want 30m", got)
	}
	w.peers.SetDown(false)
	if err := w.orc.Quests.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepRunning})
	if ws, _ := w.orc.Waits.Store().ActiveOf(w.ctx, qa.ID); len(ws) != 0 {
		t.Errorf("the wait for the sync is still active: %+v", ws)
	}
}
