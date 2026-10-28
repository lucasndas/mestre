package panel_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
)

// Part of CA-LOGIN-01 (the full part runs the wizard on a real Citadel): the wizard asks the code of setup, makes the Owner and
// opens his session; afterwards its route answers 404.
func TestSetupWizardMakesTheOwnerAndThenIsGone(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	// the rig already ran the wizard: a Guild with no user is another one
	ctx := r.ctx
	if open, _ := r.users.SetupOpen(ctx); open {
		t.Fatal("the setup is open with the Owner there")
	}
	w := r.call("POST", "/api/setup", panel.SetupRequest{Code: "AAAAAAAAAAAA", Name: "X", Login: "x1234", Password: ownerPass, PIN: ownerPIN}, opts{})
	r.wantErr(w, 404, panel.CodeNotFound)
	if st := decodeInto[panel.Status](t, r.call("GET", "/api/status", nil, opts{})); st.SetupOpen {
		t.Fatal("status says the setup is open")
	}
}

func TestSetupOpensTheSessionOfTheNewOwner(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	db2 := newBare(t) // a Guild with no user
	code, err := db2.users.NewSetupCode(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st := decodeInto[panel.Status](t, db2.call("GET", "/api/status", nil, opts{})); !st.SetupOpen {
		t.Fatal("status says the setup is closed on a Guild with no user")
	}
	w := db2.call("POST", "/api/setup", panel.SetupRequest{Code: code, Name: "Lucas", Login: "Lucas", Password: ownerPass, PIN: ownerPIN}, opts{})
	if w.Code != 200 {
		t.Fatalf("setup = %d %s", w.Code, w.Body)
	}
	me := decodeInto[panel.Me](t, w)
	if me.User.Level != string(users.Owner) || me.User.Login != "lucas" || !me.User.HasPIN {
		t.Fatalf("me = %+v", me)
	}
	var c *http.Cookie
	for _, k := range w.Result().Cookies() {
		if k.Name == panel.CookieName {
			c = k
		}
	}
	if c == nil || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" {
		t.Fatalf("cookie = %+v", c)
	}
	if got := db2.call("GET", "/api/me", nil, opts{cookie: c.Value}); got.Code != 200 {
		t.Fatalf("me with the cookie = %d", got.Code)
	}
	// used, the code is not worth a second Owner, and the route is gone
	w = db2.call("POST", "/api/setup", panel.SetupRequest{Code: code, Name: "Outro", Login: "outro", Password: ownerPass, PIN: ownerPIN}, opts{})
	r.wantErr(w, 404, panel.CodeNotFound)
	// only the hash of the token and the hash of the code are kept
	var raw int
	_ = db2.db.Reader().QueryRowContext(r.ctx, `SELECT COUNT(*) FROM panel_sessions WHERE token_hash = ?`, c.Value).Scan(&raw)
	if raw != 0 {
		t.Fatal("the token is kept as it is")
	}
	if db2.audited("panel.setup") != 1 {
		t.Fatal("the setup is not in the audit")
	}
}

func TestSetupWrongCodesAreLimitedPerIP(t *testing.T) {
	t.Parallel()
	r := newBare(t)
	code, _ := r.users.NewSetupCode(r.ctx)
	bad := panel.SetupRequest{Code: "ZZZZZZZZZZZZ", Name: "L", Login: "lucas", Password: ownerPass, PIN: ownerPIN}
	for range panel.SetupErrors {
		r.wantErr(r.call("POST", "/api/setup", bad, opts{}), 403, panel.CodeInvalidCode)
	}
	// the sixth is refused without looking at the code, even the right one
	good := bad
	good.Code = code
	w := r.call("POST", "/api/setup", good, opts{})
	r.wantErr(w, 429, panel.CodeRateLimited)
	// another IP is not limited by it
	if w := r.call("POST", "/api/setup", good, opts{addr: "198.51.100.7:1"}); w.Code != 200 {
		t.Fatalf("other IP = %d %s", w.Code, w.Body)
	}
}

func TestANewSetupCodeVoidsTheOldOne(t *testing.T) {
	t.Parallel()
	r := newBare(t)
	first, _ := r.users.NewSetupCode(r.ctx)
	second, _ := r.users.NewSetupCode(r.ctx)
	in := panel.SetupRequest{Code: first, Name: "L", Login: "lucas", Password: ownerPass, PIN: ownerPIN}
	r.wantErr(r.call("POST", "/api/setup", in, opts{}), 403, panel.CodeInvalidCode)
	in.Code = strings.ToLower(second) // the code is read in any case
	if w := r.call("POST", "/api/setup", in, opts{}); w.Code != 200 {
		t.Fatalf("setup = %d %s", w.Code, w.Body)
	}
	if _, err := r.users.NewSetupCode(r.ctx); err == nil {
		t.Fatal("a code of setup was made for a Guild that has an Owner")
	}
}

