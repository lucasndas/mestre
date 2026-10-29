package telegram_test

import (
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/telegram"
	"github.com/mestre-ia/mestre/internal/testkit/faketelegram"
	"github.com/mestre-ia/mestre/internal/users"
)

// CA-IDIOMA-01
func TestTheNoticeOfAPausedTableGoesInTheLanguageOfEachRecipient(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	chose, choseTG := w.user("Ana", users.Administrator, "loja")
	if err := w.users.SetLanguage(w.ctx, chose.ID, "en"); err != nil {
		t.Fatal(err)
	}
	hintEN, hintENTG := w.user("Bia", users.Administrator, "loja")
	hintENTG.LanguageCode = "en-US"
	hintDE, hintDETG := w.user("Caio", users.Administrator, "loja")
	hintDETG.LanguageCode = "de"
	// the last update of each one carries the language_code the Telegram knows
	w.say(hintENTG, "oi")
	w.say(hintDETG, "oi")
	for _, u := range []users.User{chose, hintEN, hintDE} {
		if _, err := w.bot.Send(w.ctx, telegram.Out{ChatID: u.TelegramID, UserID: u.ID, Msg: i18n.M("bot.table_paused", "table", "42"), Origin: "motor"}); err != nil {
			t.Fatal(err)
		}
	}
	w.drain()
	if got := lastText(w, choseTG); !strings.Contains(got, "Table 42 paused.") {
		t.Errorf("a user who chose en: %q", got)
	}
	if got := lastText(w, hintENTG); !strings.Contains(got, "Table 42 paused.") {
		t.Errorf("a user with no choice and en-US: %q", got)
	}
	if got := lastText(w, hintDETG); !strings.Contains(got, "Mesa 42 pausada.") {
		t.Errorf("a user with no choice and de: %q", got)
	}
	for _, id := range []string{hintEN.ID, hintDE.ID} {
		if u, err := w.users.Get(w.ctx, id); err != nil || u.Language != "" {
			t.Errorf("a hint was written as the language: %+v, %v", u, err)
		}
	}
}

// CA-IDIOMA-02
func TestAnInvalidPairingCodeIsAnsweredInTheLanguageOfTheUpdate(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	rando := faketelegram.User{ID: 777, FirstName: "Novo", LanguageCode: "en"}
	w.say(rando, strings.Repeat("A", users.CodeLength))
	w.drain()
	var got string
	for _, s := range w.sent("sendMessage") {
		if s.ChatID == rando.ID {
			got = s.Text
		}
	}
	if got != "Invalid or expired code." {
		t.Errorf("the answer to the stranger = %q", got)
	}
}

// CA-IDIOMA-03
func TestIdiomaWritesTheLanguageOfWhoTouchedAndAnswersInIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	u, tg := w.user("Ana", users.Approver, "loja")
	other, otherTG := w.user("Bia", users.Approver, "loja")
	w.say(tg, "/idioma")
	w.drain()
	if got := lastText(w, tg); !strings.Contains(got, "Idioma em uso: Português") {
		t.Fatalf("the command says %q", got)
	}
	msg, keys := w.buttons(tg)
	if len(keys) != 2 || keys["Português"] == "" || keys["English"] == "" {
		t.Fatalf("buttons %v, want Português and English", keys)
	}
	// the button of another person is refused and writes nothing
	w.click(otherTG, msg, keys["English"])
	if got, _ := w.users.Get(w.ctx, other.ID); got.Language != "" {
		t.Errorf("the language of another person was written: %q", got.Language)
	}
	if got, _ := w.users.Get(w.ctx, u.ID); got.Language != "" {
		t.Errorf("the language of the owner of the button was written by another: %q", got.Language)
	}
	w.click(tg, msg, keys["English"])
	if got, _ := w.users.Get(w.ctx, u.ID); got.Language != "en" {
		t.Errorf("Language = %q, want en", got.Language)
	}
	if edits := w.sent("editMessageText"); len(edits) == 0 || !strings.Contains(edits[len(edits)-1].Text, "Language: English.") {
		t.Errorf("the confirmation is not in the new language: %+v", edits)
	}
	// a value out of the list writes nothing
	if err := w.users.SetLanguage(w.ctx, u.ID, "de"); err == nil {
		t.Error("a language out of the list was accepted")
	}
	if got, _ := w.users.Get(w.ctx, u.ID); got.Language != "en" {
		t.Errorf("Language = %q after a refused value", got.Language)
	}
}

// CA-IDIOMA-04
func TestTheMenuOfCommandsIsRegisteredInBothLanguages(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if err := w.bot.Start(w.ctx); err != nil {
		t.Fatal(err)
	}
	var menus []faketelegram.Sent
	for _, s := range w.fake.Sent() {
		if s.Method == "setMyCommands" {
			menus = append(menus, s)
		}
	}
	if len(menus) != 2 {
		t.Fatalf("%d setMyCommands, want 2", len(menus))
	}
	code := func(s faketelegram.Sent) string { c, _ := s.Params["language_code"].(string); return c }
	if code(menus[0]) != "" || code(menus[1]) != "en" {
		t.Errorf("language codes %q and %q, want the default and en", code(menus[0]), code(menus[1]))
	}
	names := func(s faketelegram.Sent) (out []string) {
		for _, c := range s.Params["commands"].([]any) {
			out = append(out, c.(map[string]any)["command"].(string))
		}
		return out
	}
	if a, b := names(menus[0]), names(menus[1]); len(a) == 0 || strings.Join(a, ",") != strings.Join(b, ",") {
		t.Errorf("the names differ: %v and %v", a, b)
	}
}
