package telegram_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/chain"
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