func TestPasswordAndPINRules(t *testing.T) {
	t.Parallel()
	r := newBare(t)
	code, _ := r.users.NewSetupCode(r.ctx)
	in := panel.SetupRequest{Code: code, Name: "L", Login: "lucas", Password: "curta", PIN: ownerPIN}
	r.wantErr(r.call("POST", "/api/setup", in, opts{}), 422, panel.CodePasswordTooShort)
	in.Password = ownerPass
	in.PIN = "12ab"
	r.wantErr(r.call("POST", "/api/setup", in, opts{}), 422, panel.CodeInvalidPIN)
	in.PIN, in.Login = ownerPIN, "a b"
	r.wantErr(r.call("POST", "/api/setup", in, opts{}), 422, panel.CodeInvalidLogin)
	// invalid input does not spend the code
	in.Login = "lucas"
	if w := r.call("POST", "/api/setup", in, opts{}); w.Code != 200 {
		t.Fatalf("setup = %d %s", w.Code, w.Body)
	}
}

// CA-LOGIN-05: 5 wrong passwords in 15 minutes block the login; the sixth is
// refused without checking the password and the Owner is told.
func TestFiveWrongPasswordsBlockTheLogin(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	u, _ := r.member("Ana", "ana", users.Approver)
	for range panel.LoginErrors {
		r.wantErr(r.login("ana", "senha errada demais", opts{}), 401, panel.CodeInvalidCredentials)
		r.clk.Advance(time.Minute)
	}
	if got := r.al.sent(); len(got) != 1 || !contains(got[0], "Ana") {
		t.Fatalf("the Owner was told %q", got)
	}
	w := r.login("ana", otherPass, opts{}) // the right password
	r.wantErr(w, 429, panel.CodeLoginBlocked)
	if r.audited("panel.login.blocked") < 2 {
		t.Fatal("the block is not in the audit")
	}
	r.clk.Advance(panel.LoginBlockFor)
	if w := r.login("ana", otherPass, opts{}); w.Code != 200 {
		t.Fatalf("after the block: %d %s", w.Code, w.Body)
	}
	_ = u
}

func TestWrongLoginAndWrongPasswordSayTheSame(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.login("ninguem", ownerPass, opts{})
	b := r.login(ownerLogin, "senha errada demais", opts{})
	if a.Code != b.Code || a.Body.String() != b.Body.String() {
		t.Fatalf("the answers differ:\n%s\n%s", a.Body, b.Body)
	}
	if !contains(a.Body.String(), "Usuário ou senha inválidos") {
		t.Fatalf("the message is %s", a.Body)
	}
	// the login typed (maybe a password) is not written in the audit when it is nobody's
	var n int
	_ = r.db.Reader().QueryRowContext(r.ctx, `SELECT COUNT(*) FROM audit_log WHERE detail LIKE '%ninguem%'`).Scan(&n)
	if n != 0 {
		t.Fatal("a login that is nobody's was written in the audit")
	}
}

func TestASuspendedUserGetsTheSameMessageAndIsAudited(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	u, _ := r.member("Ana", "ana", users.Approver)
	if err := r.users.Suspend(r.ctx, r.owner, u.ID); err != nil {
		t.Fatal(err)
	}
	w := r.login("ana", otherPass, opts{})
	r.wantErr(w, 401, panel.CodeInvalidCredentials)
	if !contains(w.Body.String(), "Usuário ou senha inválidos") || r.audited("panel.login.suspended") != 1 {
		t.Fatalf("answer %s, audited %d", w.Body, r.audited("panel.login.suspended"))
	}
}

// CA-LOGIN-07: a code of first access of more than 24 h is refused; it is single
// use; and a reset takes the old password and the sessions away.
func TestFirstAccessCodeExpiresAndIsSingleUse(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	u, err := r.users.Create(r.ctx, r.owner, users.NewUser{Name: "Bia", Level: users.Observer})
	if err != nil {
		t.Fatal(err)
	}
	old, _ := r.users.NewAccessCode(r.ctx, r.owner, u.ID, users.AccessFirst)
	r.clk.Advance(users.AccessCodeTTL + time.Second)
	in := panel.FirstAccessRequest{Code: old.Code, Login: "bia", Password: otherPass, PIN: "654321"}
	r.wantErr(r.call("POST", "/api/first-access", in, opts{}), 403, panel.CodeInvalidCode)

	fresh, _ := r.users.NewAccessCode(r.ctx, r.owner, u.ID, users.AccessFirst)
	r.clk.Advance(users.AccessCodeTTL - time.Minute)
	in.Code = fresh.Code
	if w := r.call("POST", "/api/first-access", in, opts{}); w.Code != 200 {
		t.Fatalf("first access = %d %s", w.Code, w.Body)
	}
	r.wantErr(r.call("POST", "/api/first-access", in, opts{}), 403, panel.CodeInvalidCode)
}

