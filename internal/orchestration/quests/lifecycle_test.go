package quests_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/connections"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/protocol"
)

// told says whether an item with the text was put in the inbox of the
// character, delivered or still waiting for it to be free.
func (w *world) told(c characters.Character, sub string) bool {
	items, err := w.orc.Inbox.Store().All(w.ctx, c.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	for _, it := range items {
		if strings.Contains(it.Text, sub) {
			return true
		}
	}
	return false
}

const oneStep = `
name: Sozinha
steps:
  - {id: a, sheet: Dev Claude, instructions: "Faça"}
  - {id: b, sheet: Revisor Codex, after: [a]}
`

// CA-MIS-04: with the clock fake, the character of a step stays 20 minutes
// idle with no done: the step is stuck and the Master of the table is told; 30
// minutes later the Grand Master and the users are told too. A new turn takes
// the step back to running.
func TestAStepWhoseCharacterIsIdleTooLongIsStuckAndEscalatesAfterThirtyMinutes(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	q := w.run(w.create(oneStep))
	dev := w.char("Dev Claude 1")
	w.machines.Report(w.ctx, dev.ID, characters.Idle) // the turn ended and it never gave its done

	w.clk.Advance(19*time.Minute + 59*time.Second)
	if err := w.orc.Quests.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepRunning})
	if w.told(w.master, "travada") {
		t.Fatal("the Master was told before the 20 minutes")
	}
	w.clk.Advance(time.Second)
	if err := w.orc.Quests.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepStuck})
	if !w.told(w.master, "A etapa a da missão Sozinha está travada") {
		t.Errorf("the Master was not told: %q", w.machines.Delivered(w.master))
	}
	if gmTold := w.told(w.gm, "travada"); gmTold || len(w.users.Told()) != 0 {
		t.Fatalf("the Grand Master or the users were told at once")
	}

	w.clk.Advance(29*time.Minute + 59*time.Second)
	_ = w.orc.Quests.Sweep(w.ctx)
	if w.told(w.gm, "travada") || len(w.users.Told()) != 0 {
		t.Fatal("escalated before the 30 minutes")
	}
	w.clk.Advance(time.Second)
	_ = w.orc.Quests.Sweep(w.ctx)
	if !w.told(w.gm, "segue travada") || len(w.users.Told()) != 1 {
		t.Errorf("30 minutes later: Grand Master %q, users %q", w.machines.Delivered(w.gm), w.users.Told())
	}
	_ = w.orc.Quests.Sweep(w.ctx) // told once
	if len(w.users.Told()) != 1 {
		t.Errorf("the users were told again: %q", w.users.Told())
	}

	// A new turn of the character: the step is running again; its done ends it.
	w.machines.Report(w.ctx, dev.ID, characters.Busy)
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepRunning})
	w.finish("Dev Claude 1", "terminei", nil, "")
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepCompleted, "b": quests.StepRunning})
}

// The step of a character that was stuck by the provider is stuck at once, and
// a done takes it back.
func TestAStepWhoseCharacterIsStuckIsStuckAndADoneEndsIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	q := w.run(w.create(oneStep))
	dev := w.char("Dev Claude 1")
	w.machines.Report(w.ctx, dev.ID, characters.Stuck)
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepStuck})
	if !w.told(w.master, "travada") {
		t.Error("the Master was not told")
	}
	w.finish("Dev Claude 1", "saí", nil, "")
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepCompleted})
}

// CA-MIS-06: with a fake GitHub, when the quest is made there is the issue
// under the epic with the steps as a checklist, and the sub-issue of the step
// that asks for one; when `dev` ends, the sub-issue is closed as completed and
// the summary is commented with the App of the class of the character.
func TestTheQuestIsMirroredOnGitHubWithTheIssueTheSubIssueAndTheComment(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	v := w.create(`
name: Espelhada
steps:
  - {id: dev, sheet: Dev Claude, sub_issue: true}
  - {id: review, sheet: Revisor Codex, after: [dev]}
`)
	if v.Issue == 0 {
		t.Fatal("no issue was made")
	}
	issue := w.hub.get(v.Issue)
	if issue.Parent != 42 || !strings.Contains(issue.Body, "- [ ] 1. dev") || !strings.Contains(issue.Body, "- [ ] 2. review") {
		t.Fatalf("the issue: %+v", issue)
	}
	steps, _ := w.orc.Quests.Store().Steps(w.ctx, v.ID)
	sub := steps[0].SubIssue
	if sub == 0 || steps[1].SubIssue != 0 {
		t.Fatalf("sub-issues: %+v", steps)
	}
	if got := w.hub.get(sub); got.Parent != v.Issue || got.Closed != "" || !strings.Contains(got.Title, "dev") {
		t.Fatalf("the sub-issue: %+v", got)
	}

	w.run(v)
	w.finish("Dev Claude 1", "Login feito", map[string]string{"src/login.go": "x"}, "")
	if got := w.hub.get(sub); got.Closed != "completed" || len(got.Comments) != 1 || got.Comments[0].As != "Dev" || !strings.Contains(got.Comments[0].Body, "Login feito") ||
		!strings.Contains(got.Comments[0].Body, "A src/login.go") {
		t.Errorf("the sub-issue after dev: %+v", got)
	}
	if got := w.hub.get(v.Issue); !strings.Contains(got.Body, "- [x] 1. dev") || !strings.Contains(got.Body, "- [ ] 2. review") || got.Closed != "" {
		t.Errorf("the checklist of the issue after dev: %+v", got)
	}
}

