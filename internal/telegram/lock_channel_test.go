package telegram_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/telegram"
	"github.com/mestre-ia/mestre/internal/users"
)

func lockChanWorld(t *testing.T) *world {
	t.Helper()
	return newWorld(t, rigOpts{lockdown: true, commands: []telegram.Command{
		{Name: "status", Description: "estado", Query: true, Run: func(context.Context, telegram.Call) (string, error) { return "Cidadela online", nil }},
		{Name: "pausar", Description: "pausa", Capability: users.Pause, Run: func(context.Context, telegram.Call) (string, error) { return "pausado", nil }},
	}})
}

func texts(w *world, chat int64) []string {
	var out []string
	for _, s := range w.sent("sendMessage") {
		if s.ChatID == chat {
			out = append(out, s.Text)
		}
	}
	return out
}

// Part of CA-CANAL-04: /travar asks for a confirmation with a button, and only
// the confirmed one locks; the Approver is refused; everyone is told.
func TestTravarLocksAfterTheButtonAndEveryoneIsTold(t *testing.T) {
	t.Parallel()
	w := lockChanWorld(t)
	_, adm := w.user("Ana", users.Administrator)
	_, ap := w.user("Bia", users.Approver)
	w.say(ap, "/travar")
	w.drain()
	if w.lock.Locked(w.ctx) {
		t.Fatal("an Approver locked")
	}
	if got := texts(w, ap.ID); len(got) == 0 || !strings.Contains(got[len(got)-1], "/travar") {
		t.Errorf("the Approver was told %q", got)
	}
	w.say(adm, "/travar")
	w.drain()
	if w.lock.Locked(w.ctx) {
		t.Fatal("/travar locked before the confirmation")
	}
	msg, keys := w.buttons(adm)
	if keys["Confirmar travamento"] == "" {
		t.Fatalf("no button: %v", keys)
	}
	// a button of the lock is the clicker's: another user can not use it
	w.click(ap, msg, keys["Confirmar travamento"])
	if w.lock.Locked(w.ctx) {
		t.Fatal("the button of another user locked")
	}
	w.click(adm, msg, keys["Confirmar travamento"])
	if !w.lock.Locked(w.ctx) {
		t.Fatal("the confirmed /travar did not lock")
	}
	w.drain()
	for _, tg := range []int64{adm.ID, ap.ID} {
		var told bool
		for _, s := range texts(w, tg) {
			told = told || strings.Contains(s, "Ana travou o Mestre")
		}
		if !told {
			t.Errorf("chat %d was not told who locked", tg)
		}
	}
}

// Part of CA-CANAL-05: while locked the bot answers only the queries and
// /destravar, and keeps the messages; the Owner unlocks with the PIN and what
// was kept goes to the Grand Master, in order.
func TestWhileLockedTheBotAnswersQueriesAndKeepsTheMessagesUntilTheOwnerUnlocks(t *testing.T) {
	t.Parallel()
	w := lockChanWorld(t)
	if err := w.users.SetPIN(w.ctx, w.owner.ID, "123456"); err != nil {
		t.Fatal(err)
	}
	owner := w.pairOwnerTelegram()
	_, adm := w.user("Ana", users.Administrator)
	if err := w.lock.Lock(w.ctx, mustUser(t, w, "Ana")); err != nil {
		t.Fatal(err)
	}
	w.drain()
	w.say(adm, "/status")
	w.drain()
	if got := texts(w, adm.ID); !strings.Contains(strings.Join(got, "|"), "Cidadela online") {
		t.Errorf("a query was not answered while locked: %q", got)
	}
	w.say(adm, "/pausar")
	w.drain()
	if got := texts(w, adm.ID); !strings.Contains(got[len(got)-1], "travado") {
		t.Errorf("a command was answered while locked: %q", got)
	}
	// chat messages are kept
	w.say(adm, "primeira mensagem")
	w.say(owner, "segunda mensagem")
	w.drain()
	if w.router.count() != 0 {
		t.Fatalf("%d messages reached the Grand Master while locked", w.router.count())
	}
	// an Administrator does not unlock
	w.say(adm, "/destravar")
	w.drain()
	if !w.lock.Locked(w.ctx) {
		t.Fatal("an Administrator unlocked")
	}
	// the Owner: PIN prompt, a wrong PIN, the right one
	w.say(owner, "/destravar")
	w.drain()
	w.say(owner, "000000")
	w.drain()
	if !w.lock.Locked(w.ctx) {
		t.Fatal("a wrong PIN unlocked")
	}
	w.say(owner, "123456")
	w.drain()
	if w.lock.Locked(w.ctx) {
		t.Fatal("the right PIN did not unlock")
	}
	got := w.router.all()
	if len(got) != 2 || got[0].Text != "primeira mensagem" || got[1].Text != "segunda mensagem" {
		t.Fatalf("the Grand Master got %+v, want the two messages in order", got)
	}
	// delivered once: no more in the store
	if held, _ := w.bot.Store().Held(w.ctx); len(held) != 0 {
		t.Errorf("%d messages still held", len(held))
	}
	// the PIN never stayed in the chat
	for _, s := range w.fake.Sent() {
		if strings.Contains(s.Text, "123456") || strings.Contains(s.Text, "000000") {
			t.Errorf("a PIN went out: %q", s.Text)
		}
	}
}

func mustUser(t *testing.T, w *world, name string) users.User {
	t.Helper()
	all, err := w.users.List(w.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range all {
		if u.Name == name {
			return u
		}
	}
	t.Fatalf("no user %s", name)
	return users.User{}
}

func TestTravarWhenLockedAndDestravarWhenNotSayWhatIs(t *testing.T) {
	t.Parallel()
	w := lockChanWorld(t)
	owner := w.pairOwnerTelegram()
	w.say(owner, "/destravar")
	w.drain()
	if got := texts(w, owner.ID); !strings.Contains(strings.Join(got, "|"), "não está travado") {
		t.Errorf("destravar when not locked: %q", got)
	}
	if err := w.lock.Lock(w.ctx, w.owner); err != nil {
		t.Fatal(err)
	}
	w.say(owner, "/travar")
	w.drain()
	if got := texts(w, owner.ID); !strings.Contains(got[len(got)-1], "travado") {
		t.Errorf("travar when locked: %q", got)
	}
}
