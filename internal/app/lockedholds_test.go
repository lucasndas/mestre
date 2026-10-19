package app

import (
	"context"
	"testing"
)

type holdsStub struct{ held bool }

func (h holdsStub) Held(context.Context, string) (bool, error) { return h.held, nil }

// Part of CA-CANAL-04: with the lock on, the inbox holds every character; the
// messages wait, and the circuit breaker still answers when it is not locked.
func TestTheLockHoldsEveryCharacterOfTheInbox(t *testing.T) {
	t.Parallel()
	locked := false
	h := lockedHolds{locked: func(context.Context) bool { return locked }, then: holdsStub{}}
	if held, _ := h.Held(t.Context(), "c1"); held {
		t.Error("held while unlocked")
	}
	locked = true
	if held, _ := h.Held(t.Context(), "c1"); !held {
		t.Error("not held while locked")
	}
	locked = false
	h.then = holdsStub{held: true}
	if held, _ := h.Held(t.Context(), "c1"); !held {
		t.Error("the circuit breaker no longer holds")
	}
	if held, _ := (lockedHolds{then: holdsStub{}}).Held(t.Context(), "c1"); held {
		t.Error("held with no lock set")
	}
}
