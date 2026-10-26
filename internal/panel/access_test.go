package panel_test

import (
	"testing"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
)

// CA-LOGIN-03: an action that asks for the PIN, without it in the request, is
// refused with "PIN obrigatório" and nothing changes.
func TestASensitiveActionWithoutPINIsRefused(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	tok := r.ownerSession()
	w := r.call("POST", "/api/test/vault", nil, opts{cookie: tok})
	r.wantErr(w, 403, panel.CodePINRequired)
	if !contains(w.Body.String(), "PIN obrigatório") {
		t.Fatalf("message = %s", w.Body)
	}
	// the real thing: an access code is not made without the PIN
	ana, err := r.users.Create(r.ctx, r.owner, users.NewUser{Name: "Ana", Level: users.Observer})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/users/" + ana.ID + "/access-code"
	r.wantErr(r.call("POST", path, panel.AccessCodeRequest{Kind: users.AccessFirst}, opts{cookie: tok}), 403, panel.CodePINRequired)
	var n int
	_ = r.db.Reader().QueryRowContext(r.ctx, `SELECT COUNT(*) FROM panel_access_codes`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d codes were made without the PIN", n)
	}
	w = r.call("POST", path, panel.AccessCodeRequest{Kind: users.AccessFirst}, opts{cookie: tok, pin: ownerPIN})
	if w.Code != 200 {
		t.Fatalf("with the PIN = %d %s", w.Code, w.Body)
	}
	if c := decodeInto[panel.AccessCodeResponse](t, w); len(c.Code) != users.AccessCodeLength || c.Kind != users.AccessFirst {
		t.Fatalf("code = %+v", c)
	}
}

// A PIN checked is spent by its action: it is worth no other, and no later one.
func TestAPINIsWorthOneRequestOnly(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	tok := r.ownerSession()
	if w := r.call("POST", "/api/test/vault", nil, opts{cookie: tok, pin: ownerPIN}); w.Code != 200 {
		t.Fatalf("with the PIN = %d %s", w.Code, w.Body)
	}
	r.wantErr(r.call("POST", "/api/test/vault", nil, opts{cookie: tok}), 403, panel.CodePINRequired)
	// the request of PIN of the first action is spent and open to no action
	var open int
	_ = r.db.Reader().QueryRowContext(r.ctx, `SELECT COUNT(*) FROM pin_requests WHERE state IN ('open', 'done')`).Scan(&open)
	if open != 0 {
		t.Fatalf("%d requests of PIN are still open", open)
	}
	var action string
	_ = r.db.Reader().QueryRowContext(r.ctx, `SELECT action FROM pin_requests`).Scan(&action)
	if action != "vault.put" {
		t.Fatalf("the request was for %q", action)
	}
	r.wantErr(r.call("POST", "/api/test/vault", nil, opts{cookie: tok, pin: "999999"}), 403, panel.CodePINInvalid)
}

func TestWrongPINsLockThePINAndEndTheSession(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	tok := r.ownerSession()
	for range users.PINLockErrors - 1 {
		r.wantErr(r.call("POST", "/api/test/vault", nil, opts{cookie: tok, pin: "000000"}), 403, panel.CodePINInvalid)
	}
	r.wantErr(r.call("POST", "/api/test/vault", nil, opts{cookie: tok, pin: "000000"}), 403, panel.CodePINLocked)
	r.wantErr(r.call("GET", "/api/me", nil, opts{cookie: tok}), 401, panel.CodeUnauthenticated) // the session ended
	if len(r.al.sent()) != 1 {
		t.Fatalf("the Owner was told %q", r.al.sent())
	}
	// locked, the right PIN is not looked at either
	tok = r.ownerSession()
	r.wantErr(r.call("POST", "/api/test/vault", nil, opts{cookie: tok, pin: ownerPIN}), 403, panel.CodePINLocked)
	if w := r.call("GET", "/api/me", nil, opts{cookie: tok}); w.Code != 200 {
		t.Fatal("a refusal for a PIN already locked ended the session")
	}
}

// The levels are the table's, read at every request.
func TestTheServerChecksTheLevelOfEveryRequest(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	_, adm := r.member("Bruno", "bruno", users.Administrator)
	w := r.call("GET", "/api/test/admin", nil, opts{cookie: adm})
	r.wantErr(w, 403, panel.CodeForbiddenLevel)
	if b := decodeInto[panel.ErrorBody](t, w); b.Error.Params["need"] != "owner" || b.Error.Params["have"] != "administrator" {
		t.Fatalf("params = %v", b.Error.Params)
	}
	// the level is checked before the PIN
	r.wantErr(r.call("POST", "/api/test/vault", nil, opts{cookie: adm, pin: "654321"}), 403, panel.CodeForbiddenLevel)
	if w := r.call("GET", "/api/test/admin", nil, opts{cookie: r.ownerSession()}); w.Code != 200 {
		t.Fatalf("owner = %d", w.Code)
	}
	// the Observer can read, and that is all the table gives
	_, obs := r.member("Caio", "caio", users.Observer)
	me := decodeInto[panel.Me](t, r.call("GET", "/api/me", nil, opts{cookie: obs}))
	if !contains(joinCaps(me.Capabilities), "status") || contains(joinCaps(me.Capabilities), "pause") {
		t.Fatalf("capabilities = %v", me.Capabilities)
	}
	// only the Owner makes access codes
	ana, _ := r.users.Create(r.ctx, r.owner, users.NewUser{Name: "Ana", Level: users.Observer})
	r.wantErr(r.call("POST", "/api/users/"+ana.ID+"/access-code", panel.AccessCodeRequest{Kind: "first"}, opts{cookie: adm, pin: "654321"}), 403, panel.CodeForbiddenLevel)
}

func joinCaps(c []string) string {
	s := " "
	for _, x := range c {
		s += x + " "
	}
	return s
}
