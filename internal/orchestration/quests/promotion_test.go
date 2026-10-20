package quests_test

import (
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/protocol"
)

// CA-CIDADELA-11: with characters alive on a Tower and others on the machine of
// the old Citadel, each with a step under way, when the new Citadel takes over
// (new services over the replicated database) and takes what each machine tells
// (Service.Reconcile, as the connection of a Tower and the start of the period
// do), only the characters of the old Citadel's machine are interrupted, and
// only their steps are delivered again, with the note, once they are resumed
// (Service.ResumeMachine).
func TestAfterAPromotionOnlyTheStepsOfTheInterruptedCharactersAreDeliveredAgain(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	q := w.run(w.create(`
name: Dois lados
steps:
  - {id: local,  sheet: Dev Claude}
  - {id: remoto, sheet: QA Antigravity, machine: windows-pc}
`))
	local, remote := w.char("Dev Claude 1"), w.char("QA Antigravity 1")
	if local.Machine != "cidadela" || remote.Machine != "torre" {
		t.Fatalf("machines: %s, %s", local.Machine, remote.Machine)
	}
	beforeLocal, beforeRemote := len(w.machines.Delivered(local)), len(w.machines.Delivered(remote))
	startCommit := func(step string) string {
		rs, _ := w.orc.Quests.Store().Runs(w.ctx, q.ID+"/"+step)
		return rs[0].StartCommit
	}
	startLocal := startCommit("local")

	// The new Citadel takes over and finds the terminals of the Tower alive and
	// the ones of the machine of the old Citadel gone.
	w.restart()
	live := []protocol.TerminalState{{TerminalID: remote.TerminalID, AgentID: remote.ID, State: string(characters.Busy)}}
	if err := w.orc.Characters.Reconcile(w.ctx, "torre", live); err != nil {
		t.Fatal(err)
	}
	if err := w.orc.Characters.Reconcile(w.ctx, "cidadela", nil); err != nil {
		t.Fatal(err)
	}
	if w.get(local.ID).State != characters.Interrupted || w.get(remote.ID).State != characters.Busy {
		t.Fatalf("states: %s, %s", w.get(local.ID).State, w.get(remote.ID).State)
	}
	// The interrupted step waits to be delivered again; the other is untouched.
	if st := w.step(q, "local"); st.State != quests.StepPreparing || st.ResumeNote != "retomada após reinício" {
		t.Errorf("the step of the interrupted character: %+v", st)
	}
	if st := w.step(q, "remoto"); st.State != quests.StepRunning {
		t.Errorf("the step of the live character: %+v", st)
	}
	if len(w.machines.Delivered(local)) != beforeLocal {
		t.Error("the step was delivered again before the character was back")
	}
	if err := w.orc.Characters.ResumeMachine(w.ctx, "cidadela"); err != nil {
		t.Fatal(err)
	}
	w.wantStates(q, map[string]quests.StepState{"local": quests.StepRunning, "remoto": quests.StepRunning})
	again := w.machines.Delivered(local)
	if len(again) != beforeLocal+1 || !strings.Contains(again[len(again)-1], "retomada após reinício") || !strings.Contains(again[len(again)-1], "Faça") && !strings.Contains(again[len(again)-1], "Etapa") {
		t.Errorf("the step was not delivered again with the note: %q", again)
	}
	if len(w.machines.Delivered(remote)) != beforeRemote {
		t.Error("the step of the live character was delivered again")
	}
	if runs, _ := w.orc.Quests.Store().Runs(w.ctx, q.ID+"/local"); len(runs) != 1 || runs[0].StartCommit != startLocal {
		t.Errorf("the delivery again made a run or moved the start: %+v", runs)
	}
	// and it ends normally
	w.finish("Dev Claude 1", "fim", nil, "")
	w.wantStates(q, map[string]quests.StepState{"local": quests.StepCompleted})
}
