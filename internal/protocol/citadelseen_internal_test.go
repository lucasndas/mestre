package protocol

import (
	"testing"
	"time"
)

// A Tower forgets nothing for ProcessedTTL after the Citadel changes: the
// new Citadel may send again an outbox older than the floor the Tower heard.
func TestATowerHoldsItsPruningAfterTheCitadelChanges(t *testing.T) {
	t.Parallel()
	const ttl = 7 * 24 * time.Hour
	t0 := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	var w citadelSeen
	if !w.holds("old", t0, ttl) {
		t.Error("the first Citadel seen did not hold the pruning")
	}
	if w.holds("old", t0.Add(ttl), ttl) {
		t.Error("the same Citadel still held the pruning after the TTL")
	}
	// the Citadel changes: held again, from the change
	change := t0.Add(ttl + time.Hour)
	if !w.holds("new", change, ttl) {
		t.Error("a new Citadel did not hold the pruning")
	}
	if !w.holds("new", change.Add(ttl-time.Second), ttl) {
		t.Error("the hold ended before the TTL")
	}
	if w.holds("new", change.Add(ttl), ttl) {
		t.Error("the hold lasted past the TTL")
	}
	// an earlier Citadel that comes back holds the pruning again
	if !w.holds("old", change.Add(ttl+time.Hour), ttl) {
		t.Error("the return of an earlier Citadel did not hold the pruning")
	}
}
