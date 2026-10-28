package panel_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/panel"
)

// gateRig is a rig whose panel asks a gate of the handover that the test opens and closes.
type gateRig struct {
	*rig
	mu       sync.Mutex
	open     bool
	entered  int
	released int
}

func newGateRig(t *testing.T) *gateRig {
	t.Helper()
	g := &gateRig{rig: newRig(t), open: true}
	p, err := panel.New(panel.Options{DB: g.db, Clock: g.clk, Users: g.users, Alerts: g.al, Assets: g.assets, Machine: "m1",
		Hold: func() (func(), bool) {
			g.mu.Lock()
			defer g.mu.Unlock()
			if !g.open {
				return nil, false
			}
			g.entered++
			return func() { g.mu.Lock(); g.released++; g.mu.Unlock() }, true
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	g.panel = p
	return g
}

func (g *gateRig) counts() (entered, released int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.entered, g.released
}

func (g *gateRig) setOpen(v bool) {
	g.mu.Lock()
	g.open = v
	g.mu.Unlock()
}

// spy is a body that tells how many times the gate had been entered when it was read.
type spy struct {
	io.Reader
	g               *gateRig
	enteredWhenRead *int
}

func (s spy) Read(p []byte) (int, error) {
	e, _ := s.g.counts()
	*s.enteredWhenRead = e
	return s.Reader.Read(p)
}

// Part of CA-CIDADELA-19: the gate of the handover is held only after the user is authenticated
// and the body is read: a body that is still being sent, and a request with no session, never
// hold it.
func TestTheGateIsHeldOnlyAfterAuthenticationAndTheBodyIsRead(t *testing.T) {
	t.Parallel()
	g := newGateRig(t)
	sess := g.ownerSession()
	before, _ := g.counts()

	seen := -1
	req := g.request("PUT", "/api/me/language", nil, opts{cookie: sess})
	req.Body = io.NopCloser(spy{Reader: strings.NewReader(`{"language":"pt-BR"}`), g: g, enteredWhenRead: &seen})
	req.ContentLength = -1
	w := httptest.NewRecorder()
	g.panel.ServeHTTP(w, req)
	if seen != before {
		t.Errorf("the gate had been entered %d times when the body was read, want %d (still none)", seen, before)
	}
	if e, r := g.counts(); e != before+1 || r != before+1 {
		t.Errorf("entered %d, released %d; want the one request held once and released", e, r)
	}

	// no session: refused with 401 and the gate never entered
	w = g.call("PUT", "/api/me/language", map[string]string{"language": "pt-BR"}, opts{})
	g.wantErr(w, http.StatusUnauthorized, panel.CodeUnauthenticated)
	if e, _ := g.counts(); e != before+1 {
		t.Errorf("a request with no session entered the gate (%d)", e)
	}
}

// Part of CA-CIDADELA-19: once the Citadel drains, a request that changes something, the touch
// of a session on a read and an audit line are refused with citadel_in_failover, and write nothing.
func TestWritesDuringAReadAreRefusedWhileTheCitadelDrains(t *testing.T) {
	t.Parallel()
	g := newGateRig(t)
	sess := g.ownerSession()
	g.setOpen(false)

	g.wantErr(g.call("PUT", "/api/me/language", map[string]string{"language": "en"}, opts{cookie: sess}), http.StatusServiceUnavailable, panel.CodeCitadelInFailover)

	// a read that does not touch the session goes on
	if w := g.call("GET", "/api/me", nil, opts{cookie: sess}); w.Code != http.StatusOK {
		t.Fatalf("GET with the session fresh = %d %s; want it served", w.Code, w.Body)
	}
	// a read that would write the touch of the session is refused, and the session is not touched
	var before int64
	if err := g.db.Reader().QueryRowContext(g.ctx, `SELECT MAX(last_used_at) FROM panel_sessions`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	g.clk.Advance(2 * time.Minute)
	g.wantErr(g.call("GET", "/api/me", nil, opts{cookie: sess}), http.StatusServiceUnavailable, panel.CodeCitadelInFailover)
	var after int64
	if err := g.db.Reader().QueryRowContext(g.ctx, `SELECT MAX(last_used_at) FROM panel_sessions`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("the session was touched while the Citadel drains")
	}

	// the audit line of a refused origin is not written either
	n := g.audited("panel.origin_refused")
	g.wantErr(g.call("POST", "/api/logout", nil, opts{cookie: sess, origin: "https://evil.example"}), http.StatusForbidden, panel.CodeBadOrigin)
	if g.audited("panel.origin_refused") != n {
		t.Error("an audit line was written while the Citadel drains")
	}

	// the gate opens again (this Citadel keeps the command): all of it works
	g.setOpen(true)
	if w := g.call("GET", "/api/me", nil, opts{cookie: sess}); w.Code != http.StatusOK {
		t.Fatalf("GET after the gate opened = %d %s", w.Code, w.Body)
	}
	g.wantErr(g.call("POST", "/api/logout", nil, opts{cookie: sess, origin: "https://evil.example"}), http.StatusForbidden, panel.CodeBadOrigin)
	if g.audited("panel.origin_refused") != n+1 {
		t.Error("the audit line was not written after the gate opened")
	}
}

// Part of CA-CIDADELA-19: a request that holds the gate does not enter it again for its own
// audit and session writes (a second entry would be refused by a gate that began to drain).
func TestARequestThatHoldsTheGateEntersItOnce(t *testing.T) {
	t.Parallel()
	g := newGateRig(t)
	before, _ := g.counts()
	if w := g.login(ownerLogin, ownerPass, opts{}); w.Code != http.StatusOK {
		t.Fatalf("login = %d %s", w.Code, w.Body)
	}
	if e, r := g.counts(); e != before+1 || r != before+1 {
		t.Errorf("login entered the gate %d times and released %d; want once", e-before, r-before)
	}
}
