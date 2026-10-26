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
