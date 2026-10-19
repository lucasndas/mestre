package waits_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/waits"
)

type askRes struct {
	out waits.AskOutcome
	err error
}

// askAsync makes an ask that blocks, in a goroutine, and returns once the ask
// holds its time limit (so the test can move the clock).
func (w *world) askAsync(req waits.AskRequest) <-chan askRes {
	w.t.Helper()
	armed := w.clk.Armed()
	ch := make(chan askRes, 1)
	go func() {
		o, err := w.svc.Ask(context.Background(), req)
		ch <- askRes{o, err}
	}()
	w.clk.BlockUntil(armed + 1)
	return ch
}

func notYet(t *testing.T, ch <-chan askRes, what string) {
	t.Helper()
	select {
	case r := <-ch:
		t.Fatalf("%s: the ask already returned %+v", what, r)
	default:
	}
}

// CA-ESP-04 (the service: pending at 90 s, the late answer whole, no resend;
// the text "sem resposta no prazo" is asserted at the command, in
// internal/app/cli_comm_test.go and in TestTheAskCommandAnswersPendingBatchAndNotConnected)
func TestAnAskThatRunsOutOfTimeReturnsPendingAndTheLateAnswerComesWholeAsAnAnswerWithoutAResend(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.dev("42-login", "Dev")
	rev := w.dev("42-login", "Revisor", dev)
	w.set(dev, characters.Busy) // inside the shell tool of its ask
	ch := w.askAsync(waits.AskRequest{From: dev, To: rev, Prompt: "revise o PR #12"})
	if got := w.deliv.typedTo(rev); len(got) != 1 {
		t.Fatalf("typed in the target %q, want the question once", got)
	}
	w.clk.Advance(89 * time.Second)
	notYet(t, ch, "at 89 s")
	w.clk.Advance(time.Second)
	r := <-ch
	if r.err != nil || r.out.Status != waits.StatusPending || r.out.Response != "" || r.out.ID == "" {
		t.Fatalf("at 90 s the ask returned %+v, want pending", r)
	}
	// the target answers later, with ask back, and the answer is not cut
	answer := "Revisei o PR #12: 3 comentários.\n\n1. falta teste do erro\n2. renomear login()\n3. ok"
	back, err := w.svc.Ask(w.ctx, waits.AskRequest{From: rev, To: dev, Prompt: answer})
	if err != nil || back.Status != waits.StatusDelivered {
		t.Fatalf("ask back = %+v, %v: it returns at once, delivered", back, err)
	}
	items := w.inboxOf(dev)
	if len(items) != 1 || items[0].Kind != inbox.KindAnswer || !strings.Contains(items[0].Text, answer) || items[0].Refs.Ask != r.out.ID || items[0].Sender != "Revisor" {
		t.Fatalf("the inbox of the asker is %+v, want the whole answer as an `answer`", items)
	}
	if got := w.deliv.typedTo(rev); len(got) != 1 {
		t.Fatalf("typed in the target %q: the request was sent again", got)
	}
	if a, _ := w.svc.Store().GetAsk(w.ctx, r.out.ID); a.State != waits.AskAnswered || a.Response != answer {
		t.Errorf("ask = %+v", a)
	}
	// a second ask back is a plain message, not an answer to anything
	again, _ := w.svc.Ask(w.ctx, waits.AskRequest{From: rev, To: dev, Prompt: "mais uma coisa", NoWait: true})
	if again.Status != waits.StatusSent {
		t.Errorf("a message with no open request = %+v", again)
	}
}

// The text of the done of the turn that handled the request is the answer; with
// no done, the last message of the agent in that turn.
func TestTheAnswerIsTheDoneOfTheTurnOrTheLastMessage(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.dev("42-login", "Dev")
	rev := w.dev("42-login", "Revisor", dev)
	ch := w.askAsync(waits.AskRequest{From: dev, To: rev, Prompt: "revise"})
	w.done(rev, "aprovado, com 1 ressalva")
	w.turnEnds(rev, "mensagem final que não é a resposta")
	r := <-ch
	if r.err != nil || r.out.Status != waits.StatusAnswered || r.out.Response != "aprovado, com 1 ressalva" {
		t.Fatalf("with a done: %+v", r)
	}
	ch = w.askAsync(waits.AskRequest{From: dev, To: rev, Prompt: "e agora?"})
	w.turnEnds(rev, "a última mensagem do agente")
	r = <-ch
	if r.err != nil || r.out.Status != waits.StatusAnswered || r.out.Response != "a última mensagem do agente" {
		t.Fatalf("without a done: %+v", r)
	}
	// the done of a turn is not the answer of the next one
	ch = w.askAsync(waits.AskRequest{From: dev, To: rev, Prompt: "mais uma"})
	w.turnEnds(rev, "só texto")
	if r = <-ch; r.out.Response != "só texto" {
		t.Fatalf("a done of an earlier turn leaked into the answer: %+v", r)
	}
}

// A turn that was not the one of the request does not answer it: the target was
// busy with something else when the question came.
func TestTheTurnThatWasWorkingWhenTheQuestionCameDoesNotAnswerIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.dev("42-login", "Dev")
	rev := w.dev("42-login", "Revisor", dev)
	w.set(rev, characters.Busy)
	ch := w.askAsync(waits.AskRequest{From: dev, To: rev, Prompt: "revise"})
	if got := w.deliv.typedTo(rev); len(got) != 0 {
		t.Fatalf("typed %q in a busy target", got)
	}
	w.turnEnds(rev, "terminei o que estava fazendo") // the question is typed now
	notYet(t, ch, "after the earlier turn")
	if got := w.deliv.typedTo(rev); len(got) != 1 {
		t.Fatalf("typed %q after the earlier turn, want the question", got)
	}
	w.turnEnds(rev, "revisado")
	if r := <-ch; r.out.Response != "revisado" || r.out.Status != waits.StatusAnswered {
		t.Fatalf("answer = %+v", r)
	}
}

