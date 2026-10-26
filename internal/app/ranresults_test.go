package app

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mestre-ia/mestre/pkg/clock"
)

// The result of a call that ran goes to the inbox once, whether the
// duplicate from the outbox arrives after the run ended or while it still
// runs; a call that never comes back delivers nothing, and an id that did
// not run here is nobody's.
func TestTheResultOfARunGoesToTheDuplicateOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		got := map[string]int{}
		r := newRanCalls(clock.Real{}, func(q QueuedResult) {
			mu.Lock()
			got[q.OpID]++
			mu.Unlock()
		})
		// the duplicate after the end of the run
		r.started("b", "1")
		r.finished("b", "1", QueuedResult{OpID: "1"})
		r.duplicate("b", "1")
		r.duplicate("b", "1")
		// the duplicate while it runs
		r.started("b", "2")
		r.duplicate("b", "2")
		r.finished("b", "2", QueuedResult{OpID: "2"})
		// no duplicate; a duplicate of another sender; an id that did not run
		r.started("b", "3")
		r.finished("b", "3", QueuedResult{OpID: "3"})
		r.duplicate("c", "3")
		r.duplicate("b", "4")
		synctest.Wait()
		mu.Lock()
		defer mu.Unlock()
		if got["1"] != 1 || got["2"] != 1 || got["3"] != 0 || len(got) != 2 {
			t.Fatalf("delivered %v; want 1 and 2 once each", got)
		}
	})
}

// What is kept is bounded: the results older than the keep time, or beyond
// the most kept, go.
func TestTheRanResultsAreBounded(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRanCalls(clock.Real{}, func(QueuedResult) {})
		for i := range ranMax + 10 {
			r.started("b", string(rune('a'+i%26))+time.Duration(i).String())
		}
		if n := len(r.calls); n > ranMax {
			t.Fatalf("%d kept; at most %d", n, ranMax)
		}
		_ = clock.Sleep(t.Context(), clock.Real{}, ranKeep+time.Second)
		r.started("b", "new")
		if n := len(r.calls); n != 1 {
			t.Fatalf("%d kept after the keep time; want only the new one", n)
		}
	})
}
