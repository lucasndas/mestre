//go:build full

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/store"
	guilddb "github.com/mestre-ia/mestre/internal/store/guild"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The server of the browser smoke test starts a real Citadel with the panel
// embedded, prints the ready line, answers over HTTPS with the wizard open, and
// stops when its context ends.
func TestTheE2EServerServesTheEmbeddedPanelAndStops(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	pr, pw := io.Pipe()
	errc := make(chan error, 1)
	var stderr lockedBuffer
	go func() {
		err := runWith(ctx, pw, &stderr, nil, runOptions{Seed: true})
		pw.Close()
		errc <- err
	}()
	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil {
		cancel()
		t.Fatalf("no ready line: %v; run = %v; stderr: %s", err, <-errc, stderr.String())
	}
	url, code, ok := parseReady(line)
	if !ok {
		t.Fatalf("not a ready line: %q", line)
	}
	if len(code) < 8 {
		t.Errorf("setup code %q", code)
	}
	c := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableKeepAlives: true}} //nolint:gosec // test
	get := func(path string) (int, string) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url+path, nil)
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	if st, body := get("/api/status"); st != 200 || !strings.Contains(body, `"setup_open":true`) {
		t.Errorf("status = %d %s", st, body)
	}
	// The Go jobs do not build the front end (docs/decisoes/0027): without a
	// bundle the panel answers its own "not built" page, which still proves the
	// panel is mounted; with one (the e2e job), it is the real index.
	st, body := get("/")
	built := st == 200 && strings.Contains(body, "<title>Mestre</title>")
	notBuilt := st == 503 && strings.Contains(body, "não foi construído")
	if !built && !notBuilt {
		t.Errorf("index = %d %s", st, body)
	}
	cancel()
	wait, stop := clock.WithTimeout(context.Background(), clock.Real{}, 30*time.Second)
	defer stop()
	select {
	case err := <-errc:
		if err != nil {
			t.Errorf("run = %v", err)
		}
	case <-wait.Done():
		t.Fatal("the server did not stop")
	}
}

// lockedBuffer is a buffer the server writes to while the test reads it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// serve runs the server over a folder and returns its address, the setup code and what
// stops it (and waits for the engine to end).
func serve(t *testing.T, o runOptions) (url, code string, stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	pr, pw := io.Pipe()
	errc := make(chan error, 1)
	var stderr lockedBuffer
	go func() {
		err := runWith(ctx, pw, &stderr, nil, o)
		pw.Close()
		errc <- err
	}()
	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil {
		cancel()
		t.Fatalf("no ready line: %v; run = %v; stderr: %s", err, <-errc, stderr.String())
	}
	url, code, ok := parseReady(line)
	if !ok {
		cancel()
		t.Fatalf("not a ready line: %q", line)
	}
	var once sync.Once
	return url, code, func() {
		once.Do(func() { stopServer(t, cancel, errc) })
	}
}

func stopServer(t *testing.T, cancel context.CancelFunc, errc <-chan error) {
	{
		cancel()
		wait, done := clock.WithTimeout(context.Background(), clock.Real{}, 60*time.Second)
		defer done()
		select {
		case err := <-errc:
			if err != nil {
				t.Errorf("run = %v", err)
			}
		case <-wait.Done():
			t.Fatal("the server did not stop")
		}
	}
}

// panelCall calls the panel as a browser on its address would.
func panelCall(t *testing.T, url, method, path string, body any, cookie, pin string) (int, []byte, *http.Cookie) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, url+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", url)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: panel.CookieName, Value: cookie})
	}
	if pin != "" {
		req.Header.Set(panel.PINHeader, pin)
	}
	c := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableKeepAlives: true}} //nolint:gosec // the Guild CA is in no trust store
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var ck *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == panel.CookieName {
			ck = c
		}
	}
	return res.StatusCode, b, ck
}