// GitHub that does not answer does not stop the quest, and what was left is
// done on the next sweep, once.
func TestAGitHubThatFailsDoesNotStopTheQuestAndTheMirrorCatchesUp(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.hub.setFail(true)
	v := w.create(`
name: Espelhada
steps:
  - {id: dev, sheet: Dev Claude, sub_issue: true}
`)
	if v.Issue != 0 {
		t.Fatalf("an issue was made with GitHub down: %d", v.Issue)
	}
	q := w.run(v)
	w.finish("Dev Claude 1", "feito", nil, "")
	if got := w.quest(q.ID); got.State != quests.Completed {
		t.Fatalf("the quest is %s; GitHub down must not stop it", got.State)
	}
	w.hub.setFail(false)
	if err := w.orc.Quests.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	got := w.quest(q.ID)
	steps, _ := w.orc.Quests.Store().Steps(w.ctx, q.ID)
	if got.Issue == 0 || steps[0].SubIssue == 0 {
		t.Fatalf("the mirror did not catch up: %+v / %+v", got, steps)
	}
	sub := w.hub.get(steps[0].SubIssue)
	if sub.Closed != "completed" || len(sub.Comments) != 1 {
		t.Errorf("the sub-issue: %+v", sub)
	}
	before := len(w.hub.calls)
	_ = w.orc.Quests.Sweep(w.ctx)
	if len(w.hub.calls) != before {
		t.Errorf("GitHub was called again for nothing: %v", w.hub.calls[before:])
	}
}

// A cancelled quest closes its issue and its open sub-issues as not planned; a
// completed one run again keeps the same issue and sub-issues, with a comment
// per round.
func TestACancelledQuestClosesItsIssuesAndANewRoundCommentsTheSameOnes(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	v := w.create(`
name: Duas
steps:
  - {id: a, sheet: Dev Claude, sub_issue: true}
  - {id: b, sheet: Revisor Codex, after: [a], sub_issue: true}
`)
	q := w.run(v)
	w.finish("Dev Claude 1", "a feito", nil, "")
	w.finish("Revisor Codex 1", "b feito", nil, "")
	if got := w.quest(q.ID); got.State != quests.Completed {
		t.Fatalf("round 1: %s", got.State)
	}
	steps, _ := w.orc.Quests.Store().Steps(w.ctx, q.ID)
	subA, subB := steps[0].SubIssue, steps[1].SubIssue

	// A second round: the same issue, the same sub-issues, one comment each.
	q = w.run(v)
	if q.Round != 2 {
		t.Fatalf("round = %d", q.Round)
	}
	w.finish("Dev Claude 1", "a de novo", nil, "")
	steps, _ = w.orc.Quests.Store().Steps(w.ctx, q.ID)
	if steps[0].SubIssue != subA || steps[1].SubIssue != subB || len(w.hub.issues) != 3 {
		t.Errorf("sub-issues were made again: %+v / %d issues", steps, len(w.hub.issues))
	}
	if got := w.hub.get(subA); len(got.Comments) != 2 || !strings.Contains(got.Comments[1].Body, "a de novo") {
		t.Errorf("sub-issue of a: %+v", got)
	}
	var roundComment bool
	for _, c := range w.hub.get(v.Issue).Comments {
		roundComment = roundComment || strings.Contains(c.Body, "Rodada 2")
	}
	if !roundComment {
		t.Errorf("the issue has no comment of the round: %+v", w.hub.get(v.Issue))
	}

	// b is still waiting: the quest is cancelled and what is open is closed as not planned.
	if _, err := w.orc.Quests.Cancel(w.ctx, q.ID, "mudou o plano"); err != nil {
		t.Fatal(err)
	}
	if got := w.hub.get(v.Issue); got.Closed != "not_planned" {
		t.Errorf("the issue: %+v", got)
	}
	if got := w.hub.get(subB); got.Closed != "not_planned" && got.Closed != "completed" {
		t.Errorf("the sub-issue of b: %+v", got)
	}
}

