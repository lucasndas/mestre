package telegram_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/digest"
	"github.com/lucasndas/mestre/internal/telegram"
	"github.com/lucasndas/mestre/internal/users"
)

// fakeTablePauser is the supervision behind the button Pausar mesa.
type fakeTablePauser struct {
	mu     sync.Mutex
	paused []chain.PauseRequest
}

func (f *fakeTablePauser) PauseStalled(_ context.Context, r chain.PauseRequest) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paused = append(f.paused, r)
	return true, nil
}

// Part of CA-SUPCAD-06: the warning of a table that spins reaches the Approvers
// and above of the workspace, with the button Pausar mesa; the button pauses the
// table in the name of who touched it, and an Observer has neither the message nor
// the button.
func TestTheWarningOfAStalledTableHasTheButtonToPauseItForAnApproverOfTheWorkspace(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{block: true})
	w.pairOwnerTelegram()
	_, approver := w.user("Caio", users.Approver, "loja")
	_, observer := w.user("Edu", users.Observer, "loja")
	_, elsewhere := w.user("Bia", users.Approver, "blog")
	err := w.sc.StalledTable(w.ctx, chain.StallNotice{Workspace: "loja", Table: "42-login", Since: w.clk.Now().Add(-3 * time.Hour),
		Text: "A mesa 42-login está em trabalho há 3h sem entregar nada."})
	if err != nil {
		t.Fatal(err)
	}
	w.drain()
	for _, s := range w.sent("sendMessage") {
		if s.ChatID == observer.ID || s.ChatID == elsewhere.ID {
			t.Errorf("the warning reached %d, who may not pause the table", s.ChatID)
		}
	}
	msg, keys := w.buttons(approver)
	if keys["Pausar mesa"] == "" {
		t.Fatalf("the Approver has the buttons %v", keys)
	}
	w.click(approver, msg, keys["Pausar mesa"])
	w.pauser.mu.Lock()
	defer w.pauser.mu.Unlock()
	if len(w.pauser.paused) != 1 {
		t.Fatalf("the button paused %d tables; want 1", len(w.pauser.paused))
	}
	got := w.pauser.paused[0]
	if got.Workspace != "loja" || got.Table != "42-login" || got.By.UserLevel != "approver" || !strings.Contains(got.Reason, "sem entregar") {
		t.Errorf("pause = %+v", got)
	}
}

// CA-USR-10
//
// The bot half of the criterion: an Approver taps Pausar mesa and the table is
// paused in his name; /pausar and Foi um erro are refused with the level that
// they need (Administrador); an Observer has none of the three. The resume of the
// pause of a person by an Approver is the Supervisor's (CA-SUPCAD-05, in
// internal/orchestration/chain: an Approver resumes it, an Observer is refused).
func TestAnApproverPausesTheTableByTheButtonButIsRefusedTheCommandAndTheMistakeButtonAndAnObserverHasNone(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{block: true, decrees: true, digest: true, commands: []telegram.Command{
		{Name: "pausar", Description: "pausa", Capability: users.Pause, Run: func(context.Context, telegram.Call) (string, error) { return "pausado", nil }},
	}})
	w.pairOwnerTelegram()
	apr, approver := w.user("Caio", users.Approver, "loja")
	obs, observer := w.user("Edu", users.Observer, "loja")
	// the warning of a table that spins reaches the Approver, with the button
	if err := w.sc.StalledTable(w.ctx, chain.StallNotice{Workspace: "loja", Table: "42-login", Since: w.clk.Now().Add(-3 * time.Hour), Text: "A mesa 42-login gira."}); err != nil {
		t.Fatal(err)
	}
	w.drain()
	msg, keys := w.buttons(approver)
	w.click(approver, msg, keys["Pausar mesa"])
	w.pauser.mu.Lock()
	paused := len(w.pauser.paused)
	w.pauser.mu.Unlock()
	if paused != 1 {
		t.Fatalf("the Approver paused %d tables with the button; want 1", paused)
	}
	// /pausar: refused, with the level it needs
	w.say(approver, "/pausar")
	w.drain()
	if got := lastText(w, approver); !strings.Contains(got, "Administrador") || strings.Contains(got, "pausado") {
		t.Errorf("/pausar by an Approver answered %q; want the refusal with the level Administrador", got)
	}
	// Foi um erro, the button of the summary: refused at the tap, with the level
	if err := w.dg.Send(w.ctx, digest.Message{UserID: apr.ID, Workspace: "loja", Text: "Resumo: o Mestre da mesa criou a regra 1.",
		Buttons: [][]digest.Button{{{Text: "Foi um erro", Action: digest.MistakeAction, Arg: "1"}}}}); err != nil {
		t.Fatal(err)
	}
	w.drain()
	mmsg, mkeys := w.buttons(approver)
	if mkeys["Foi um erro"] == "" {
		t.Fatalf("the summary has the buttons %v", mkeys)
	}
	w.click(approver, mmsg, mkeys["Foi um erro"])
	if got := lastCallbackAnswer(w); !strings.Contains(got, "Administrador") {
		t.Errorf("Foi um erro by an Approver answered %q; want the refusal with the level Administrador", got)
	}
	// the Observer: not the command, and a button that reached him is refused too
	w.say(observer, "/pausar")
	w.drain()
	if got := lastText(w, observer); strings.Contains(got, "pausado") || got == "" {
		t.Errorf("/pausar by an Observer answered %q", got)
	}
	for _, b := range []telegram.Button{
		{Text: "Pausar mesa", Action: telegram.PauseTableAction, Arg: "loja:42-login", Capability: users.PauseButtons},
		{Text: "Foi um erro", Action: digest.MistakeAction, Arg: "1", Capability: users.RevokeTemporaryRules},
	} {
		if _, err := w.bot.Send(w.ctx, telegram.Out{ChatID: observer.ID, UserID: obs.ID, Text: "botão " + b.Text, Origin: "motor", Buttons: [][]telegram.Button{{b}}}); err != nil {
			t.Fatal(err)
		}
		w.drain()
		omsg, okeys := w.buttons(observer)
		w.click(observer, omsg, okeys[b.Text])
		if got := lastCallbackAnswer(w); !strings.Contains(got, "nível não permite") {
			t.Errorf("%s by an Observer answered %q; want the refusal by level", b.Text, got)
		}
	}
	w.pauser.mu.Lock()
	defer w.pauser.mu.Unlock()
	if len(w.pauser.paused) != 1 {
		t.Errorf("the tables paused = %d; the tap of the Observer paused one", len(w.pauser.paused))
	}
}

// lastCallbackAnswer is the text of the last answer the bot gave to a tap.
func lastCallbackAnswer(w *world) string {
	got := w.sent("answerCallbackQuery")
	if len(got) == 0 {
		return ""
	}
	return got[len(got)-1].Text
}
