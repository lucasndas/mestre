package panel_test

import (
	"testing"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
)

func TestTheLanguageIsPerUserAndStartsEmpty(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	tok := r.ownerSession()
	me := decodeInto[panel.Me](t, r.call("GET", "/api/me", nil, opts{cookie: tok}))
	if me.User.Language != "" || me.Timezone == "" {
		t.Fatalf("me = %+v", me)
	}
	w := r.call("PUT", "/api/me/language", panel.LanguageRequest{Language: "en"}, opts{cookie: tok})
	if w.Code != 200 || decodeInto[panel.Me](t, w).User.Language != "en" {
		t.Fatalf("set = %d %s", w.Code, w.Body)
	}
	if got := decodeInto[panel.Me](t, r.call("GET", "/api/me", nil, opts{cookie: tok})); got.User.Language != "en" {
		t.Fatalf("not kept: %+v", got.User)
	}
	r.wantErr(r.call("PUT", "/api/me/language", panel.LanguageRequest{Language: "fr"}, opts{cookie: tok}), 400, panel.CodeBadRequest)
	// another user keeps his own
	_, other := r.member("Ana", "ana", users.Approver)
	if got := decodeInto[panel.Me](t, r.call("GET", "/api/me", nil, opts{cookie: other})); got.User.Language != "" {
		t.Fatalf("language leaked to another user: %+v", got.User)
	}
}

// CA-IDIOMA-03
// What the Telegram writes (the same users.Service.SetLanguage that the button /idioma calls) is
// what the panel opens with in the next session: a new login of that user gets "en" in /api/me,
// and a language out of the list written by the other channel leaves it alone.
func TestThePanelOpensInTheLanguageWrittenByTheTelegramInTheNextSession(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	u, first := r.member("Ana", "ana", users.Approver)
	if got := decodeInto[panel.Me](t, r.call("GET", "/api/me", nil, opts{cookie: first})); got.User.Language != "" {
		t.Fatalf("a user with no choice has %q", got.User.Language)
	}
	if err := r.users.SetLanguage(r.ctx, u.ID, "en"); err != nil { // the button of the bot
		t.Fatal(err)
	}
	if err := r.users.SetLanguage(r.ctx, u.ID, "de"); err == nil {
		t.Fatal("a language out of the list was accepted")
	}
	w := r.login("ana", otherPass, opts{}) // the next session
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	if got := decodeInto[panel.Me](t, r.call("GET", "/api/me", nil, opts{cookie: token(t, w)})); got.User.Language != "en" {
		t.Fatalf("the panel opens in %q, want en", got.User.Language)
	}
}
