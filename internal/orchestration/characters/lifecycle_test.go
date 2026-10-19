package characters_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/providers"
)

// audited lists the actions of the audit trail of the Guild.
func (r *rig) audited(action string) int {
	r.t.Helper()
	var n int
	if err := r.gdb.Reader().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = ?`, action).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

// CA-PERS-03
func TestDismissOfABusyCharacterIsScheduledAndHappensAfterTheDoneAndIdleWithTheSummaryInTheScroll(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	tm := r.makeTM("3-login")
	c := r.make("Dev Claude", "Dev", "3-login")
	r.ready(c)
	if err := r.svc.TaskAssigned(r.ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	r.busy(c)

	res, err := r.svc.Dismiss(r.ctx, characters.DismissRequest{Name: "Dev", Workspace: workspace, Table: "3-login", By: byChar(tm)})
	if err != nil || res.Status != characters.Scheduled {
		t.Fatalf("dismiss = %+v, %v; want scheduled", res, err)
	}
	proc := r.mach[c.Machine].host.proc(c.ID)
	if got := r.get(c.ID); got.State != characters.Busy || got.StopRequestedAt.IsZero() || len(proc.stopCalls()) != 0 {
		t.Fatalf("after dismiss: %s, stop %v, %d stops; want busy and the end scheduled", got.State, got.StopRequestedAt, len(proc.stopCalls()))
	}

	// The done alone is not enough: it is still busy.
	if err := r.svc.Done(r.ctx, c.ID, "login feito com o teste verde"); err != nil {
		t.Fatal(err)
	}
	if len(proc.stopCalls()) != 0 || len(r.scrolls.all()) != 0 {
		t.Fatal("it was ended before the turn ended")
	}
	// Idle with the done delivered and the inbox empty: the safe point.
	r.hook(c, providers.EventTurnEnd)
	got := r.get(c.ID)
	if got.State != characters.Stopped {
		t.Fatalf("after the done and idle: %s, want stopped", got.State)
	}
	sums := r.scrolls.all()
	if len(sums) != 1 || sums[0].Character.Name != "Dev" || sums[0].Character.Sheet != "Dev Claude" || sums[0].Character.Class != "Dev" ||
		len(sums[0].Dones) != 1 || sums[0].Dones[0].Text != "login feito com o teste verde" {
		t.Errorf("summary = %+v", sums)
	}
	calls := proc.stopCalls()
	if len(calls) != 1 || calls[0].graceful != "/exit\r" || calls[0].wait != characters.DefaultGracefulWait {
		t.Errorf("the normal exit of the provider, then 30 s: %+v", calls)
	}
	if ends := r.mach[c.Machine].prep.endedOf(c.ID); len(ends) != 1 || !ends[0].Final {
		t.Errorf("the files of the character were not taken away: %+v", ends)
	}
	if ns := r.notes.of(characters.NoticeDismissed); len(ns) != 1 || ns[0].Creator != byChar(tm) {
		t.Errorf("whoever asked was not told: %+v", ns)
	}
	if got := r.path(c); got[len(got)-3] != characters.Idle || got[len(got)-2] != characters.Stopping || got[len(got)-1] != characters.Stopped {
		t.Errorf("states = %v", got)
	}
}

func TestDismissOfAnIdleCharacterWithItsDoneEndsAtOnceAndAnOpenTaskOrAFullInboxHoldsIt(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	idle := r.make("Dev Claude", "Livre", "3-login")
	r.ready(idle)
	res, err := r.svc.Dismiss(r.ctx, characters.DismissRequest{ID: idle.ID, By: engine})
	if err != nil || res.Status != characters.Dismissed || r.state(idle) != characters.Stopped {
		t.Fatalf("an idle character: %+v, %v, state %s; want dismissed at once", res, err, r.state(idle))
	}

	task := r.make("Dev Claude", "Tarefa", "3-login")
	r.ready(task)
	if err := r.svc.TaskAssigned(r.ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	res, err = r.svc.Dismiss(r.ctx, characters.DismissRequest{ID: task.ID, By: engine})
	if err != nil || res.Status != characters.Scheduled {
		t.Fatalf("idle with a task and no done: %+v, %v; want scheduled", res, err)
	}
	if err := r.svc.Done(r.ctx, task.ID, "pronto"); err != nil {
		t.Fatal(err)
	}
	if r.state(task) != characters.Stopped {
		t.Errorf("the done of an idle character with an end scheduled: %s, want stopped", r.state(task))
	}

	full := r.make("Dev Claude", "Cheia", "3-login")
	r.ready(full)
	r.inbox.set(full.ID, 1)
	if res, _ := r.svc.Dismiss(r.ctx, characters.DismissRequest{ID: full.ID, By: engine}); res.Status != characters.Scheduled {
		t.Errorf("a character with something in its inbox: %+v, want scheduled", res)
	}
	r.inbox.set(full.ID, 0)
	r.advance(time.Minute)
	if r.state(full) != characters.Stopped {
		t.Errorf("the inbox emptied: %s, want stopped", r.state(full))
	}

	// Waiting is not idle.
	waiting := r.make("Dev Claude", "Espera", "3-login")
	r.ready(waiting)
	r.busy(waiting)
	r.waits.set(waiting.ID, "Revisor")
	r.hook(waiting, providers.EventTurnEnd)
	if res, _ := r.svc.Dismiss(r.ctx, characters.DismissRequest{ID: waiting.ID, By: engine}); res.Status != characters.Scheduled || r.state(waiting) != characters.Waiting {
		t.Errorf("a waiting character: %+v, state %s; want scheduled", res, r.state(waiting))
	}
}

func TestWhoMayDismissAndWhere(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	gm := r.makeGM()
	tm := r.makeTM("3-login")
	other := r.makeTM("4-api")
	dev := r.make("Dev Claude", "Dev", "3-login")
	r.ready(dev)
	if _, err := r.svc.Dismiss(r.ctx, characters.DismissRequest{ID: dev.ID, By: byChar(other)}); !errors.Is(err, characters.ErrNotVisible) {
		t.Errorf("a table master of another table: %v, want not_visible", err)
	}
	if _, err := r.svc.Dismiss(r.ctx, characters.DismissRequest{ID: dev.ID, By: byChar(dev)}); !errors.Is(err, characters.ErrLevelDenied) {
		t.Errorf("a common character: %v, want level_denied", err)
	}
	observer := characters.Creator{Kind: characters.CreatedByUser, ID: "bia", Level: characters.LevelObserver}
	if _, err := r.svc.Dismiss(r.ctx, characters.DismissRequest{ID: dev.ID, By: observer}); !errors.Is(err, characters.ErrLevelDenied) {
		t.Errorf("an Observer: %v, want level_denied", err)
	}
	if _, err := r.svc.Dismiss(r.ctx, characters.DismissRequest{ID: tm.ID, By: byChar(tm)}); !errors.Is(err, characters.ErrLevelDenied) {
		t.Errorf("a table master dismissing a table master: %v, want level_denied", err)
	}
	if _, err := r.svc.Dismiss(r.ctx, characters.DismissRequest{Name: "Nao existe", Workspace: workspace, Table: "3-login", By: byChar(tm)}); !errors.Is(err, characters.ErrNoCharacter) {
		t.Errorf("a name that does not exist: %v", err)
	}
	if res, err := r.svc.Dismiss(r.ctx, characters.DismissRequest{ID: dev.ID, By: byChar(gm)}); err != nil || res.Status != characters.Dismissed {
		t.Errorf("the Grand Master: %+v, %v", res, err)
	}
}

// CA-PERS-04
func TestAPersistentCharacterEndsOnlyAfterTheMaximumIdleTimeInARow(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{machines: []machineSpec{withCodex(linux("linux"))}})
	c := r.make("Revisor Codex", "Revisor", "3-login") // persistent, 120 min
	r.ready(c)
	r.advance(119 * time.Minute)
	if got := r.state(c); got != characters.Idle {
		t.Fatalf("after 119 minutes idle: %s, want idle", got)
	}
	// It works, and waits for 30 minutes: that is not idle time.
	r.busy(c)
	r.waits.set(c.ID, "Dev")
	r.hook(c, providers.EventTurnEnd)
	r.waits.set(c.ID, "")
	if got := r.state(c); got != characters.Waiting {
		t.Fatalf("state = %s, want waiting", got)
	}
	r.advance(30 * time.Minute)
	if err := r.mach[c.Machine].rt.WaitResolved(r.ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	r.advance(119 * time.Minute)
	if got := r.state(c); got != characters.Idle {
		t.Fatalf("119 minutes idle again: %s, want idle", got)
	}
	r.advance(time.Minute)
	if got := r.state(c); got != characters.Stopped {
		t.Fatalf("120 minutes idle in a row: %s, want stopped", got)
	}
	if got := r.get(c.ID).StopReason; got != "" {
		t.Errorf("the request of the end was not cleared: %q", got)
	}
	if len(r.scrolls.all()) != 1 {
		t.Error("the summary of its end was not written")
	}
}

func TestAnEphemeralFromADirectRecruitEndsFiveMinutesAfterItsDoneAndOneFromAQuestWithTheQuest(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	tm := r.makeTM("3-login")
	direct := r.make("Dev Claude", "Direto", "3-login")
	r.ready(direct)
	r.advance(time.Hour)
	if r.state(direct) != characters.Idle {
		t.Fatal("an ephemeral that never got a task was ended")
	}
	if err := r.svc.TaskAssigned(r.ctx, direct.ID); err != nil {
		t.Fatal(err)
	}
	r.busy(direct)
	if err := r.svc.Done(r.ctx, direct.ID, "feito"); err != nil {
		t.Fatal(err)
	}
	r.idle(direct)
	r.advance(4*time.Minute + 59*time.Second)
	if r.state(direct) != characters.Idle {
		t.Fatal("it was ended before the 5 minutes of grace")
	}
	r.advance(time.Second)
	if r.state(direct) != characters.Stopped {
		t.Errorf("after the grace: %s, want stopped", r.state(direct))
	}

	// With a new message in the grace, the count starts again after the next done.
	again := r.make("Dev Claude", "De novo", "3-login")
	r.ready(again)
	if err := r.svc.TaskAssigned(r.ctx, again.ID); err != nil {
		t.Fatal(err)
	}
	r.busy(again)
	_ = r.svc.Done(r.ctx, again.ID, "um")
	r.idle(again)
	r.advance(4 * time.Minute)
	r.inbox.set(again.ID, 1)
	r.advance(2 * time.Minute)
	if r.state(again) != characters.Idle {
		t.Error("it was ended with something in its inbox")
	}

	// An ephemeral of a quest is not used by another and ends with its quest,
	// at its safe point.
	step, err := r.create(characters.CreateRequest{Name: "Etapa", Sheet: "Dev Claude", Table: "3-login", Quest: "q7", Creator: engine})
	if err != nil {
		t.Fatal(err)
	}
	s := step.Character
	r.ready(s)
	if err := r.svc.TaskAssigned(r.ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	r.busy(s)
	_ = r.svc.Done(r.ctx, s.ID, "etapa feita")
	r.idle(s)
	r.advance(time.Hour)
	if r.state(s) != characters.Idle {
		t.Fatal("an ephemeral of a quest ended before its quest")
	}
	if err := r.svc.EndQuest(r.ctx, "q7"); err != nil {
		t.Fatal(err)
	}
	if r.state(s) != characters.Stopped {
		t.Errorf("when the quest ended: %s, want stopped", r.state(s))
	}
	_ = tm
}

// CA-PERS-05
func TestAPinnedCharacterIsNeverEndedAndTheRefusalIsRecorded(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{machines: []machineSpec{withCodex(linux("linux"))}})
	tm := r.makeTM("3-login")
	c := r.make("Revisor Codex", "Fixo", "3-login")
	r.ready(c)
	if err := r.svc.Pin(r.ctx, c.ID, true, byChar(tm)); !errors.Is(err, characters.ErrLevelDenied) {
		t.Fatalf("a character pinning: %v, want level_denied (only a user pins)", err)
	}
	if err := r.svc.Pin(r.ctx, c.ID, true, owner); err != nil {
		t.Fatal(err)
	}
	proc := r.mach[c.Machine].host.proc(c.ID)

	// The lifecycle: 120 minutes idle.
	r.advance(3 * time.Hour)
	if r.state(c) != characters.Idle || len(proc.stopCalls()) != 0 {
		t.Fatalf("the lifecycle ended a pinned character: %s", r.state(c))
	}
	if r.audited("character.end_refused_pinned") != 1 {
		t.Errorf("the refusal of the lifecycle was not recorded once: %d", r.audited("character.end_refused_pinned"))
	}
	// Dismiss.
	if _, err := r.svc.Dismiss(r.ctx, characters.DismissRequest{ID: c.ID, By: byChar(tm)}); !errors.Is(err, characters.ErrPinned) {
		t.Fatalf("dismiss: %v, want pinned", err)
	}
	if r.state(c) != characters.Idle || len(proc.stopCalls()) != 0 || r.audited("character.end_refused_pinned") != 2 {
		t.Errorf("state %s, %d stops, %d refusals", r.state(c), len(proc.stopCalls()), r.audited("character.end_refused_pinned"))
	}
	// The end of the quest, too.
	if err := r.svc.EndQuest(r.ctx, "qualquer"); err != nil {
		t.Fatal(err)
	}
	// Only a user unpins; then the lifecycle ends it.
	if err := r.svc.Pin(r.ctx, c.ID, false, owner); err != nil {
		t.Fatal(err)
	}
	r.advance(time.Minute)
	if r.state(c) != characters.Stopped {
		t.Errorf("after unpinning: %s, want stopped", r.state(c))
	}
}

func TestAStuckOrInterruptedCharacterIsEndedByATableMasterWithAnAuditLine(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	tm := r.makeTM("3-login")
	stuck := reach(t, r, characters.Stuck, "Travado")
	gone := reach(t, r, characters.Interrupted, "Perdido")
	for _, c := range []characters.Character{stuck, gone} {
		res, err := r.svc.Dismiss(r.ctx, characters.DismissRequest{ID: c.ID, By: byChar(tm)})
		if err != nil || res.Status != characters.Dismissed || r.state(c) != characters.Stopped {
			t.Errorf("%s: %+v, %v, state %s; want dismissed", c.Name, res, err, r.state(c))
		}
	}
	if r.audited("character.force_stopped") != 2 {
		t.Errorf("%d audit lines, want 2", r.audited("character.force_stopped"))
	}
	if calls := r.mach[stuck.Machine].host.proc(stuck.ID).stopCalls(); len(calls) != 1 || calls[0].graceful != "" {
		t.Errorf("a stuck agent is not asked to exit by typing: %+v", calls)
	}
}

// The exception to the safe point is the table master's and the
// Administrators': the Grand Master does not force a stop.
func TestTheGrandMasterDoesNotForceTheStopOfAStuckCharacter(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	gm := r.makeGM()
	r.makeTM("3-login")
	stuck := reach(t, r, characters.Stuck, "Travado")
	_, err := r.svc.Dismiss(r.ctx, characters.DismissRequest{ID: stuck.ID, By: byChar(gm)})
	if !errors.Is(err, characters.ErrLevelDenied) || r.state(stuck) != characters.Stuck {
		t.Fatalf("Dismiss by the Grand Master = %v, state %s; want level_denied and still stuck", err, r.state(stuck))
	}
	admin := characters.Creator{Kind: characters.CreatedByUser, ID: "ana", Level: characters.LevelAdministrator}
	if _, err := r.svc.Dismiss(r.ctx, characters.DismissRequest{ID: stuck.ID, By: admin}); err != nil || r.state(stuck) != characters.Stopped {
		t.Fatalf("Dismiss by an Administrator = %v, state %s", err, r.state(stuck))
	}
}

func TestReplaceKeepsTheNameAndTheCreatorAndStartsWhenTheOldOneEnds(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{machines: []machineSpec{linux("a"), linux("b")}})
	tm := r.makeTM("3-login")
	old, err := r.create(characters.CreateRequest{Name: "Dev-1", Sheet: "Dev Claude", Table: "3-login", Creator: byChar(tm), Machine: "b"})
	if err != nil {
		t.Fatal(err)
	}
	c := old.Character
	r.ready(c)
	_ = r.svc.TaskAssigned(r.ctx, c.ID)
	r.busy(c)
	res, err := r.svc.Replace(r.ctx, characters.ReplaceRequest{Workspace: workspace, Table: "3-login", Name: "Dev-1", Sheet: "Dev Persistente", By: byChar(tm)})
	if err != nil || res.Pending == "" {
		t.Fatalf("replace of a busy character: %v %+v, want pending", err, res)
	}
	if r.state(c) != characters.Busy {
		t.Fatal("the old one was ended while it worked")
	}
	_ = r.svc.Done(r.ctx, c.ID, "feito")
	r.hook(c, providers.EventTurnEnd)
	if r.state(c) != characters.Stopped {
		t.Fatalf("the old one: %s, want stopped", r.state(c))
	}
	if ns := r.notes.of(characters.NoticeCreated); len(ns) != 1 || ns[0].Creator != byChar(tm) {
		t.Errorf("notices of the new character = %+v, want one to who asked", ns)
	}
	n, err := r.reg.ByName(r.ctx, workspace, "3-login", "Dev-1")
	if err != nil || n.ID == c.ID || n.Sheet != "Dev Persistente" || n.CreatedBy != byChar(tm) || n.Machine != "b" {
		t.Fatalf("the new one = %+v, %v; want the same name, creator and machine with the new sheet", n, err)
	}
}

func TestTheSuperiorComesFromWhoCreatedTheCharacter(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	gm := r.makeGM()
	tm := r.makeTM("3-login")
	recruited := mustCreate(t, r, characters.CreateRequest{Name: "Recrutado", Sheet: "Dev Claude", Table: "3-login", Creator: byChar(tm)})
	step := mustCreate(t, r, characters.CreateRequest{Name: "Etapa", Sheet: "Dev Claude", Table: "3-login", Quest: "q1"})
	canvas := mustCreate(t, r, characters.CreateRequest{Name: "Canvas", Sheet: "Dev Claude", Table: "3-login", Creator: owner})
	other := mustCreate(t, r, characters.CreateRequest{Name: "Outro", Sheet: "Dev Claude", Table: "4-api", Creator: owner}) // no table master there
	want := func(c characters.Character, id string, people bool) {
		t.Helper()
		sup, err := r.svc.Superior(r.ctx, c.ID)
		if err != nil || sup.People != people || sup.ID != id {
			t.Errorf("superior of %s = %+v, %v; want %q (people %v)", c.Name, sup, err, id, people)
		}
	}
	want(recruited, tm.ID, false)
	want(step, tm.ID, false)
	want(canvas, tm.ID, false)
	want(other, gm.ID, false)
	want(tm, gm.ID, false)
	want(gm, "", true)

	// A recruit of a character that is not the table master: the creator.
	sub := mustCreate(t, r, characters.CreateRequest{Name: "Sub", Sheet: "Dev Claude", Table: "3-login", Creator: byChar(tm)})
	r.svc.Pin(r.ctx, sub.ID, true, owner) //nolint:errcheck // not under test
	// When the creator ends, the superior becomes the creator's, at once.
	r.ready(recruited)
	if _, err := r.svc.Dismiss(r.ctx, characters.DismissRequest{ID: recruited.ID, By: engine}); err != nil {
		t.Fatal(err)
	}
	want(recruited, tm.ID, false) // itself ended: its own superior stays the table master
	// A table master that ended: its characters go to the superior of the master.
	r.ready(tm)
	if _, err := r.svc.Dismiss(r.ctx, characters.DismissRequest{ID: tm.ID, By: engine}); err != nil {
		t.Fatal(err)
	}
	want(sub, gm.ID, false)
	want(canvas, gm.ID, false) // without a table master: the Grand Master
	if err := r.mach[gm.Machine].rt.WaitResolved(r.ctx, gm.ID); err != nil {
		t.Fatal(err)
	}
	r.ready(gm)
	if _, err := r.svc.Dismiss(r.ctx, characters.DismissRequest{ID: gm.ID, By: engine}); err != nil {
		t.Fatal(err)
	}
	want(canvas, "", true) // no Grand Master either: the people
	if !strings.Contains(gm.Name, "Grão") {
		t.Error("the Grand Master has its reserved name")
	}
}

func mustCreate(t *testing.T, r *rig, req characters.CreateRequest) characters.Character {
	t.Helper()
	res, err := r.create(req)
	if err != nil || res.Pending != "" {
		t.Fatalf("create %s: %v %+v", req.Name, err, res)
	}
	return res.Character
}

func TestRunSweepsTheLifecycleOnEachTick(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{machines: []machineSpec{withCodex(linux("linux"))}})
	c := r.make("Revisor Codex", "Revisor", "3-login")
	r.ready(c)
	ctx, cancel := context.WithCancel(r.ctx)
	done := make(chan error, 1)
	armed := r.clk.Armed()
	go func() { done <- r.svc.Run(ctx, time.Minute) }()
	r.clk.BlockUntil(armed + 1)
	r.clk.Advance(2 * time.Hour)
	for {
		next := r.tap.next()
		if r.state(c) == characters.Stopped {
			break
		}
		<-next
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("Run ended with %v", err)
	}
	if r.state(c) != characters.Stopped {
		t.Errorf("state = %s, want stopped by the periodic work", r.state(c))
	}
}
