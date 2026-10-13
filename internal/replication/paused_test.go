package replication_test

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// A Citadel that does not act (suspended at the start) prunes nothing from the
// change log: a demotion would report as discarded what it held back. Once it
// acts, the retention runs.
func TestASuspendedCitadelDoesNotPruneTheLog(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var paused atomic.Bool
		paused.Store(true)
		r := newRig(t, rigOpts{lagThreshold: 24 * time.Hour, paused: paused.Load})
		for i := range 10 {
			r.insertMachine(i)
		}
		first, _, err := r.cdb.LogBounds(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		advance(3 * time.Hour) // far past the retention of one hour
		r.insertMachine(100)
		advance(2 * time.Minute) // the Citadel prunes every minute
		lo, _, err := r.cdb.LogBounds(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if lo != first {
			t.Fatalf("a Citadel that does not act pruned the log: the lowest lsn went from %d to %d", first, lo)
		}
		paused.Store(false)
		advance(2 * time.Minute)
		lo, _, err = r.cdb.LogBounds(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if lo <= first {
			t.Fatalf("the retention did not run once the Citadel acts: lowest lsn %d", lo)
		}
	})
}