// CA-APROV-FLUXO-08: a request waits for the people, the Citadel is restarted, the Owner
// approves it through the panel with the PIN (which the request asks), and the decision is
// applied and audited: the request is approved by him, the audit of the flow has the
// answer, and the history of the panel tells it.
func TestARequestThatWaitedThroughARestartIsApprovedThroughThePanelAndAudited(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	url, code, stop := serve(t, runOptions{Dir: dir, Seed: true})
	setup := panel.SetupRequest{Code: code, Name: "Lucas", Login: "lucas", Password: "uma senha bem longa", PIN: "123456"}
	if st, body, _ := panelCall(t, url, "POST", "/api/setup", setup, "", ""); st != 200 {
		stop()
		t.Fatalf("setup = %d %s", st, body)
	}
	stop() // the Citadel stops with the request waiting

	url, _, stop = serve(t, runOptions{Dir: dir})
	defer stop()
	st, body, ck := panelCall(t, url, "POST", "/api/login", panel.LoginRequest{Login: "lucas", Password: "uma senha bem longa"}, "", "")
	if st != 200 || ck == nil {
		t.Fatalf("login = %d %s", st, body)
	}
	st, body, _ = panelCall(t, url, "GET", "/api/approvals", nil, ck.Value, "")
	var list panel.ApprovalList
	if err := json.Unmarshal(body, &list); err != nil || st != 200 || len(list.Requests) != 1 || list.Pending != 1 {
		t.Fatalf("after the restart: %d %s", st, body)
	}
	item := list.Requests[0]
	if item.Type != "custo_extra" || len(item.Answers) == 0 {
		t.Fatalf("the request after the restart: %+v", item)
	}
	path := "/api/approvals/" + strconv.FormatInt(item.ID, 10) + "/answer"
	// the PIN is asked: no approval without it, and a wrong one decides nothing
	if st, body, _ := panelCall(t, url, "POST", path, panel.ApprovalAnswerRequest{Answer: "approve"}, ck.Value, ""); st != 403 || !strings.Contains(string(body), `"pin_required"`) {
		t.Fatalf("without the PIN = %d %s", st, body)
	}
	if st, body, _ := panelCall(t, url, "POST", path, panel.ApprovalAnswerRequest{Answer: "approve"}, ck.Value, "000000"); st != 403 || !strings.Contains(string(body), `"pin_invalid"`) {
		t.Fatalf("a wrong PIN = %d %s", st, body)
	}
	st, body, _ = panelCall(t, url, "POST", path, panel.ApprovalAnswerRequest{Answer: "approve"}, ck.Value, "123456")
	if st != 200 || !strings.Contains(string(body), `"approved"`) {
		t.Fatalf("approve = %d %s", st, body)
	}
	// nothing is left waiting, and the history tells who decided
	st, body, _ = panelCall(t, url, "GET", "/api/approvals", nil, ck.Value, "")
	if err := json.Unmarshal(body, &list); err != nil || st != 200 || len(list.Requests) != 0 || list.Pending != 0 {
		t.Fatalf("after the answer: %d %s", st, body)
	}
	var hist panel.ApprovalHistory
	st, body, _ = panelCall(t, url, "GET", "/api/approvals/history", nil, ck.Value, "")
	if err := json.Unmarshal(body, &hist); err != nil || st != 200 || len(hist.Items) != 1 || hist.Items[0].State != "approved" || hist.Items[0].By != "Lucas" || hist.Items[0].LevelKind != "people" {
		t.Fatalf("history = %d %s", st, body)
	}
	stop()

	// the audit of the flow and of the panel, in the database the engine left
	db, err := guilddb.Open(t.Context(), filepath.Join(dir, "banco", "mestre.db"), store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatalf("the Guild database: %v", err)
	}
	defer db.Close()
	var outcome, answer string
	if err := db.Reader().QueryRowContext(t.Context(), `SELECT outcome, answer FROM approval_audit WHERE request = ? AND outcome = 'accepted'`, item.ID).Scan(&outcome, &answer); err != nil || answer != "approve" {
		t.Fatalf("the audit of the flow: %q %q %v", outcome, answer, err)
	}
	var n int
	if err := db.Reader().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_log WHERE action = 'panel.approval.answered'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the audit of the panel: %d %v", n, err)
	}
}