// CA-CONX-02: Dev -> Reviewer with a return to the Dev: when the quest is made
// there are the connections Dev <-> Reviewer and the ones with the scroll of
// the quest; when it ends they go away if nothing else keeps them.
func TestTheConnectionsOfAQuestExistFromItsCreationAndGoWhenItEnds(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev Claude", "Dev Claude 1")
	rev := w.recruit("Revisor Codex", "Revisor Codex 1")
	// The characters of the table are connected to the Master, not to each other.
	reasons := func(a, b connections.Ref) string {
		t.Helper()
		cs, err := w.orc.Connections.Store().Of(w.ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cs {
			if c.A == b || c.B == b {
				var rs []string
				for _, r := range c.Reasons {
					rs = append(rs, r.Name)
				}
				return strings.Join(rs, ",")
			}
		}
		return ""
	}
	if got := reasons(connections.Char(dev.ID), connections.Char(rev.ID)); got != "" {
		t.Fatalf("Dev and Reviewer were connected before the quest: %q", got)
	}
	v := w.create(`
name: Revisao
steps:
  - {id: dev,    character: Dev Claude 1}
  - {id: review, character: Revisor Codex 1, after: [dev], can_return_to: dev}
`)
	reason := connections.QuestReason(v.ID)
	scroll := connections.Scroll(v.Scroll)
	if v.Scroll == "" {
		t.Fatal("the quest has no scroll")
	}
	for name, pair := range map[string][2]connections.Ref{
		"Dev and Reviewer":    {connections.Char(dev.ID), connections.Char(rev.ID)},
		"Dev and scroll":      {connections.Char(dev.ID), scroll},
		"Reviewer and scroll": {connections.Char(rev.ID), scroll},
	} {
		if got := reasons(pair[0], pair[1]); got != reason {
			t.Errorf("%s after the quest was made: %q; want %q", name, got, reason)
		}
	}
	// The Dev can ask the Reviewer by name, and read the scroll of the quest.
	if _, err := w.orc.Connections.Target(w.ctx, dev.ID, "", "Revisor Codex 1"); err != nil {
		t.Errorf("Dev cannot reach the Reviewer: %v", err)
	}
	if ok, err := w.orc.Connections.CanSeeScroll(w.ctx, dev.ID, v.Scroll); err != nil || !ok {
		t.Errorf("Dev cannot see the scroll of the quest: %v, %v", ok, err)
	}

	q := w.run(v)
	w.finish("Dev Claude 1", "feito", nil, "")
	w.finish("Revisor Codex 1", "ok", nil, "")
	if got := w.quest(q.ID); got.State != quests.Completed {
		t.Fatalf("the quest is %s", got.State)
	}
	for name, pair := range map[string][2]connections.Ref{
		"Dev and Reviewer":    {connections.Char(dev.ID), connections.Char(rev.ID)},
		"Dev and scroll":      {connections.Char(dev.ID), scroll},
		"Reviewer and scroll": {connections.Char(rev.ID), scroll},
	} {
		if got := reasons(pair[0], pair[1]); got != "" {
			t.Errorf("%s after the quest ended: %q; want no connection", name, got)
		}
	}
}

// The characters the engine makes for the steps are connected as the steps are:
// the one of a step with the one of the step it follows and the one it may
// return to, and each of them with the scroll.
func TestTheCharactersOfTheStepsAreConnectedAsTheStepsAreAndTheirSuperiorIsTheMaster(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	q := w.run(w.create(reviewDef))
	w.finish("Dev Claude 1", "feito", nil, "")
	dev, rev := w.char("Dev Claude 1"), w.char("Revisor Codex 1")
	if _, err := w.orc.Connections.Target(w.ctx, rev.ID, "", "Dev Claude 1"); err != nil {
		t.Errorf("the Reviewer cannot reach the Dev: %v", err)
	}
	if ok, _ := w.orc.Connections.CanSeeScroll(w.ctx, rev.ID, w.quest(q.ID).Scroll); !ok {
		t.Error("the Reviewer cannot see the scroll of the quest")
	}
	// Made by the engine for a step: the superior in the chain of approval is
	// the Master of the table (docs/orquestracao/cadeia-de-aprovacao.md#superior).
	for _, c := range []characters.Character{rev, w.get(dev.ID)} {
		if c.Name == "Dev Claude 1" {
			continue // the Dev is persistent, but the engine made it for the step too
		}
		sup, err := w.orc.Characters.Superior(w.ctx, c.ID)
		if err != nil || sup.People || sup.ID != w.master.ID {
			t.Errorf("the superior of %s = %+v, %v; want the Master of the table", c.Name, sup, err)
		}
	}
	if sup, err := w.orc.Characters.Superior(w.ctx, dev.ID); err != nil || sup.ID != w.master.ID {
		t.Errorf("the superior of the Dev = %+v, %v", sup, err)
	}
}

// Pausing lets the step under way finish and starts no other; resuming goes on.
// Cancelling lets it finish too, then ends the characters of the quest.
func TestPauseLetsTheStepFinishAndResumeGoesOnAndCancelEndsTheCharacters(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	q := w.run(w.create(oneStep))
	if _, err := w.orc.Quests.Pause(w.ctx, q.ID); err != nil {
		t.Fatal(err)
	}
	w.finish("Dev Claude 1", "terminei durante a pausa", nil, "")
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepCompleted, "b": quests.StepPending})
	if len(w.machines.Started()) != 3 { // Grão-Mestre, Mestre da mesa, Dev
		t.Errorf("a character was made while paused: %d started", len(w.machines.Started()))
	}
	if _, err := w.orc.Quests.Pause(w.ctx, q.ID); err == nil {
		t.Error("pausing a paused quest worked")
	}
	if _, err := w.orc.Quests.Resume(w.ctx, q.ID); err != nil {
		t.Fatal(err)
	}
	w.wantStates(q, map[string]quests.StepState{"b": quests.StepRunning})

	if _, err := w.orc.Quests.Cancel(w.ctx, q.ID, "desisti"); err != nil {
		t.Fatal(err)
	}
	// b is under way: it finishes first, nobody busy is interrupted
	if slices.Contains(w.stoppedNames(), "Revisor Codex 1") {
		t.Fatal("a busy character was ended")
	}
	w.finish("Revisor Codex 1", "terminei", nil, "")
	w.wantStates(q, map[string]quests.StepState{"b": quests.StepCancelled})
	if got := w.quest(q.ID); got.State != quests.Cancelled {
		t.Errorf("the quest is %s", got.State)
	}
	// the ephemeral one it made is ended; the persistent Dev stays
	if names := w.stoppedNames(); !slices.Contains(names, "Revisor Codex 1") || slices.Contains(names, "Dev Claude 1") {
		t.Errorf("the characters of the cancelled quest were not ended as they should: %v", names)
	}
	if text := w.scroll(w.quest(q.ID)); !strings.Contains(text, "Missão cancelada") || !strings.Contains(text, "desisti") {
		t.Errorf("the scroll does not say it was cancelled:\n%s", text)
	}
	if _, err := w.orc.Quests.Run(w.ctx, q.ID, ""); err == nil {
		t.Error("a cancelled quest ran")
	}
}

