//go:build full

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/testkit/faketelegram"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// CA-IDIOMA-10
func TestTheGrandMasterGetsTheLanguageOfTheUserInTheHeaderAndItsReplyArrivesAsWritten(t *testing.T) {
	t.Parallel()
	w := newChatWorld(t)
	if err := w.run.users.SetLanguage(w.ctx, w.bia.ID, "en"); err != nil {
		t.Fatal(err)
	}
	// the lease of the epoch is valid, so the gate lets the bot act (as in approvalhost_test.go)
	fence := guild.NewFence(guild.FenceConfig{Lock: epochLock{}, Clock: w.clk, Epoch: func() uint64 { return 1 }})
	w.run.BindFence(fence)
	go func() { _ = fence.Run(w.ctx) }()
	for i := 0; i < 15000 && !fence.LeaseValid(); i++ {
		w.clk.Advance(guild.LeaseInterval)
		<-clock.Real{}.NewTimer(2 * time.Millisecond).C()
	}
	if !fence.LeaseValid() {
		t.Fatal("the lease of the epoch did not become valid")
	}
	bot := w.run.currentBot()
	if bot == nil {
		t.Fatal("no bot")
	}
	bia := faketelegram.User{ID: biaChat, FirstName: "Bia", Username: "bia", LanguageCode: "pt-BR"} // the choice beats the hint
	if _, err := w.tg.UserSendsText(bia, faketelegram.Private(bia), "how is the login table?"); err != nil {
		t.Fatal(err)
	}
	if err := bot.PollOnce(w.ctx); err != nil {
		t.Fatal(err)
	}
	headers, typed := w.machines.Headers(w.gm), w.machines.Delivered(w.gm)
	if len(headers) != 1 || len(typed) != 1 || typed[0] != "how is the login table?" {
		t.Fatalf("what the Grand Master got: %q %q", headers, typed)
	}
	if !strings.Contains(headers[0], " · via Telegram · idioma en · ") {
		t.Fatalf("header %q lacks the language", headers[0])
	}
	// the agent answers in the language it was told, and the text arrives as it wrote it
	const answer = "A mesa do login está em revisão. Review pending."
	if r := w.forward("reply", nil, answer); !r.OK {
		t.Fatalf("mestre reply: %+v", r.Error)
	}
	found := false
	for _, p := range w.chatTexts(biaChat) {
		found = found || strings.Contains(p, answer)
	}
	if !found {
		t.Fatalf("the Telegram did not get the reply as written: %q", w.chatTexts(biaChat))
	}
	// the frame of the engine around it is in en: a notice of the engine for the same user
	w.run.alert.Tell(w.ctx, w.bia.ID, i18n.M("bot.table_paused", "table", "42"))
	if _, err := bot.Flush(w.ctx); err != nil {
		t.Fatal(err)
	}
	framed := false
	for _, p := range w.chatTexts(biaChat) {
		framed = framed || strings.Contains(p, "Table 42 paused.") && strings.Contains(p, "System")
	}
	if !framed {
		t.Fatalf("the frame of the engine is not in en: %q", w.chatTexts(biaChat))
	}
	if u, err := w.run.users.Get(w.ctx, w.bia.ID); err != nil || u.Language != "en" || u.TelegramLanguage != "pt-BR" {
		t.Fatalf("the user %+v, %v", u, err)
	}
}
