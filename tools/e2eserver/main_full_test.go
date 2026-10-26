//go:build full

package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

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
	go func() {
		err := run(ctx, pw, io.Discard, nil)
		pw.Close()
		errc <- err
	}()
	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil {
		t.Fatalf("no ready line: %v", err)
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