func TestResetCodeTakesTheOldPasswordAndTheSessionsAway(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	u, tok := r.member("Ana", "ana", users.Approver)
	c, err := r.users.NewAccessCode(r.ctx, r.owner, u.ID, users.AccessReset)
	if err != nil {
		t.Fatal(err)
	}
	r.wantErr(r.call("GET", "/api/me", nil, opts{cookie: tok}), 401, panel.CodeUnauthenticated)
	r.wantErr(r.login("ana", otherPass, opts{}), 401, panel.CodeInvalidCredentials)
	in := panel.FirstAccessRequest{Code: c.Code, Login: "outro", Password: "nova senha bem longa"}
	r.wantErr(r.call("POST", "/api/first-access", in, opts{}), 403, panel.CodeInvalidCode) // the login is the one he has
	in.Login = "ana"
	if w := r.call("POST", "/api/first-access", in, opts{}); w.Code != 200 {
		t.Fatalf("reset = %d %s", w.Code, w.Body)
	}
	if w := r.login("ana", "nova senha bem longa", opts{}); w.Code != 200 {
		t.Fatalf("login with the new password = %d", w.Code)
	}
	// the PIN he had stays
	got, _ := r.users.Get(r.ctx, u.ID)
	if !got.HasPIN {
		t.Fatal("the reset took the PIN")
	}
}

func TestChangingThePasswordAsksTheCurrentOneAndEndsTheOtherSessions(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a, b := r.ownerSession(), r.ownerSession()
	r.wantErr(r.call("POST", "/api/password", panel.PasswordRequest{Current: "errada errada errada", New: "uma senha nova longa"}, opts{cookie: a}), 403, panel.CodePasswordWrong)
	r.wantErr(r.call("POST", "/api/password", panel.PasswordRequest{Current: ownerPass, New: "curta"}, opts{cookie: a}), 422, panel.CodePasswordTooShort)
	if w := r.call("POST", "/api/password", panel.PasswordRequest{Current: ownerPass, New: "uma senha nova longa"}, opts{cookie: a}); w.Code != 204 {
		t.Fatalf("change = %d %s", w.Code, w.Body)
	}
	if w := r.call("GET", "/api/me", nil, opts{cookie: a}); w.Code != 200 {
		t.Fatalf("the session that changed it = %d", w.Code)
	}
	r.wantErr(r.call("GET", "/api/me", nil, opts{cookie: b}), 401, panel.CodeUnauthenticated)
	r.wantErr(r.login(ownerLogin, ownerPass, opts{}), 401, panel.CodeInvalidCredentials)
	if w := r.login(ownerLogin, "uma senha nova longa", opts{}); w.Code != 200 {
		t.Fatalf("login with the new = %d", w.Code)
	}
}

// Nothing the audit keeps holds a password, a PIN or a code.
func TestTheAuditNeverHoldsASecret(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	tok := r.ownerSession()
	r.login(ownerLogin, "uma-senha-errada-qualquer", opts{})
	r.call("POST", "/api/test/vault", nil, opts{cookie: tok, pin: ownerPIN})
	r.call("POST", "/api/test/vault", nil, opts{cookie: tok, pin: "999999"})
	r.call("POST", "/api/test/vault", nil, opts{cookie: tok})
	r.call("POST", "/api/logout", nil, opts{cookie: tok})
	rows, err := r.db.Reader().QueryContext(r.ctx, `SELECT action, detail FROM audit_log`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var action, detail string
		if err := rows.Scan(&action, &detail); err != nil {
			t.Fatal(err)
		}
		seen[action] = true
		for _, secret := range []string{ownerPass, ownerPIN, "uma-senha-errada-qualquer", "999999", tok} {
			if strings.Contains(detail, secret) {
				t.Errorf("%s holds a secret: %s", action, detail)
			}
		}
	}
	for _, a := range []string{"panel.login", "panel.login.failed", "panel.pin.ok", "panel.pin.failed", "panel.pin.required", "panel.logout"} {
		if !seen[a] {
			t.Errorf("%s is not in the audit", a)
		}
	}
}
