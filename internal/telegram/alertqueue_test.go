package telegram_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/telegram"
	"github.com/mestre-ia/mestre/internal/users"
)

// Part of CA-BOT-13: the operational alerts that come while the bot is not up wait in a
// bounded queue (the oldest is dropped) and go out once, when the bot is bound, only to
// the Owner and the Administrators who act in the workspace and are paired; an alert
// after the bind goes straight out.
func TestTheOperationalAlertsThatWaitForTheBotGoOutOnceWhenItIsBound(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{block: true})
	owner := w.pairOwnerTelegram()
	_, ana := w.user("Ana", users.Administrator, "loja")
	_, bia := w.user("Bia", users.Approver, "loja")
	_, cris := w.user("Cris", users.Administrator, "blog")
	al := &telegram.OwnerAlerts{Users: w.users, Clock: w.clk}
	for i := range 25 {
		if _, err := al.AdminAlert(w.ctx, "loja", "alerta "+strings.Repeat("x", i)); !errors.Is(err, telegram.ErrQueued) {
			t.Fatalf("alert %d with the bot down: %v; want it queued", i, err)
		}
	}
	al.Bind(w.bot)
	for range 10 { // the bot sends one message a second to each chat
		w.drain()
	}
	count := map[int64]int{}
	for _, s := range w.sent("sendMessage") {
		if strings.Contains(s.Text, "alerta ") {
			count[s.ChatID]++
		}
	}
	if count[owner.ID] != 20 || count[ana.ID] != 20 || count[bia.ID] != 0 || count[cris.ID] != 0 {
		t.Errorf("messages by chat after the bind = %v; want 20 each to the Owner and Ana (the queue keeps 20) and none to the others", count)
	}
	// bound: straight out, and nothing waits any more
	if told, err := al.AdminAlert(w.ctx, "loja", "alerta depois"); err != nil || told != 2 {
		t.Fatalf("alert after the bind: told %d, %v; want 2", told, err)
	}
	al.Bind(w.bot) // a second bind sends nothing again
	for range 3 {
		w.drain()
	}
	n := 0
	for _, s := range w.sent("sendMessage") {
		if strings.Contains(s.Text, "alerta ") {
			n++
		}
	}
	if n != 42 {
		t.Errorf("%d messages in all; want 40 from the queue and 2 of the alert after the bind", n)
	}
}
