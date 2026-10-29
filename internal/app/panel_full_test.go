//go:build full

package app_test

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/app"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/transport"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// panelReply is an answer of the panel of an instance.
type panelReply struct {
	status   int
	location string
	body     []byte
	cookies  []*http.Cookie
}

// panelDo calls the panel of an instance over HTTPS, as a browser on its address
// would (the certificate is not what these tests are about: transport proves it).
func panelDo(t *testing.T, p *proc, method, path string, body any, cookie string) panelReply {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	base := "https://" + p.addr()
	req, err := http.NewRequestWithContext(t.Context(), method, base+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", base)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: panel.CookieName, Value: cookie})
	}
	c := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableKeepAlives: true},
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return panelReply{status: res.StatusCode, location: res.Header.Get("Location"), body: b, cookies: res.Cookies()}
}

// CA-LOGIN-01: a new Guild has no user, the panel of the Citadel opens the wizard,
// the code of setup makes the Owner, and afterwards the route of the wizard is a 404.
// CA-LOGIN-08: a Tower sends the browser to the Citadel and its API gives no data;
// once the Tower is promoted, the old Citadel, back as a Tower, does the same.
func TestTheWizardMakesTheOwnerAndTheTowerSendsTheBrowserToTheCitadel(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ports := freePorts(t, 2)
	a := w.add("a", ports[0], nil)
	b := w.add("b", ports[1], nil)
	a.cfg.Addresses = []string{b.addr()}
	b.cfg.Addresses = []string{a.addr()}
	ctx := t.Context()

	founded, rk, err := app.Found(ctx, a.cfg, app.DirPaths(a.dir), clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	rk.Destroy()
	if err := os.WriteFile(filepath.Join(w.lock, "epoca-1"), []byte(founded.MachineID), 0o600); err != nil {
		t.Fatal(err)
	}
	w.start(a)
	w.waitForCitadel(a)
	if _, err := app.Enroll(ctx, b.cfg, app.DirPaths(b.dir), a.addr(), transport.SystemNetwork{Suite: suite()}, clock.Real{}, nil); err != nil {
		t.Fatalf("b enters: %v", err)
	}
	w.start(b)
	w.waitFor("b to connect", 0, func() bool {
		st, ok := w.status(b)
		return ok && st.Role == app.RoleTower && st.Connected && st.Epoch == 1
	})

	// the Citadel answers; the Tower redirects, keeping path and query, and gives nothing
	if got := panelDo(t, a, "GET", "/api/status", nil, ""); got.status != 200 || !strings.Contains(string(got.body), `"setup_open":true`) {
		t.Fatalf("status on the Citadel = %d %s", got.status, got.body)
	}
	for _, path := range []string{"/api/status", "/github/apps/novo/abc?origem=telegram", "/"} {
		got := panelDo(t, b, "GET", path, nil, "")
		// the address of the panel (mestre.local, which only the Citadel publishes) and the same path
		if want := "https://mestre.local:" + strconv.Itoa(b.cfg.EnginePort) + path; got.status != http.StatusTemporaryRedirect || got.location != want || len(got.body) != 0 {
			t.Fatalf("GET %s on the Tower = %d %q body %q", path, got.status, got.location, got.body)
		}
	}
	if got := panelDo(t, b, "POST", "/api/login", panel.LoginRequest{Login: "x", Password: "y"}, ""); got.status != http.StatusTemporaryRedirect {
		t.Fatalf("login on the Tower = %d", got.status)
	}

	// the code of setup is made on the Citadel (here by its database, as the
	// command setup-code does) and the wizard makes the Owner
	const code = "ABCDEFGH2345"
	sum := sha256.Sum256([]byte(code))
	w.must(a, map[string]string{"op": "exec", "sql": fmt.Sprintf(`INSERT INTO panel_setup_codes (code_hash, created_at) VALUES ('%s', 1)`, hex.EncodeToString(sum[:]))})
	setup := panel.SetupRequest{Code: code, Name: "Lucas", Login: "lucas", Password: "uma senha bem longa", PIN: "123456"}
	got := panelDo(t, a, "POST", "/api/setup", setup, "")
	if got.status != 200 || len(got.cookies) != 1 {
		t.Fatalf("setup = %d %s", got.status, got.body)
	}
	cookie := got.cookies[0].Value
	if me := panelDo(t, a, "GET", "/api/me", nil, cookie); me.status != 200 || !strings.Contains(string(me.body), `"level":"owner"`) {
		t.Fatalf("me = %d %s", me.status, me.body)
	}
	if again := panelDo(t, a, "POST", "/api/setup", setup, ""); again.status != 404 {
		t.Fatalf("the wizard after the Owner = %d, want 404", again.status)
	}

	// the Citadel falls, b is promoted; the old Citadel returns as a Tower
	lsn := w.mark(a, "t1")
	w.waitFor("b to apply t1", 0, func() bool { st, ok := w.status(b); return ok && st.Connected && st.LSN >= lsn })
	w.kill(a)
	w.must(b, map[string]string{"op": "promote"})
	w.waitFor("b to act as the Citadel", 500*time.Millisecond, func() bool {
		st, ok := w.status(b)
		return ok && st.Role == app.RoleCitadel && st.Epoch == 2 && st.Acting
	})
	w.start(a)
	w.waitFor("a to come back as a Tower of the epoch 2", 500*time.Millisecond, func() bool {
		st, ok := w.status(a)
		return ok && st.Role == app.RoleTower && st.Connected && st.Epoch == 2
	})
	got = panelDo(t, a, "GET", "/api/status?x=1", nil, "")
	if want := "https://mestre.local:" + strconv.Itoa(a.cfg.EnginePort) + "/api/status?x=1"; got.status != http.StatusTemporaryRedirect || got.location != want || len(got.body) != 0 {
		t.Fatalf("the old Citadel = %d %q %q", got.status, got.location, got.body)
	}
	// the new Citadel serves what the old one made: the Owner, with no wizard
	if st := panelDo(t, b, "GET", "/api/status", nil, ""); st.status != 200 || !strings.Contains(string(st.body), `"setup_open":false`) {
		t.Fatalf("status on the new Citadel = %d %s", st.status, st.body)
	}
	if login := panelDo(t, b, "POST", "/api/login", panel.LoginRequest{Login: "lucas", Password: "uma senha bem longa"}, ""); login.status != 200 {
		t.Fatalf("login on the new Citadel = %d %s", login.status, login.body)
	}
}

// `mestre setup-code` makes a code and voids the one before; it refuses an agent,
// one who is not root or an administrator, a machine that is not the Citadel and a
// Guild that has users already.
func TestSetupCodeMakesANewCodeOnlyForRootOnTheCitadelOfAGuildWithNoUser(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	a := w.add("a", freePorts(t, 1)[0], nil)
	paths := app.DirPaths(a.dir)
	_, rk, err := app.Found(t.Context(), a.cfg, paths, clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	rk.Destroy()
	run := func(env map[string]string, elevated bool) (int, string, string) {
		var out, errb bytes.Buffer
		lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
		code := app.SetupCodeWith(t.Context(), []string{"--dir", a.dir}, &out, &errb, lookup, nil, func() bool { return elevated })
		return code, strings.TrimSpace(out.String()), errb.String()
	}
	if code, out, _ := run(map[string]string{"MESTRE_AGENT_ID": "x"}, true); code != 3 || out != "" {
		t.Fatalf("an agent = %d %q", code, out)
	}
	if code, out, _ := run(nil, false); code != 3 || out != "" {
		t.Fatalf("not root = %d %q", code, out)
	}
	code1, first, errb := run(nil, true)
	if code1 != 0 || len(first) != 12 {
		t.Fatalf("first = %d %q %s", code1, first, errb)
	}
	code2, second, _ := run(nil, true)
	if code2 != 0 || len(second) != 12 || second == first {
		t.Fatalf("second = %d %q", code2, second)
	}
}