// CA-LOCAL-04
func TestWhenTheCallOfTheCLIIsGoneTheAnswerGoesToTheInboxOfTheAsker(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.dev("42-login", "Dev")
	rev := w.dev("42-login", "Revisor", dev)
	w.set(dev, characters.Busy)
	gone := make(chan struct{})
	ch := w.askAsync(waits.AskRequest{From: dev, To: rev, Prompt: "revise", Gone: gone})
	close(gone) // the CLI died
	r := <-ch
	if r.err != nil || r.out.Status != waits.StatusPending {
		t.Fatalf("the ask of a dead CLI = %+v", r)
	}
	w.done(rev, "tudo certo")
	w.turnEnds(rev, "x")
	items := w.inboxOf(dev)
	if len(items) != 1 || items[0].Kind != inbox.KindAnswer || !strings.Contains(items[0].Text, "tudo certo") || items[0].Refs.Ask != r.out.ID {
		t.Fatalf("inbox of the asker = %+v, want the answer", items)
	}
}

func TestAskWithoutWaitingDeliversAtOnceAndTheAnswerComesToTheInbox(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.dev("42-login", "Dev")
	rev := w.dev("42-login", "Revisor", dev)
	w.set(dev, characters.Busy)
	out, err := w.svc.Ask(w.ctx, waits.AskRequest{From: dev, To: rev, Prompt: "revise", NoWait: true})
	if err != nil || out.Status != waits.StatusSent || out.ID == "" {
		t.Fatalf("Ask = %+v, %v", out, err)
	}
	w.turnEnds(rev, "revisado")
	items := w.inboxOf(dev)
	if len(items) != 1 || items[0].Kind != inbox.KindAnswer || !strings.Contains(items[0].Text, "revisado") {
		t.Fatalf("inbox of the asker = %+v", items)
	}
	if _, err := w.svc.Ask(w.ctx, waits.AskRequest{From: dev, To: dev, Prompt: "x"}); !errors.Is(err, waits.ErrSelf) {
		t.Errorf("an ask to oneself = %v", err)
	}
}

func TestAnAskToACharacterThatEndsIsCancelledAndTheAskerIsTold(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.dev("42-login", "Dev")
	rev := w.dev("42-login", "Revisor", dev)
	ch := w.askAsync(waits.AskRequest{From: dev, To: rev, Prompt: "revise"})
	w.set(rev, characters.Stopping)
	w.set(rev, characters.Stopped)
	r := <-ch
	if r.err != nil || r.out.Status != waits.StatusEnded || !strings.Contains(r.out.Response, "foi encerrado antes de responder") {
		t.Fatalf("ask = %+v", r)
	}
	// an ask with no caller waiting tells the asker through the inbox
	out, _ := w.svc.Ask(w.ctx, waits.AskRequest{From: dev, To: w.dev("42-login", "QA", dev), Prompt: "teste", NoWait: true})
	qa, _ := w.reg.ByName(w.ctx, workspace, "42-login", "QA")
	w.set(qa, characters.Stopped)
	found := false
	for _, it := range w.inboxOf(dev) {
		found = found || (it.Refs.Ask == out.ID && strings.Contains(it.Text, "QA foi encerrado"))
	}
	if !found {
		t.Errorf("the asker was not told: %+v", w.inboxOf(dev))
	}
}

// CA-ESP-05
func TestAWaitForAllTheStepsStaysActiveUntilTheLastOneEndsAndThenWakesWhoWaits(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	master := w.dev("42-login", "Mestre")
	w.dev("42-login", "QA", master)
	w.dev("42-login", "Revisor", master)
	wt, err := w.svc.WaitSteps(w.ctx, waits.StepsRequest{Waiter: master, Quest: "q1", Steps: []waits.Target{{ID: "s-qa", Name: "QA"}, {ID: "s-rev", Name: "Revisor"}}})
	if err != nil {
		t.Fatal(err)
	}
	if wt.Reason != "QA e Revisor (todas)" || wt.Kind != waits.KindSteps || wt.State != waits.Active {
		t.Fatalf("wait = %+v", wt)
	}
	w.set(master, characters.Busy)
	w.set(master, characters.Waiting) // it ended its turn: the wait is the reason
	w.waker.woke = nil
	w.svc.StepDone(w.ctx, "s-qa", "testes passam")
	got, _ := w.svc.Store().GetWait(w.ctx, wt.ID)
	if got.State != waits.Active {
		t.Fatalf("with only the QA done the wait is %s, want active", got.State)
	}
	if n := len(w.all(master)); n != 0 {
		t.Fatalf("%d items for the waiter before the last step", n)
	}
	w.svc.StepDone(w.ctx, "s-rev", "aprovado")
	got, _ = w.svc.Store().GetWait(w.ctx, wt.ID)
	if got.State != waits.Satisfied {
		t.Fatalf("with both done the wait is %s", got.State)
	}
	items := w.all(master)
	if len(items) != 1 || items[0].Kind != inbox.KindWake || items[0].Refs.Wait != wt.ID ||
		!strings.Contains(items[0].Text, "QA concluiu: testes passam") || !strings.Contains(items[0].Text, "Revisor concluiu: aprovado") {
		t.Fatalf("the wake is %+v", items)
	}
	if got := w.deliv.typedTo(master); len(got) != 1 {
		t.Fatalf("typed %q in the waiting master: the wake is delivered at once", got)
	}
	// the first of the steps wakes the waiter when the mode asks for it
	wt, _ = w.svc.WaitSteps(w.ctx, waits.StepsRequest{Waiter: master, Steps: []waits.Target{{ID: "x1", Name: "A"}, {ID: "x2", Name: "B"}}, First: true})
	if wt.Reason != "A e B (primeira)" {
		t.Errorf("reason = %q", wt.Reason)
	}
	w.svc.StepDone(w.ctx, "x2", "feito")
	if got, _ := w.svc.Store().GetWait(w.ctx, wt.ID); got.State != waits.Satisfied {
		t.Errorf("the first step done left the wait %s", got.State)
	}
}

