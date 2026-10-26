package panel_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mestre-ia/mestre/internal/panel"
)

type place struct {
	place panel.Place
	url   string
}

func (p *place) Place() panel.Place { return p.place }
func (p *place) CitadelURL() string { return p.url }

func gate(t *testing.T, p *place) (http.Handler, *int) {
	t.Helper()
	served := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { served++; _, _ = w.Write([]byte("panel")) })
	return panel.Gate(p, next), &served
}

// CA-LOGIN-09, and the 307 of the redirected panel (the planned switch is proved in
// internal/app, by the door of the panel): a Tower answers 307 to the same path and query on the Citadel, and gives
// no data; a suspended Citadel does the same when it knows the Citadel of now,
// and shows only the page "Cidadela suspensa" when it does not.
//
// Part of CA-CIDADELA-08
func TestATowerRedirectsEverythingToTheCitadelKeepingPathAndQuery(t *testing.T) {
	t.Parallel()
	p := &place{place: panel.PlaceTower, url: "https://mestre.local:7443"}
	h, served := gate(t, p)
	for _, c := range []struct{ method, target, want string }{
		{"GET", "/github/apps/novo/abc?origem=telegram", "https://mestre.local:7443/github/apps/novo/abc?origem=telegram"},
		{"GET", "/", "https://mestre.local:7443/"},
		{"GET", "/api/me", "https://mestre.local:7443/api/me"},
		{"POST", "/api/login", "https://mestre.local:7443/api/login"},
		{"GET", "/ws/ui", "https://mestre.local:7443/ws/ui"},
		{"GET", "//evil.example/x?a=b&c=d%20e", "https://mestre.local:7443//evil.example/x?a=b&c=d%20e"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(c.method, c.target, nil))
		if w.Code != http.StatusTemporaryRedirect || w.Header().Get("Location") != c.want || w.Body.Len() != 0 {
			t.Errorf("%s %s = %d %q body %q, want 307 %q", c.method, c.target, w.Code, w.Header().Get("Location"), w.Body, c.want)
		}
	}
	if *served != 0 {
		t.Fatal("a Tower served the panel")
	}
	// the Citadel changes: the same address leads to the new one
	p.url = "https://mestre.local:7453"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/painel?x=1", nil))
	if w.Header().Get("Location") != "https://mestre.local:7453/painel?x=1" {
		t.Fatalf("after the switch: %q", w.Header().Get("Location"))
	}
}

func TestASuspendedCitadelRedirectsWhenItKnowsTheCurrentOneAndElseShowsOnlyThePage(t *testing.T) {
	t.Parallel()
	p := &place{place: panel.PlaceSuspended, url: "https://torre.local:7443"}
	h, served := gate(t, p)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/a?b=c", nil))
	if w.Code != 307 || w.Header().Get("Location") != "https://torre.local:7443/a?b=c" {
		t.Fatalf("knows the Citadel: %d %q", w.Code, w.Header().Get("Location"))
	}
	p.url = ""
	for _, target := range []string{"/", "/api/me", "/github/apps/novo/abc"} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
		body := w.Body.String()
		if w.Code != 503 || !contains(body, "Cidadela suspensa") || contains(body, "<script") || contains(body, "<form") || w.Header().Get("Location") != "" {
			t.Errorf("%s = %d %q", target, w.Code, body)
		}
	}
	if *served != 0 {
		t.Fatal("a suspended Citadel served the panel")
	}
}

func TestTheCitadelThatActsServesThePanel(t *testing.T) {
	t.Parallel()
	h, served := gate(t, &place{place: panel.PlaceCitadel})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/me", nil))
	if w.Code != 200 || *served != 1 {
		t.Fatalf("%d, served %d", w.Code, *served)
	}
}
