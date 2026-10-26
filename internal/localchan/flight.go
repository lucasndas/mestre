package localchan

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/pkg/clock"
)

// maxPartials bounds the partial responses kept for a request that is
// resent; later ones still reach the connections that are listening.
const maxPartials = 256

// flightKey identifies a request: the verified character and the id the CLI
// generated. The character is part of the key so that nobody can read the
// stored response of another character by guessing its id.
type flightKey struct{ agent, id string }

// flight is a request in progress or finished: the partials sent so far
// and, once done, the stored response a repeated request gets back
// (docs/arquitetura/canal-local.md: a request with an id already received
// never runs again).
type flight struct {
	cmd string

	mu       sync.Mutex
	partials []json.RawMessage
	changed  chan struct{} // closed and replaced on every change
	done     bool
	resp     Response
	doneAt   time.Time
}

func newFlight(cmd string) *flight { return &flight{cmd: cmd, changed: make(chan struct{})} }

func (f *flight) publish(data json.RawMessage) {
	f.mu.Lock()
	if len(f.partials) < maxPartials {
		f.partials = append(f.partials, data)
	}
	f.wakeLocked()
	f.mu.Unlock()
}

func (f *flight) finish(resp Response, at time.Time) {
	f.mu.Lock()
	f.done, f.resp, f.doneAt = true, resp, at
	f.wakeLocked()
	f.mu.Unlock()
}

func (f *flight) wakeLocked() {
	close(f.changed)
	f.changed = make(chan struct{})
}

// snapshot returns the partials from index next on, the channel that closes
// at the next change, and the response if the request is done.
func (f *flight) snapshot(next int) (parts []json.RawMessage, changed <-chan struct{}, final *Response) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if next < len(f.partials) {
		parts = append(parts, f.partials[next:]...)
	}
	if f.done {
		r := f.resp
		final = &r
	}
	return parts, f.changed, final
}

// flights is the table of requests by id.
type flights struct {
	clk    clock.Clock
	retain time.Duration
	limit  int

	mu sync.Mutex
	m  map[flightKey]*flight
}

func newFlights(clk clock.Clock, retain time.Duration, limit int) *flights {
	return &flights{clk: clk, retain: retain, limit: limit, m: map[flightKey]*flight{}}
}

// join returns the flight of key. owner is true when the request is new
// and the caller must run it; otherwise it is running or finished, and the
// caller only listens. A request with the same id and another command is a
// bug of the CLI.
func (t *flights) join(key flightKey, cmd string) (f *flight, owner bool, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.purgeLocked()
	if f, ok := t.m[key]; ok {
		if f.cmd != cmd {
			return nil, false, fmt.Errorf("request id %s was used for %q, not %q", key.id, f.cmd, cmd)
		}
		return f, false, nil
	}
	f = newFlight(cmd)
	t.m[key] = f
	return f, true, nil
}

// purgeLocked forgets the finished requests older than the retention, and
// the oldest finished ones beyond the limit. A request in progress is never
// forgotten.
func (t *flights) purgeLocked() {
	now := t.clk.Now()
	var oldest flightKey
	var oldestAt time.Time
	for k, f := range t.m {
		f.mu.Lock()
		done, at := f.done, f.doneAt
		f.mu.Unlock()
		if !done {
			continue
		}
		if now.Sub(at) > t.retain {
			delete(t.m, k)
			continue
		}
		if oldestAt.IsZero() || at.Before(oldestAt) {
			oldest, oldestAt = k, at
		}
	}
	if len(t.m) > t.limit && !oldestAt.IsZero() {
		delete(t.m, oldest)
	}
}
