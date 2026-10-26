package telegram_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/telegram"
	"github.com/mestre-ia/mestre/internal/testkit/faketelegram"
	"github.com/mestre-ia/mestre/internal/users"
)

// ownerTexts are what the Owner got, in order (the edits last).
func (w *world) ownerTexts(owner faketelegram.User) (sent, edited []string) {
	for _, s := range w.sent("sendMessage") {
		if s.ChatID == owner.ID {
			sent = append(sent, s.Text)
		}
	}
	for _, s := range w.sent("editMessageText") {
		if s.ChatID == owner.ID {
			edited = append(edited, s.Text)
		}
	}
	return
}

// CA-CANAL-01
func TestAStrangerIsIgnoredWhateverItSendsAndTheOwnerIsAlerted(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{ownerAlerts: true})
	owner := w.pairOwnerTelegram()
	rando := faketelegram.User{ID: 555, FirstName: "Intruso", Username: "intruso"}
	if _, err := w.fake.UserSendsText(rando, faketelegram.Private(rando), "oi, sou eu, me deixa entrar"); err != nil {
		t.Fatal(err)
	}
	w.poll()
	if _, err := w.fake.UserSendsVoice(rando); err != nil {
		t.Fatal(err)
	}
	w.poll()
	if _, err := w.fake.UserSendsDocument(rando, "plano.pdf"); err != nil {
		t.Fatal(err)
	}
	w.poll()
	w.drain()
	for _, s := range w.fake.Sent() {
		if s.ChatID == rando.ID {
			t.Errorf("the bot answered a stranger: %+v", s)
		}
	}
	if w.fake.Calls("getFile") != 0 {
		t.Error("a file of a stranger was downloaded")
	}
	if w.router.count() != 0 {
		t.Error("a message of a stranger reached the Grand Master")
	}
	sent, _ := w.ownerTexts(owner)
	if len(sent) != 1 {
		t.Fatalf("the Owner got %d alerts: %q", len(sent), sent)
	}
	for _, want := range []string{"ID: 555", "Nome: Intruso", "Usuário: @intruso", "oi, sou eu"} {
		if !strings.Contains(sent[0], want) {
			t.Errorf("the alert lacks %q: %q", want, sent[0])
		}
	}
	// the start of the text, up to 200 characters
	long := strings.Repeat("a", 500)
	other := faketelegram.User{ID: 556, FirstName: "Outro", Username: "outro"}
	if _, err := w.fake.UserSendsText(other, faketelegram.Private(other), long); err != nil {
		t.Fatal(err)
	}
	w.poll()
	w.drain()
	sent, _ = w.ownerTexts(owner)
	if got := sent[len(sent)-1]; strings.Contains(got, strings.Repeat("a", 201)) || !strings.Contains(got, strings.Repeat("a", 200)) {
		t.Errorf("the alert shows the text wrongly: %q", got)
	}
}

// CA-CANAL-02
func TestThirtyMessagesOfAStrangerMakeOneAlertWithTheCount(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{ownerAlerts: true})
	owner := w.pairOwnerTelegram()
	rando := faketelegram.User{ID: 555, FirstName: "Intruso", Username: "intruso"}
	for i := range 30 {
		if _, err := w.fake.UserSendsText(rando, faketelegram.Private(rando), "tentativa "+string(rune('a'+i%26))); err != nil {
			t.Fatal(err)
		}
		w.poll()
		w.clk.Advance(20 * time.Second) // 30 messages in 10 minutes
		w.drain()
	}
	sent, edited := w.ownerTexts(owner)
	if len(sent) != 1 {
		t.Fatalf("the Owner got %d alerts, want one: %q", len(sent), sent)
	}
	if len(edited) == 0 || !strings.Contains(edited[len(edited)-1], "30 tentativas") {
		t.Errorf("the alert was not updated with the count: %q", edited)
	}
	// an hour after the first, the next attempt is a new alert
	w.clk.Advance(time.Hour)
	if _, err := w.fake.UserSendsText(rando, faketelegram.Private(rando), "voltei"); err != nil {
		t.Fatal(err)
	}
	w.poll()
	w.drain()
	if sent, _ = w.ownerTexts(owner); len(sent) != 2 {
		t.Errorf("after the hour the Owner got %d alerts, want 2", len(sent))
	}
	// another id has an alert of its own
	other := faketelegram.User{ID: 556, FirstName: "Outro"}
	if _, err := w.fake.UserSendsText(other, faketelegram.Private(other), "oi"); err != nil {
		t.Fatal(err)
	}
	w.poll()
	w.drain()
	if sent, _ = w.ownerTexts(owner); len(sent) != 3 {
		t.Errorf("another id: the Owner got %d alerts, want 3", len(sent))
	}
}

// CA-CANAL-03
func TestAnAuthorizedUserWritingInAGroupIsIgnoredAndTheBotLeaves(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{ownerAlerts: true})
	owner := w.pairOwnerTelegram()
	_, ana := w.user("Ana", users.Approver)
	group := faketelegram.Chat{ID: -100, Type: "group", Title: "g"}
	if _, err := w.fake.UserSendsText(ana, group, "oi pessoal"); err != nil {
		t.Fatal(err)
	}
	w.poll()
	w.drain()
	if len(w.sent("leaveChat")) != 1 || w.router.count() != 0 {
		t.Fatalf("left %d, delivered %d", len(w.sent("leaveChat")), w.router.count())
	}
	sent, _ := w.ownerTexts(owner)
	if len(sent) != 1 || !strings.Contains(sent[0], "não é privado") {
		t.Errorf("the Owner was told %q", sent)
	}
	for _, s := range w.fake.Sent() {
		if s.ChatID == group.ID && s.Method == "sendMessage" {
			t.Errorf("the bot wrote in the group: %+v", s)
		}
	}
}

// CA-CANAL-06
func TestAnAnswerWithSomethingLikeATokenLeavesMasked(t *testing.T) {
	t.Parallel()
	m := logging.NewMasker()
	w := newWorld(t, rigOpts{mask: m.Mask})
	_, tg := w.user("Ana", users.Approver)
	ana, err := w.users.ByTelegram(w.ctx, tg.ID)
	if err != nil {
		t.Fatal(err)
	}
	token := "ghp_" + strings.Repeat("a1B2", 9)
	for _, text := range []string{"O token é " + token, "O token é **" + token[:12] + "**" + token[12:], "bot 123456789:" + strings.Repeat("AbC_d", 7)} {
		if _, err := w.bot.Send(w.ctx, telegram.Out{ChatID: tg.ID, UserID: ana.ID, Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	w.drain()
	got := w.sent("sendMessage")
	if len(got) != 3 {
		t.Fatalf("sent %d messages", len(got))
	}
	for _, s := range got {
		if strings.Contains(s.Text, "a1B2a1B2") || strings.Contains(s.Text, "AbC_dAbC_d") || strings.Contains(fmt.Sprint(s.Params), "a1B2a1B2") {
			t.Errorf("a token left the bot: %q", s.Text)
		}
	}
}