// A paused table starts no step and recruits no character for a quest.
func TestAPausedTableStartsNoStep(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if err := w.orc.Tables.Pause(w.ctx, w.slug, w.table.ID, tables.PauseRequest{Reason: "teste", By: "lucas"}); err != nil {
		t.Fatalf("pause the table: %v", err)
	}
	q := w.run(w.create(oneStep))
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepPending})
	if err := w.orc.Tables.Resume(w.ctx, w.slug, w.table.ID); err != nil {
		t.Fatalf("resume the table: %v", err)
	}
	if err := w.orc.Quests.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepRunning})
}

// A table that waits for the merge of another one begins no step, even one
// that is at work, and goes on when the other epic is done
// (docs/orquestracao/dependencia-entre-mesas.md).
// CA-DEPMESA-01
func TestATableThatWaitsForAnotherTableStartsNoStepUntilItIsMerged(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{epics: []int{43}})
	res, err := w.orc.EpicDeps.Depend(w.ctx, w.gm, 42, 43)
	if err != nil || !res.Waiting {
		t.Fatalf("Depend = %+v, %v; want waiting", res, err)
	}
	q := w.run(w.create(oneStep))
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepPending})
	// The epic #43 is closed as completed: the table goes on at once, with no
	// sweep to wake the quest.
	if err := w.orc.EpicDeps.EpicClosed(w.ctx, w.slug, 43, false); err != nil {
		t.Fatal(err)
	}
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepRunning})
}