func TestAWaitForCharactersIsSatisfiedByTheirDoneOrByAnAskBack(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	master := w.dev("42-login", "Mestre")
	qa := w.dev("42-login", "QA", master)
	rev := w.dev("42-login", "Revisor", master)
	wt, err := w.svc.WaitFor(w.ctx, waits.WaitRequest{Waiter: master, Names: []string{"QA", "revisor"}})
	if err != nil || wt.Reason != "QA e Revisor (todas)" || wt.TimeoutAt.Sub(wt.CreatedAt) != time.Hour {
		t.Fatalf("WaitFor = %+v, %v: the default limit is 60 min", wt, err)
	}
	if reason, ok, _ := w.svc.Active(w.ctx, master.ID); !ok || reason != "QA e Revisor (todas)" {
		t.Fatalf("Active = %q, %v", reason, ok)
	}
	w.done(qa, "testes ok")
	if got, _ := w.svc.Store().GetWait(w.ctx, wt.ID); got.State != waits.Active {
		t.Fatalf("after the QA the wait is %s", got.State)
	}
	// the Revisor answers by ask back instead of a done
	out, err := w.svc.Ask(w.ctx, waits.AskRequest{From: rev, To: master, Prompt: "revisão: aprovado"})
	if err != nil || out.Status != waits.StatusDelivered {
		t.Fatalf("ask back = %+v, %v", out, err)
	}
	got, _ := w.svc.Store().GetWait(w.ctx, wt.ID)
	if got.State != waits.Satisfied {
		t.Fatalf("after the ask back the wait is %s", got.State)
	}
	items := w.all(master)
	if len(items) != 1 || items[0].Kind != inbox.KindWake || !strings.Contains(items[0].Text, "revisão: aprovado") {
		t.Fatalf("items = %+v: one wake, and the ask back is not a second message", items)
	}
	if _, ok, _ := w.svc.Active(w.ctx, master.ID); ok {
		t.Error("a satisfied wait is still active")
	}
	// the first of the names
	wt, _ = w.svc.WaitFor(w.ctx, waits.WaitRequest{Waiter: master, Names: []string{"QA", "Revisor"}, First: true})
	w.done(rev, "x")
	if got, _ := w.svc.Store().GetWait(w.ctx, wt.ID); got.State != waits.Satisfied {
		t.Errorf("--first: %s", got.State)
	}
	if _, err := w.svc.WaitFor(w.ctx, waits.WaitRequest{Waiter: master, Names: []string{"mestre"}}); !errors.Is(err, waits.ErrSelf) {
		t.Errorf("a wait for oneself = %v", err)
	}
}

// CA-ESP-06
func TestWhenTwoCharactersWaitForEachOtherTheDeadlockIsWarnedOnceWithTheRing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	master := w.add(characters.RoleTableMaster, "42-login", characters.NameTableMaster)
	a := w.dev("42-login", "A", master)
	b := w.dev("42-login", "B", a, master)
	if _, err := w.svc.WaitFor(w.ctx, waits.WaitRequest{Waiter: a, Names: []string{"B"}}); err != nil {
		t.Fatal(err)
	}
	if n := len(w.notes.of(characters.NoticeDeadlock)); n != 0 {
		t.Fatalf("%d warnings before there was a ring", n)
	}
	if _, err := w.svc.WaitFor(w.ctx, waits.WaitRequest{Waiter: b, Names: []string{"A"}}); err != nil {
		t.Fatal(err)
	}
	notices := w.notes.of(characters.NoticeDeadlock)
	if len(notices) != 2 {
		t.Fatalf("notices = %+v, want one for the table master and one for the users", notices)
	}
	for _, n := range notices {
		if !strings.Contains(n.Text, "A → B → A") || strings.Contains(n.Text, "B → A → B") {
			t.Errorf("the notice does not write the ring as A → B → A: %q", n.Text)
		}
	}
	if notices[0].To.Kind != characters.ToTableMaster || notices[0].Table != "42-login" || notices[1].To.Kind != characters.ToOwner {
		t.Errorf("recipients = %+v / %+v", notices[0].To, notices[1].To)
	}
	// nothing is cancelled by itself, and the same ring is not warned again
	for _, id := range []string{a.ID, b.ID} {
		if _, ok, _ := w.svc.Active(w.ctx, id); !ok {
			t.Errorf("the wait of %s was cancelled", id)
		}
	}
	w.svc.StepDone(w.ctx, "nothing", "") // an event that changes nothing
	if _, err := w.svc.WaitFor(w.ctx, waits.WaitRequest{Waiter: a, Names: []string{"B"}}); err != nil {
		t.Fatal(err)
	}
	if n := len(w.notes.of(characters.NoticeDeadlock)); n != 2 {
		t.Fatalf("%d notices after a second wait on the same ring, want it warned once", n)
	}
	if cs, _ := w.svc.Cycles(w.ctx); len(cs) != 1 || cs[0].Text != "A → B → A" {
		t.Errorf("the cycles to highlight = %+v", cs)
	}
	// when the ring is gone and comes back, it is a new warning
	w.done(b, "ok") // B's done satisfies the wait of A
	w.set(a, characters.Idle)
	if cs, _ := w.svc.Cycles(w.ctx); len(cs) != 0 {
		t.Errorf("the ring went away but the cycles are %+v", cs)
	}
}

