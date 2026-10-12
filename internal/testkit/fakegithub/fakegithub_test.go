package fakegithub_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/testkit"
	"github.com/lucasndas/mestre/internal/testkit/fakegithub"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/pkg/clock"
)

type recorder struct{ msgs []string }

func (r *recorder) Errorf(format string, args ...any) {
	r.msgs = append(r.msgs, fmt.Sprintf(format, args...))
}

func setup(t *testing.T, rep testkit.Reporter) (*fakegithub.Server, *clock.Fake, func(method, path, etag, body string) *http.Response) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC))
	s, err := fakegithub.New(clk, rep)
	if err != nil {
		t.Fatal(err)
	}
	s.AddRepo("octo", "repo")
	s.IssueToken("inst-token", "mestre-grao-mestre")
	c := &http.Client{Transport: fakenet.HandlerTransport(s)}
	do := func(method, path, etag, body string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, "https://api.github.com"+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer inst-token")
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp
	}
	return s, clk, do
}

// CA-DUBLES-07
func TestConditionalRequestAnswers304WithoutSpendingQuota(t *testing.T) {
	t.Parallel()
	_, _, do := setup(t, t)
	do("POST", "/repos/octo/repo/issues", "", `{"title":"epic","labels":["epico"]}`)

	first := do("GET", "/repos/octo/repo/issues", "", "")
	etag := first.Header.Get("ETag")
	remaining := first.Header.Get("x-ratelimit-remaining")
	if first.StatusCode != 200 || etag == "" {
		t.Fatalf("first GET = %d, etag %q", first.StatusCode, etag)
	}

	again := do("GET", "/repos/octo/repo/issues", etag, "")

	if again.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional GET = %d, want 304", again.StatusCode)
	}
	if got := again.Header.Get("x-ratelimit-remaining"); got != remaining {
		t.Fatalf("remaining went from %s to %s", remaining, got)
	}
}

func TestChangedResourceGetsNewETagAndActionsRecordTheApp(t *testing.T) {
	t.Parallel()
	s, _, do := setup(t, t)
	etag := do("GET", "/repos/octo/repo/issues", "", "").Header.Get("ETag")
	do("POST", "/repos/octo/repo/issues", "", `{"title":"x"}`)
	if got := do("GET", "/repos/octo/repo/issues", etag, ""); got.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 after a change", got.StatusCode)
	}
	if a := s.Actions(); len(a) != 1 || a[0].App != "mestre-grao-mestre" {
		t.Fatalf("actions = %+v", a)
	}
}

func TestInstallationTokenExpiresAfterOneHour(t *testing.T) {
	t.Parallel()
	_, clk, do := setup(t, t)
	clk.Advance(59 * time.Minute)
	if got := do("GET", "/repos/octo/repo/issues", "", "").StatusCode; got != 200 {
		t.Fatalf("status = %d before expiry", got)
	}
	clk.Advance(time.Minute)
	if got := do("GET", "/repos/octo/repo/issues", "", "").StatusCode; got != http.StatusUnauthorized {
		t.Fatalf("status = %d after expiry, want 401", got)
	}
}

// CA-DUBLES-09
func TestUnknownRouteFailsTheTestAsNotImplemented(t *testing.T) {
	t.Parallel()
	rep := &recorder{}
	_, _, do := setup(t, rep)

	resp := do("GET", "/repos/octo/repo/pulls", "", "")

	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 (never an empty success)", resp.StatusCode)
	}
	if len(rep.msgs) != 1 || rep.msgs[0] != "not implemented in double: GET /repos/octo/repo/pulls" {
		t.Fatalf("test failures = %q", rep.msgs)
	}
}
