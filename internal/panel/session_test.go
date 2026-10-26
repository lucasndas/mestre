package panel_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
)

// wsURL serves the panel on a real socket and returns the address of /ws/ui.
func (r *rig) wsServer() *httptest.Server {
	r.t.Helper()
	srv := httptest.NewServer(r.panel)
	r.t.Cleanup(srv.Close)
	return srv
}

func (r *rig) dial(srv *httptest.Server, cookie, originValue string) (*websocket.Conn, *http.Response, error) {
	r.t.Helper()
	u, _ := url.Parse(srv.URL)
	h := http.Header{}
	if cookie != "" {
		h.Set("Cookie", panel.CookieName+"="+cookie)
	}
	if originValue != "" {
		h.Set("Origin", originValue)
	}
	return websocket.Dial(r.ctx, "ws://"+u.Host+"/ws/ui", &websocket.DialOptions{HTTPHeader: h})
}

// okOrigin is the Origin a browser sends to the panel at srv: the panel is always HTTPS.
func okOrigin(srv *httptest.Server) string {
	u, _ := url.Parse(srv.URL)
	return "https://" + u.Host
}

// closed waits for the server to close the socket and returns the code.
func closedWith(t *testing.T, c *websocket.Conn) websocket.StatusCode {
	t.Helper()
	_, _, err := c.Read(t.Context())
	return websocket.CloseStatus(err)
}

// CA-LOGIN-02: a session unused for 12 h (fake clock) is a 401 and its socket is
// closed.
func TestAnIdleSessionExpiresAndItsSocketCloses(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	srv := r.wsServer()
	tok := r.ownerSession()
	c, _, err := r.dial(srv, tok, okOrigin(srv))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()

	r.clk.Advance(panel.IdleTimeout - time.Minute)
	if w := r.call("GET", "/api/me", nil, opts{cookie: tok}); w.Code != 200 { // a use: the idle time starts again
		t.Fatalf("before the limit = %d", w.Code)
	}
	r.clk.Advance(panel.IdleTimeout)
	r.wantErr(r.call("GET", "/api/me", nil, opts{cookie: tok}), 401, panel.CodeUnauthenticated)
	if code := closedWith(t, c); code != panel.CloseSessionEnded {
		t.Fatalf("the socket closed with %d", code)
	}
}

func TestASessionLivesSevenDaysAtMostWhateverItsUse(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	tok := r.ownerSession()
	for range 13 { // every 11 h: never idle
		r.clk.Advance(11 * time.Hour)
		if w := r.call("GET", "/api/me", nil, opts{cookie: tok}); w.Code != 200 {
			t.Fatalf("at %v: %d", r.clk.Now(), w.Code)
		}
	}
	r.clk.Advance(panel.MaxAge - 13*11*time.Hour)
	r.wantErr(r.call("GET", "/api/me", nil, opts{cookie: tok}), 401, panel.CodeUnauthenticated)
}

func TestNoSessionIsA401EverywhereButTheLogin(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	for _, path := range []string{"/api/me", "/api/sessions", "/api/admin/sessions", "/api/nada"} {
		r.wantErr(r.call("GET", path, nil, opts{}), 401, panel.CodeUnauthenticated)
	}
	r.wantErr(r.call("GET", "/api/me", nil, opts{cookie: "inventado"}), 401, panel.CodeUnauthenticated)
	r.wantErr(r.call("GET", "/api/nada", nil, opts{cookie: r.ownerSession()}), 404, panel.CodeNotFound)
}

// CA-LOGIN-04: the Owner suspends a user with two sessions: both stop at once, and
// so do their sockets. The same for a change of level and a removal.
func TestSuspendingEndsEverySessionAtOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	srv := r.wsServer()
	u, first := r.member("Ana", "ana", users.Approver)
	second := token(t, r.login("ana", otherPass, opts{}))
	c, _, err := r.dial(srv, second, okOrigin(srv))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	if err := r.users.Suspend(r.ctx, r.owner, u.ID); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{first, second} {
		r.wantErr(r.call("GET", "/api/me", nil, opts{cookie: tok}), 401, panel.CodeUnauthenticated)
	}
	if code := closedWith(t, c); code != panel.CloseSessionEnded {
		t.Fatalf("the socket closed with %d", code)
	}
}

func TestALevelChangeEndsTheSessionsToo(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	u, tok := r.member("Ana", "ana", users.Approver)
	lvl := users.Observer
	if _, err := r.users.Edit(r.ctx, r.owner, u.ID, users.Edit{Level: &lvl}); err != nil {
		t.Fatal(err)
	}
	r.wantErr(r.call("GET", "/api/me", nil, opts{cookie: tok}), 401, panel.CodeUnauthenticated)
	tok = token(t, r.login("ana", otherPass, opts{}))
	if me := decodeInto[panel.Me](t, r.call("GET", "/api/me", nil, opts{cookie: tok})); me.User.Level != "observer" {
		t.Fatalf("level = %s", me.User.Level)
	}
	if err := r.users.Remove(r.ctx, r.owner, u.ID); err != nil {
		t.Fatal(err)
	}
	r.wantErr(r.call("GET", "/api/me", nil, opts{cookie: tok}), 401, panel.CodeUnauthenticated)
}