func TestADeadlockThroughTheStepsOfAQuestAndAcrossTablesGoesToTheGrandMaster(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.gm()
	a := w.dev("42-login", "A")
	b := w.dev("43-api", "B")
	w.quests.owner["step-b"] = b.ID
	if _, err := w.svc.WaitSteps(w.ctx, waits.StepsRequest{Waiter: a, Steps: []waits.Target{{ID: "step-b", Name: "etapa de B"}}}); err != nil {
		t.Fatal(err)
	}
	// B asks A, blocked: A waits for B's step, B waits for A
	w.askAsync(waits.AskRequest{From: b, To: a, Prompt: "ajude"})
	notices := w.notes.of(characters.NoticeDeadlock)
	if len(notices) != 2 || notices[0].To.Kind != characters.ToGrandMaster || notices[1].To.Kind != characters.ToOwner {
		t.Fatalf("notices = %+v, want the Grand Master and the users", notices)
	}
	if !strings.Contains(notices[0].Text, "A → B → A") {
		t.Errorf("text = %q", notices[0].Text)
	}
}

func TestAWaitThatRunsOutOfTimeExpiresAndTellsWhoWaitsWhatToDoWithoutSendingAnythingAgain(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	master := w.dev("42-login", "Mestre")
	rev := w.dev("42-login", "Revisor", master)
	wt, err := w.svc.WaitFor(w.ctx, waits.WaitRequest{Waiter: master, Names: []string{"Revisor"}, Timeout: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	w.clk.Advance(9*time.Minute + 59*time.Second)
	if got, _ := w.svc.Store().GetWait(w.ctx, wt.ID); got.State != waits.Active {
		t.Fatalf("state = %s before the limit", got.State)
	}
	w.clk.Advance(time.Second)
	got, _ := w.svc.Store().GetWait(w.ctx, wt.ID)
	if got.State != waits.Expired {
		t.Fatalf("state = %s at the limit", got.State)
	}
	items := w.all(master)
	if len(items) != 1 || items[0].Kind != inbox.KindWake {
		t.Fatalf("items = %+v", items)
	}
	for _, want := range []string{"estourou o prazo", "mestre check", "reenviar com mais contexto", "trocar o personagem", "avisar os usuários", "Nada foi reenviado"} {
		if !strings.Contains(items[0].Text, want) {
			t.Errorf("the notice lacks %q: %q", want, items[0].Text)
		}
	}
	if got := w.deliv.typedTo(rev); len(got) != 0 {
		t.Errorf("typed %q in the target: the engine never sends it again", got)
	}
	// a late done does not revive it
	w.done(rev, "tarde")
	if got, _ := w.svc.Store().GetWait(w.ctx, wt.ID); got.State != waits.Expired || len(w.all(master)) != 1 {
		t.Errorf("a late done changed an expired wait")
	}
}

func TestAWaitForATimeIsSatisfiedWhenItComesAndAPastTimeIsAnError(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.dev("42-login", "Dev")
	at, err := w.svc.ParseUntil("14:00")
	if err != nil || !at.Equal(time.Date(2026, 10, 15, 14, 0, 0, 0, time.UTC)) {
		t.Fatalf("ParseUntil = %v, %v", at, err)
	}
	if _, err := w.svc.ParseUntil("11:59"); !errors.Is(err, waits.ErrPast) {
		t.Fatalf("a time already past = %v", err)
	}
	if _, err := w.svc.WaitUntil(w.ctx, dev, w.clk.Now().Add(-time.Minute)); !errors.Is(err, waits.ErrPast) {
		t.Fatalf("WaitUntil in the past = %v", err)
	}
	if at, err := w.svc.ParseUntil("2026-10-16T09:30:00Z"); err != nil || at.Hour() != 9 {
		t.Fatalf("ISO 8601 = %v, %v", at, err)
	}
	if _, err := w.svc.ParseUntil("amanhã"); err == nil {
		t.Fatal("nonsense was accepted")
	}
	wt, err := w.svc.WaitUntil(w.ctx, dev, at)
	if err != nil || wt.Reason != "14:00" || !wt.TimeoutAt.IsZero() {
		t.Fatalf("WaitUntil = %+v, %v: no time limit, reason 14:00", wt, err)
	}
	w.clk.Advance(2*time.Hour - time.Second)
	if got, _ := w.svc.Store().GetWait(w.ctx, wt.ID); got.State != waits.Active {
		t.Fatalf("state = %s before 14:00", got.State)
	}
	w.clk.Advance(time.Second)
	if got, _ := w.svc.Store().GetWait(w.ctx, wt.ID); got.State != waits.Satisfied {
		t.Fatalf("state = %s at 14:00", got.State)
	}
	if items := w.all(dev); len(items) != 1 || items[0].Kind != inbox.KindWake || !strings.Contains(items[0].Text, "14:00") {
		t.Fatalf("items = %+v", items)
	}
}

func TestAnApprovalWaitIsSatisfiedByTheDecisionAndACancelledWaitReleasesTheCharacter(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.dev("42-login", "Dev")
	wt, err := w.svc.WaitApproval(w.ctx, dev, 3, 30*time.Minute)
	if err != nil || wt.Reason != "approval #3" || wt.Kind != waits.KindApproval {
		t.Fatalf("WaitApproval = %+v, %v", wt, err)
	}
	w.svc.ApprovalDecided(w.ctx, 4, "outro pedido") // not this one
	if got, _ := w.svc.Store().GetWait(w.ctx, wt.ID); got.State != waits.Active {
		t.Fatalf("the decision of another request changed the wait: %s", got.State)
	}
	w.svc.ApprovalDecided(w.ctx, 3, "aprovado pelo Mestre")
	if items := w.all(dev); len(items) != 1 || !strings.Contains(items[0].Text, "aprovado pelo Mestre") {
		t.Fatalf("items = %+v", items)
	}
	// a cancelled wait (the quest was cancelled) wakes nothing, but the waiting
	// character is released
	w.set(dev, characters.Busy)
	w.set(dev, characters.Waiting)
	q, _ := w.svc.WaitSteps(w.ctx, waits.StepsRequest{Waiter: dev, Quest: "q9", Steps: []waits.Target{{ID: "s1", Name: "Etapa"}}})
	if err := w.svc.CancelQuest(w.ctx, "q9"); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.svc.Store().GetWait(w.ctx, q.ID); got.State != waits.Cancelled {
		t.Fatalf("state = %s", got.State)
	}
	if !slices.Equal(w.waker.woke, []string{dev.ID}) {
		t.Errorf("woke = %v, want the waiting character released", w.waker.woke)
	}
	if len(w.all(dev)) != 1 {
		t.Errorf("a cancelled wait left a wake: %+v", w.all(dev))
	}
}

func TestAWaitOfACharacterThatEndsIsCancelled(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	master := w.dev("42-login", "Mestre")
	w.dev("42-login", "Revisor", master)
	wt, _ := w.svc.WaitFor(w.ctx, waits.WaitRequest{Waiter: master, Names: []string{"Revisor"}})
	w.set(master, characters.Stopping)
	w.set(master, characters.Stopped)
	if got, _ := w.svc.Store().GetWait(w.ctx, wt.ID); got.State != waits.Cancelled {
		t.Fatalf("state = %s", got.State)
	}
	if _, ok, _ := w.svc.Active(w.ctx, master.ID); ok {
		t.Error("a cancelled wait is active")
	}
}

func TestTheMapOfWaitsShowsWaitsAsksAndDeadlocks(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	master := w.add(characters.RoleTableMaster, "42-login", characters.NameTableMaster)
	a := w.dev("42-login", "A", master)
	b := w.dev("42-login", "B", a, master)
	c := w.dev("42-login", "C", b, master)
	w.dev("43-api", "Z")
	w.quests.owner["step-a"] = a.ID
	if _, err := w.svc.WaitFor(w.ctx, waits.WaitRequest{Waiter: a, Names: []string{"B"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.WaitSteps(w.ctx, waits.StepsRequest{Waiter: b, Steps: []waits.Target{{ID: "step-a", Name: "etapa de A"}}}); err != nil {
		t.Fatal(err)
	}
	w.askAsync(waits.AskRequest{From: c, To: b, Prompt: "x"})
	m, err := w.svc.Map(w.ctx, workspace, "42-login", false)
	if err != nil || len(m.Rows) != 3 || len(m.Cycles) != 1 {
		t.Fatalf("Map = %+v, %v", m, err)
	}
	if m.Rows[0].Waiter != "A" || m.Rows[0].Target != "B" || m.Rows[0].Kind != "character" || m.Rows[1].Kind != "steps" || m.Rows[2].Kind != "ask" || m.Rows[2].Waiter != "C" {
		t.Errorf("rows = %+v", m.Rows)
	}
	other, _ := w.svc.Map(w.ctx, workspace, "43-api", false)
	if len(other.Rows) != 0 {
		t.Errorf("another table shows %+v", other.Rows)
	}
	all, _ := w.svc.Map(w.ctx, workspace, "", true)
	if len(all.Rows) != 3 {
		t.Errorf("all = %+v", all.Rows)
	}
}

// ---- the commands ----

func (w *world) forwarder() *localchan.LocalForwarder {
	fwd := localchan.NewLocalForwarder()
	w.svc.Register(fwd)
	return fwd
}

func call(w *world, fwd *localchan.LocalForwarder, agent *characters.Character, cmd string, flags map[string]any, args ...string) localchan.Response {
	w.t.Helper()
	c := &localchan.Call{Request: localchan.Request{Cmd: cmd, Args: localchan.TextArgs(args...), Flags: flags}}
	if agent != nil {
		c.Caller = localchan.Caller{Identified: true, AgentID: agent.ID}
	}
	return fwd.Forward(w.ctx, c)
}

// CA-ESP-09
func TestAskRawIsRefusedForABusyTargetAndOneWaitingForAPermissionAndTypedForOneWaitingForInput(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	master := w.add(characters.RoleTableMaster, "42-login", characters.NameTableMaster)
	busy := w.dev("42-login", "Ocupado", master)
	perm := w.dev("42-login", "Permissao", master)
	input := w.dev("42-login", "Entrada", master)
	w.set(busy, characters.Busy)
	w.set(perm, characters.Busy)
	w.setWith(perm, characters.Blocked, characters.ReasonPermission)
	w.set(input, characters.Busy)
	w.setWith(input, characters.Blocked, characters.ReasonInput)
	fwd := w.forwarder()
	r := call(w, fwd, &master, "ask", map[string]any{"raw": `y\n`}, "Ocupado")
	if r.OK || r.Error.Code != "busy" || r.ExitCode() != localchan.ExitConflict {
		t.Errorf("busy: %+v", r)
	}
	r = call(w, fwd, &master, "ask", map[string]any{"raw": `y\n`}, "Permissao")
	if r.OK || r.Error.Code != "permission_prompt" || r.ExitCode() != localchan.ExitDenied {
		t.Errorf("permission: %+v", r)
	}
	if got := w.terms.typedIn(busy.ID); len(got) != 0 {
		t.Errorf("typed %q in the busy target", got)
	}
	if got := w.terms.typedIn(perm.ID); len(got) != 0 {
		t.Errorf("typed %q in the target that waits for a permission", got)
	}
	r = call(w, fwd, &master, "ask", map[string]any{"raw": `y\n`}, "Entrada")
	if !r.OK {
		t.Fatalf("input: %+v", r)
	}
	if got := w.terms.typedIn(input.ID); string(got) != "y\n" {
		t.Errorf("typed %q in the target that waits for input, want `y\\n` at once", got)
	}
	// the queue is not used: nothing was put in any inbox
	for _, c := range []characters.Character{busy, perm, input} {
		if items := w.all(c); len(items) != 0 {
			t.Errorf("%s got %+v: --raw does not go through the queue", c.Name, items)
		}
	}
	// the runtime is the last word: it refuses what the registry thought free
	w.terms.err[input.ID] = characters.ErrBusy
	if r := call(w, fwd, &master, "ask", map[string]any{"raw": "x"}, "Entrada"); r.OK || r.Error.Code != "busy" {
		t.Errorf("the runtime refused: %+v", r)
	}
}

// Part of CA-CLI-COM-03 (it closes with the CLI of communication): the escapes
// of --raw become the bytes of the document.
func TestRawEscapesBecomeTheBytesTyped(t *testing.T) {
	t.Parallel()
	got, err := waits.UnescapeRaw(`1\n\x1b[B`)
	if err != nil || string(got) != "\x31\x0a\x1b\x5b\x42" {
		t.Fatalf("UnescapeRaw = % x, %v, want 31 0a 1b 5b 42", got, err)
	}
	for in, want := range map[string]string{`\e[A`: "\x1b[A", `a\tb`: "a\tb", `\x03`: "\x03", `\\n`: `\n`, `\r`: "\r", `plain`: "plain", `\X0A`: ""} {
		out, err := waits.UnescapeRaw(in)
		if want == "" {
			if err == nil {
				t.Errorf("%q was accepted", in)
			}
			continue
		}
		if err != nil || string(out) != want {
			t.Errorf("UnescapeRaw(%q) = %q, %v, want %q", in, out, err, want)
		}
	}
	for _, bad := range []string{`\`, `\x`, `\x1`, `\xZZ`, `\q`} {
		if _, err := waits.UnescapeRaw(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestAskRawIntoTheTerminalOfASpawnWorksInAnyState(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	master := w.add(characters.RoleTableMaster, "42-login", characters.NameTableMaster)
	term := w.dev("42-login", "Shell", master)
	// a simple terminal is busy while a command runs, and takes input
	if _, _, err := w.reg.Transition(w.ctx, term.ID, characters.Busy, characters.Change{At: w.clk.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.Writer().Exec(`UPDATE characters SET kind = 'terminal' WHERE id = ?`, term.ID); err != nil {
		t.Fatal(err)
	}
	r := call(w, w.forwarder(), &master, "ask", map[string]any{"raw": `ls\n`}, "Shell")
	if !r.OK || string(w.terms.typedIn(term.ID)) != "ls\n" {
		t.Fatalf("raw into a spawn: %+v, typed %q", r, w.terms.typedIn(term.ID))
	}
}

// Part of CA-ESP-04, the command: past the time limit the answer is exit 6, pending,
// "sem resposta no prazo".
func TestTheAskCommandAnswersPendingBatchAndNotConnected(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.dev("42-login", "Dev")
	rev := w.dev("42-login", "Revisor", dev)
	w.dev("42-login", "QA", dev)
	other := w.dev("42-login", "Estranho")
	fwd := w.forwarder()
	// not connected: exit 3 before anything is sent
	r := call(w, fwd, &dev, "ask", nil, "Estranho", "oi")
	if r.OK || r.Error.Code != "not_connected" || r.ExitCode() != localchan.ExitDenied || !strings.Contains(r.Error.Message, "Estranho não está conectado a você") {
		t.Errorf("not connected: %+v", r)
	}
	if items := w.all(other); len(items) != 0 {
		t.Errorf("the stranger got %+v", items)
	}
	if r := call(w, fwd, &dev, "ask", nil, "Dev", "oi"); r.OK || r.ExitCode() != localchan.ExitUsage {
		t.Errorf("asking oneself: %+v", r)
	}
	if r := call(w, fwd, &dev, "ask", nil, "Revisor"); r.OK || r.ExitCode() != localchan.ExitUsage {
		t.Errorf("missing prompt: %+v", r)
	}
	// the answer
	done := make(chan localchan.Response, 1)
	armed := w.clk.Armed()
	go func() { done <- call(w, fwd, &dev, "ask", nil, "Revisor", "revise") }()
	w.clk.BlockUntil(armed + 1)
	w.done(rev, "aprovado")
	w.turnEnds(rev, "")
	resp := <-done
	var data struct {
		Status, Response, To, RequestID string
	}
	if err := resp.DecodeData(&data); err != nil || !resp.OK || data.Status != "answered" || data.Response != "aprovado" || data.To != "Revisor" {
		t.Fatalf("answered: %+v (%v) %+v", resp, err, data)
	}
	// the time limit: exit 6, pending, do not resend
	armed = w.clk.Armed()
	go func() { done <- call(w, fwd, &dev, "ask", map[string]any{"timeout": "5m"}, "Revisor", "e agora?") }()
	w.clk.BlockUntil(armed + 1)
	w.clk.Advance(5 * time.Minute)
	resp = <-done
	if resp.OK || resp.ExitCode() != localchan.ExitTimeout || resp.Error.Details["status"] != "pending" || !strings.Contains(resp.Error.Message, "sem resposta no prazo") {
		t.Fatalf("pending: %+v", resp)
	}
	// the maximum is 9 minutes, whatever is asked for
	armed = w.clk.Armed()
	go func() { done <- call(w, fwd, &dev, "ask", map[string]any{"timeout": "2h"}, "Revisor", "e então?") }()
	w.clk.BlockUntil(armed + 1)
	w.clk.Advance(9 * time.Minute)
	if resp = <-done; resp.ExitCode() != localchan.ExitTimeout {
		t.Fatalf("the limit of 9 minutes: %+v", resp)
	}
	// --no-wait
	r = call(w, fwd, &dev, "ask", map[string]any{"no-wait": true}, "QA", "teste")
	if !r.OK {
		t.Fatalf("no-wait: %+v", r)
	}
	var sent struct{ Status string }
	if err := r.DecodeData(&sent); err != nil || sent.Status != "sent" {
		t.Errorf("no-wait status = %+v (%v)", sent, err)
	}
}

// CA-CLI-COM-02 (the part of the engine): both answered, a JSON with a result
// per name and exit 0; one pending makes it 6.
func TestAskBatchAsksAllInParallelAndReturnsAResultForEach(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.dev("42-login", "Dev")
	a := w.dev("42-login", "A", dev)
	b := w.dev("42-login", "B", dev)
	fwd := w.forwarder()
	run := func() <-chan localchan.Response {
		done := make(chan localchan.Response, 1)
		armed := w.clk.Armed()
		go func() { done <- call(w, fwd, &dev, "ask", map[string]any{"batch": `{"A": "x", "B": "y"}`}) }()
		w.clk.BlockUntil(armed + 2)
		return done
	}
	done := run()
	w.done(a, "resposta de A")
	w.turnEnds(a, "")
	w.done(b, "resposta de B")
	w.turnEnds(b, "")
	resp := <-done
	var data struct {
		Results map[string]struct{ Status, Response string }
		Text    string
	}
	if err := resp.DecodeData(&data); err != nil || !resp.OK || resp.ExitCode() != 0 || data.Results["A"].Response != "resposta de A" || data.Results["B"].Status != "answered" ||
		!strings.Contains(data.Text, `"results"`) {
		t.Fatalf("batch = %+v (%v) %+v", resp, err, data)
	}
	done = run()
	w.done(a, "A de novo")
	w.turnEnds(a, "")
	w.clk.Advance(90 * time.Second) // B does not answer
	resp = <-done
	if err := resp.DecodeData(&data); err != nil || resp.ExitCode() != localchan.ExitTimeout || data.Results["A"].Status != "answered" || data.Results["B"].Status != "pending" {
		t.Fatalf("batch with a pending = %+v (%v) %+v", resp, err, data)
	}
	if r := call(w, fwd, &dev, "ask", map[string]any{"batch": `{"A": "x", "Estranho": "y"}`}); r.OK || r.Error.Code == "" {
		t.Errorf("a batch with a name that is not connected = %+v", r)
	}
}

func TestCheckReadsTheScreenInAnyStateAndFailsForWhoIsNotConnected(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.dev("42-login", "Dev")
	rev := w.dev("42-login", "Revisor", dev)
	w.dev("42-login", "Estranho")
	w.set(rev, characters.Busy)
	w.terms.screen[rev.ID] = characters.Screen{Text: "revisando o arquivo login.go"}
	fwd := w.forwarder()
	r := call(w, fwd, &dev, "check", nil, "Revisor")
	var data struct {
		Name, Status, Screen string
		CapturedAt           string `json:"captured_at"`
	}
	if err := r.DecodeData(&data); err != nil || !r.OK || data.Screen != "revisando o arquivo login.go" || data.Name != "Revisor" || data.Status != "busy" || data.CapturedAt == "" {
		t.Fatalf("check = %+v (%v) %+v", r, err, data)
	}
	if items := w.all(rev); len(items) != 0 {
		t.Errorf("check sent %+v", items)
	}
	if r := call(w, fwd, &dev, "check", nil, "Estranho"); r.OK || r.Error.Code != "not_connected" {
		t.Errorf("check of a stranger = %+v", r)
	}
}

// Part of CA-CLI-COM-08 (it closes with the CLI of communication): `wait`
// registers and returns at once with the id of the wait, and the caller then
// shows in `mestre list` as waiting for the Revisor.
func TestWaitRegistersAndReturnsAtOnceAndTheCallerShowsAsWaiting(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.dev("42-login", "Dev")
	w.dev("42-login", "Revisor", dev)
	fwd := w.forwarder()
	w.conns.Register(fwd)
	before := w.clk.Now()
	r := call(w, fwd, &dev, "wait", map[string]any{"timeout": "10m"}, "Revisor")
	if w.clk.Now() != before {
		t.Fatal("the call took time")
	}
	var data struct {
		WaitID    string `json:"wait_id"`
		Kind      string
		Targets   []string
		TimeoutAt string `json:"timeout_at"`
	}
	if err := r.DecodeData(&data); err != nil || !r.OK || data.WaitID == "" || data.Kind != "character" || !slices.Equal(data.Targets, []string{"Revisor"}) || data.TimeoutAt == "" {
		t.Fatalf("wait = %+v (%v) %+v", r, err, data)
	}
	// the turn of the caller ends: it is waiting, with the reason of the wait
	w.set(dev, characters.Busy)
	reason, ok, err := w.svc.Active(w.ctx, dev.ID)
	if err != nil || !ok {
		t.Fatalf("Active = %q, %v, %v", reason, ok, err)
	}
	w.setWith(dev, characters.Waiting, reason)
	rev, _ := w.reg.ByName(w.ctx, workspace, "42-login", "Revisor")
	lr := call(w, fwd, &rev, "list", nil)
	var list struct {
		Agents []struct {
			Name, Status string
			WaitingFor   string `json:"waiting_for"`
		}
	}
	if err := lr.DecodeData(&list); err != nil || len(list.Agents) != 1 || list.Agents[0].Name != "Dev" || list.Agents[0].Status != "aguardando Revisor" || list.Agents[0].WaitingFor != "Revisor" {
		t.Fatalf("list = %+v (%v) %+v", lr, err, list)
	}
	// the errors
	if r := call(w, fwd, &dev, "wait", nil, "Dev"); r.OK || r.ExitCode() != localchan.ExitUsage {
		t.Errorf("wait for oneself = %+v", r)
	}
	if r := call(w, fwd, &dev, "wait", nil, "Fantasma"); r.OK || r.ExitCode() != localchan.ExitNotFound {
		t.Errorf("wait for a name that does not exist = %+v", r)
	}
	if r := call(w, fwd, &dev, "wait", map[string]any{"until": "11:00"}); r.OK || r.ExitCode() != localchan.ExitUsage {
		t.Errorf("wait for a time in the past = %+v", r)
	}
	if r := call(w, fwd, &dev, "wait", map[string]any{"quest": "Missão X"}); r.OK || r.ExitCode() != localchan.ExitNotFound {
		t.Errorf("wait for a quest that does not exist = %+v", r)
	}
	w.quests.by["Login"] = waits.QuestInfo{ID: "q1", Name: "Login", Steps: []waits.StepInfo{{ID: "s1", Name: "dev"}, {ID: "s2", Name: "qa"}}}
	r = call(w, fwd, &dev, "wait", map[string]any{"quest": "Login"})
	if err := r.DecodeData(&data); err != nil || !r.OK || data.Kind != "steps" {
		t.Errorf("wait --quest = %+v (%v)", r, err)
	}
	if r := call(w, fwd, &dev, "wait", map[string]any{"until": "15:00"}); !r.OK {
		t.Errorf("wait --until = %+v", r)
	}
	if r := call(w, fwd, &dev, "wait", nil); r.OK || r.ExitCode() != localchan.ExitUsage {
		t.Errorf("wait with nothing = %+v", r)
	}
}

func TestTheWaitsCommandListsTheMapAndTheCycles(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	master := w.add(characters.RoleTableMaster, "42-login", characters.NameTableMaster)
	a := w.dev("42-login", "A", master)
	b := w.dev("42-login", "B", a, master)
	gm := w.gm()
	for _, r := range []waits.WaitRequest{{Waiter: a, Names: []string{"B"}}, {Waiter: b, Names: []string{"A"}}} {
		if _, err := w.svc.WaitFor(w.ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	fwd := w.forwarder()
	r := call(w, fwd, &a, "waits", nil)
	var data struct {
		Text   string
		Waits  []struct{ ID, Waiter, Target, Kind string }
		Cycles [][]string
	}
	if err := r.DecodeData(&data); err != nil || !r.OK || len(data.Waits) != 2 || len(data.Cycles) != 1 || !slices.Equal(data.Cycles[0], []string{"A", "B", "A"}) ||
		!strings.Contains(data.Text, "A -> B") || !strings.Contains(data.Text, "CYCLE: A -> B -> A") {
		t.Fatalf("waits = %+v (%v) %+v", r, err, data)
	}
	if r := call(w, fwd, &a, "waits", map[string]any{"all": true}); r.OK || r.Error.Code != "level_denied" {
		t.Errorf("--all by a character = %+v", r)
	}
	if r := call(w, fwd, &gm, "waits", nil); !r.OK {
		t.Errorf("waits by the Grand Master = %+v", r)
	}
	if r := call(w, fwd, nil, "waits", nil); r.OK || r.Error.Code != localchan.CodeNotIdentified {
		t.Errorf("waits unidentified = %+v", r)
	}
}
