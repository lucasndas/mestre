package panel_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

const (
	host        = "mestre.test"
	origin      = "https://" + host
	ownerLogin  = "lucas"
	ownerPass   = "uma senha bem longa"
	ownerPIN    = "123456"
	otherPass   = "outra senha bem longa"
	defaultAddr = "192.0.2.1:4000"
)

func init() {
	// a route of each kind, for the tests of the middleware (the areas of the
	// later units register theirs the same way)
	panel.RegisterArea("rigtest", func(r *panel.Router) {
		ok := func(w http.ResponseWriter, _ *http.Request) {
			panel.WriteJSON(w, http.StatusOK, map[string]bool{"done": true})
		}
		r.Auth("POST /api/test/change", ok)
		r.Require("GET /api/test/admin", ok, users.GuildSettings)
		r.Sensitive("POST /api/test/vault", "vault.put", users.BasesVaultPolicy, ok)
	})
}

type alerts struct {
	mu    sync.Mutex
	owner []string
}

func (a *alerts) Unauthorized(context.Context, int64, string) {}
func (a *alerts) Tell(context.Context, string, string)        {}
func (a *alerts) OwnerAlert(_ context.Context, text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.owner = append(a.owner, text)
}
func (a *alerts) sent() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.owner...)
}

type rig struct {
	t      *testing.T
	ctx    context.Context
	clk    *clock.Fake
	db     *store.DB
	users  *users.Service
	panel  *panel.Panel
	al     *alerts
	assets fstest.MapFS
	owner  users.User
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := newBare(t)
	code, err := r.users.NewSetupCode(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	r.owner, err = r.users.SetupOwner(r.ctx, code, users.SetupInput{Name: "Lucas", Login: ownerLogin, Password: ownerPass, PIN: ownerPIN})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

type opts struct {
	cookie, pin, origin, addr string
	noOrigin                  bool
	// acceptLanguage is the Accept-Language header of the request.
	acceptLanguage string
}

func (r *rig) request(method, path string, body any, o opts) *http.Request {
	r.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			r.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, "https://"+host+path, rd)
	req.RemoteAddr = defaultAddr
	if o.addr != "" {
		req.RemoteAddr = o.addr
	}
	switch {
	case o.noOrigin:
	case o.origin != "":
		req.Header.Set("Origin", o.origin)
	default:
		req.Header.Set("Origin", origin)
	}
	if o.cookie != "" {
		req.AddCookie(&http.Cookie{Name: panel.CookieName, Value: o.cookie})
	}
	if o.pin != "" {
		req.Header.Set(panel.PINHeader, o.pin)
	}
	if o.acceptLanguage != "" {
		req.Header.Set("Accept-Language", o.acceptLanguage)
	}
	req.Header.Set("User-Agent", "rig-browser")
	return req
}

func (r *rig) call(method, path string, body any, o opts) *httptest.ResponseRecorder {
	r.t.Helper()
	w := httptest.NewRecorder()
	r.panel.ServeHTTP(w, r.request(method, path, body, o))
	return w
}

// token reads the cookie a response sets.
func token(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == panel.CookieName && c.Value != "" {
			return c.Value
		}
	}
	t.Fatalf("no session cookie in the answer (status %d): %s", w.Code, w.Body)
	return ""
}

func (r *rig) login(login, password string, o opts) *httptest.ResponseRecorder {
	return r.call("POST", "/api/login", panel.LoginRequest{Login: login, Password: password}, o)
}

// ownerSession logs the Owner in and returns the token.
func (r *rig) ownerSession() string {
	r.t.Helper()
	w := r.login(ownerLogin, ownerPass, opts{})
	if w.Code != 200 {
		r.t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	return token(r.t, w)
}

// member makes a user with a password and a PIN, by the first access.
func (r *rig) member(name, login string, level users.Level) (users.User, string) {
	r.t.Helper()
	u, err := r.users.Create(r.ctx, r.owner, users.NewUser{Name: name, Level: level, Workspaces: []string{"loja"}})
	if err != nil {
		r.t.Fatal(err)
	}
	c, err := r.users.NewAccessCode(r.ctx, r.owner, u.ID, users.AccessFirst)
	if err != nil {
		r.t.Fatal(err)
	}
	w := r.call("POST", "/api/first-access", panel.FirstAccessRequest{Code: c.Code, Login: login, Password: otherPass, PIN: "654321"}, opts{})
	if w.Code != 200 {
		r.t.Fatalf("first access: %d %s", w.Code, w.Body)
	}
	got, err := r.users.Get(r.ctx, u.ID)
	if err != nil {
		r.t.Fatal(err)
	}
	return got, token(r.t, w)
}

func decodeInto[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("body %q: %v", w.Body, err)
	}
	return v
}

// errCode is the code of the error an answer carries ("" when it is no error).
func errCode(w *httptest.ResponseRecorder) panel.Code {
	var b panel.ErrorBody
	if json.Unmarshal(w.Body.Bytes(), &b) != nil {
		return ""
	}
	return b.Error.Code
}

func (r *rig) wantErr(w *httptest.ResponseRecorder, status int, code panel.Code) {
	r.t.Helper()
	if w.Code != status || errCode(w) != code {
		r.t.Fatalf("answer = %d %s, want %d %s", w.Code, w.Body, status, code)
	}
}

// audited counts the lines of the audit with the action.
func (r *rig) audited(action string) int {
	r.t.Helper()
	var n int
	if err := r.db.Reader().QueryRowContext(r.ctx, `SELECT COUNT(*) FROM audit_log WHERE action = ?`, action).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// newBare is a rig over a Guild with no user at all, as a new Guild is.
func newBare(t *testing.T) *rig {
	t.Helper()
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC))
	db, err := guildtpl.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cheap := users.PINCost{MemoryKiB: 8, Time: 1, Threads: 1}
	r := &rig{t: t, ctx: ctx, clk: clk, db: db, al: &alerts{}, assets: fstest.MapFS{
		"index.html":           {Data: []byte("<html>app</html>")},
		"assets/app-1a2b3c.js": {Data: []byte("console.log(1)")},
	}}
	r.users, err = users.New(users.Options{DB: db, Clock: clk, Alerts: r.al, PIN: cheap, Password: cheap})
	if err != nil {
		t.Fatal(err)
	}
	r.panel, err = panel.New(panel.Options{DB: db, Clock: clk, Users: r.users, Alerts: r.al, Assets: r.assets, Machine: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.panel.Close)
	return r
}