// Part of CA-CIDADELA-11 (the promotion itself is the Guild's: the full suite
// runs it with instances): with characters on the Tower and others on the
// machine of the old Citadel, each with a step under way, when the new Citadel
// takes over only the ones of the old Citadel's machine are interrupted, and
// only their steps are delivered again, with the note, once they are back.
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

	// The new Citadel finds the terminals of the Tower alive and the ones of
	// the machine of the old Citadel gone.
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

// A character that takes the place of the one a step had (recruit --replace)
// gets the step delivered, with the note.
func TestAReplacementTakesTheStepOfTheCharacterItReplaces(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	q := w.run(w.create(oneStep))
	dev := w.char("Dev Claude 1")
	w.machines.Report(w.ctx, dev.ID, characters.Stuck)
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepStuck})
	res, err := w.orc.Characters.Replace(w.ctx, characters.ReplaceRequest{Workspace: w.slug, Table: w.table.ID, Name: "Dev Claude 1", Sheet: "Dev Claude",
		By: characters.Creator{Kind: characters.CreatedByCharacter, ID: w.master.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pending != "" {
		t.Fatalf("the replacement waits: %+v", res)
	}
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepRunning})
	if got := w.step(q, "a").Character; got == dev.ID || got != res.Character.ID {
		t.Errorf("the step has %s; want the new character %s", got, res.Character.ID)
	}
	if !w.told(res.Character, "troca de personagem") {
		t.Errorf("the new character got: %q", w.machines.Delivered(res.Character))
	}
	w.finish("Dev Claude 1", "feito pelo novo", nil, "")
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepCompleted, "b": quests.StepRunning})
	if runs, _ := w.orc.Quests.Store().Runs(w.ctx, q.ID+"/a"); len(runs) != 1 || runs[0].Summary != "feito pelo novo" {
		t.Errorf("runs of a: %+v", runs)
	}
}

// A step whose character the limits hold waits for a vacancy; when one opens,
// the character the vacancy makes gets the step.
func TestAStepWaitsForAVacancyAndTheCharacterTheVacancyMakesGetsIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{perTable: 1})
	q := w.run(w.create(`
name: Vaga
steps:
  - {id: a, sheet: Dev Claude}
  - {id: b, sheet: Revisor Codex}
`))
	// One character per table: b waits for a vacancy.
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepRunning, "b": quests.StepPreparing})
	if blocker := w.step(q, "b").Blocker; !strings.HasPrefix(blocker, "slot:") {
		t.Fatalf("b is held by %q; want a vacancy", blocker)
	}
	if v, err := w.orc.Quests.Status(w.ctx, w.quest(q.ID)); err != nil || v.Steps[1].Waiting != "vaga para o personagem" {
		t.Errorf("status of b: %+v, %v", v.Steps[1], err)
	}
	w.finish("Dev Claude 1", "a feito", nil, "")
	if _, err := w.orc.Characters.Dismiss(w.ctx, characters.DismissRequest{Name: "Dev Claude 1", Workspace: w.slug, Table: w.table.ID,
		By: characters.Creator{Kind: characters.CreatedByCharacter, ID: w.master.ID}}); err != nil {
		t.Fatal(err)
	}
	w.wantStates(q, map[string]quests.StepState{"a": quests.StepCompleted, "b": quests.StepRunning})
	if st := w.step(q, "b"); st.Blocker != "" || st.Character != w.char("Revisor Codex 1").ID {
		t.Errorf("b did not get the character the vacancy made: %+v", st)
	}
	if !w.told(w.char("Revisor Codex 1"), "Etapa \"b\"") {
		t.Error("the character of the vacancy was not given the step")
	}
}

// A quest whose table ends is cancelled by the sweep.
func TestAQuestOfATableThatEndedIsCancelledBySweep(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	q := w.run(w.create(oneStep))
	w.finish("Dev Claude 1", "a", nil, "")
	// the Reviewer is busy with b: the table ends when it is at a safe point
	_, _ = w.orc.Tables.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: w.table.ID, Cancel: true, Reason: "abandonada"})
	if tbl, err := w.orc.Tables.Get(w.ctx, w.slug, w.table.ID); err != nil || tbl.State == tables.StateWorking {
		t.Fatalf("the table is %v (%v); want it ending", tbl.State, err)
	}
	if err := w.orc.Quests.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	if got := w.quest(q.ID); got.State != quests.Cancelled || got.PausedReason != "a mesa terminou" {
		t.Errorf("the quest of a cancelled table is %s (%q)", got.State, got.PausedReason)
	}
}