// CA-LOGIN-06: a change with another Origin, or none, is refused; so is the
// opening of the socket.
func TestAMutatingRequestFromAnotherOriginIsRefused(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	tok := r.ownerSession()
	for _, o := range []opts{
		{cookie: tok, origin: "https://evil.example"},
		{cookie: tok, origin: "http://" + host},
		{cookie: tok, noOrigin: true},
	} {
		r.wantErr(r.call("POST", "/api/test/change", nil, o), 403, panel.CodeBadOrigin)
	}
	r.wantErr(r.login(ownerLogin, ownerPass, opts{origin: "https://evil.example"}), 403, panel.CodeBadOrigin)
	if w := r.call("POST", "/api/test/change", nil, opts{cookie: tok}); w.Code != 200 {
		t.Fatalf("own origin = %d", w.Code)
	}
	// a read needs no Origin
	if w := r.call("GET", "/api/me", nil, opts{cookie: tok, noOrigin: true}); w.Code != 200 {
		t.Fatalf("read = %d", w.Code)
	}
	srv := r.wsServer()
	if c, _, err := r.dial(srv, tok, "https://evil.example"); err == nil {
		_ = c.CloseNow()
		t.Fatal("the socket opened for another origin")
	}
	if c, _, err := r.dial(srv, tok, ""); err == nil {
		_ = c.CloseNow()
		t.Fatal("the socket opened with no origin")
	}
	if c, _, err := r.dial(srv, "", okOrigin(srv)); err == nil {
		_ = c.CloseNow()
		t.Fatal("the socket opened with no session")
	}
}

func TestSessionsAreListedAndEnded(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	ownerTok := r.ownerSession()
	ana, anaTok := r.member("Ana", "ana", users.Approver)
	_, bruTok := r.member("Bruno", "bruno", users.Administrator)

	own := decodeInto[panel.SessionList](t, r.call("GET", "/api/sessions", nil, opts{cookie: anaTok}))
	if len(own.Sessions) != 1 || !own.Sessions[0].Current || own.Sessions[0].UserID != ana.ID || own.Sessions[0].Device != "rig-browser" {
		t.Fatalf("own = %+v", own)
	}
	r.wantErr(r.call("GET", "/api/admin/sessions", nil, opts{cookie: anaTok}), 403, panel.CodeForbiddenLevel)
	all := decodeInto[panel.SessionList](t, r.call("GET", "/api/admin/sessions", nil, opts{cookie: ownerTok}))
	if len(all.Sessions) != 3 {
		t.Fatalf("all = %+v", all)
	}
	// Bruno does not see, and so does not end, the session of Ana
	r.wantErr(r.call("DELETE", "/api/sessions/"+own.Sessions[0].ID, nil, opts{cookie: bruTok}), 404, panel.CodeNotFound)
	if w := r.call("GET", "/api/me", nil, opts{cookie: anaTok}); w.Code != 200 {
		t.Fatal("the session of Ana ended")
	}
	// the Owner ends it
	if w := r.call("DELETE", "/api/sessions/"+own.Sessions[0].ID, nil, opts{cookie: ownerTok}); w.Code != 204 {
		t.Fatalf("end = %d %s", w.Code, w.Body)
	}
	r.wantErr(r.call("GET", "/api/me", nil, opts{cookie: anaTok}), 401, panel.CodeUnauthenticated)
	// and a user ends his own, with the cookie cleared
	w := r.call("DELETE", "/api/sessions/"+decodeInto[panel.SessionList](t, r.call("GET", "/api/sessions", nil, opts{cookie: bruTok})).Sessions[0].ID, nil, opts{cookie: bruTok})
	if w.Code != 204 || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].MaxAge >= 0 {
		t.Fatalf("own end = %d %v", w.Code, w.Result().Cookies())
	}
	if w := r.call("POST", "/api/logout", nil, opts{cookie: ownerTok}); w.Code != 204 {
		t.Fatalf("logout = %d", w.Code)
	}
	r.wantErr(r.call("GET", "/api/me", nil, opts{cookie: ownerTok}), 401, panel.CodeUnauthenticated)
}

func TestTheSweepEndsTheSessionsThatAreOver(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	srv := r.wsServer()
	tok := r.ownerSession()
	c, _, err := r.dial(srv, tok, okOrigin(srv))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	ctx, cancel := context.WithCancel(r.ctx)
	done := make(chan struct{})
	go func() { r.panel.Run(ctx); close(done) }()
	r.clk.BlockUntil(1)
	r.clk.Advance(panel.IdleTimeout + time.Minute) // the ticker fires: the sweep ends the session
	if code := closedWith(t, c); code != panel.CloseSessionEnded {
		t.Fatalf("the socket closed with %d", code)
	}
	cancel()
	<-done
}
