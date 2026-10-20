package github

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

// Quota follows the primary quota GitHub gives each App from the headers it
// answers with (x-ratelimit-*): below 10% of it, what changes data waits for the
// renewal and a reading is refused with the time of the renewal
// (docs/cli/coordenacao-github.md#regras-e-erros).
type Quota struct {
	Clock clock.Clock

	mu sync.Mutex
	by map[string]quotaState
}

type quotaState struct {
	remaining, limit int
	reset            time.Time
}

// Observe records the headers of an answer to a request of the App.
func (q *Quota) Observe(app string, h http.Header) {
	limit, e1 := strconv.Atoi(h.Get("x-ratelimit-limit"))
	remaining, e2 := strconv.Atoi(h.Get("x-ratelimit-remaining"))
	reset, e3 := strconv.ParseInt(h.Get("x-ratelimit-reset"), 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || limit <= 0 {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.by == nil {
		q.by = map[string]quotaState{}
	}
	q.by[app] = quotaState{remaining: remaining, limit: limit, reset: time.Unix(reset, 0)}
}

// Low says whether the App is under 10% of its quota, and when it renews.
func (q *Quota) Low(app string) (reset time.Time, low bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	s, ok := q.by[app]
	if !ok || !q.Clock.Now().Before(s.reset) {
		return time.Time{}, false
	}
	return s.reset, s.remaining*10 < s.limit
}
