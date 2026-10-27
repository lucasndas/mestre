package panel_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mestre-ia/mestre/internal/panel"
)

type holdingPlace struct {
	place
	open     bool
	released int
}

func (p *holdingPlace) Hold() (func(), bool) {
	if !p.open {
		return nil, false
	}
	return func() { p.released++ }, true
}

// Part of CA-CIDADELA-19: a request of the panel that changes something holds the gate of the
// handover until it ends, and is refused like any request of a suspended Citadel once the
// Citadel drains; a read is not held, and goes on.
func TestARequestThatChangesSomethingHoldsTheGateOfTheHandoverUntilItEnds(t *testing.T) {
	t.Parallel()
	p := &holdingPlace{place: place{place: panel.PlaceCitadel}}
	served, releasedInside := 0, -1
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served++
		releasedInside = p.released
		_, _ = w.Write([]byte("panel"))
	})
	h := panel.Gate(p, next)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/users", nil))
	if w.Code != http.StatusServiceUnavailable || served != 0 {
		t.Fatalf("POST while the Citadel drains = %d, served %d; want 503 and nothing served", w.Code, served)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/users", nil))
	if w.Code != http.StatusOK || served != 1 {
		t.Fatalf("GET while the Citadel drains = %d, served %d; want it served", w.Code, served)
	}

	p.open = true
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("PUT", "/api/users/1", nil))
	if w.Code != http.StatusOK || served != 2 || releasedInside != 0 || p.released != 1 {
		t.Fatalf("PUT with the gate open = %d, served %d, released inside %d, after %d; want served, held while it ran, freed after", w.Code, served, releasedInside, p.released)
	}
}
