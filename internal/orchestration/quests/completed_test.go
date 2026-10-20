package quests_test

import (
	"context"
	"sync"
	"testing"
)

// The end of a mission with all its steps done is told to whoever sends the
// message of progress (CA-GM-15), once, with the workspace and the mesa.
func TestACompletedMissionIsToldToTheMessageOfProgress(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var got []string
	w := newWorld(t, worldOpts{completed: func(_ context.Context, workspace, mesa, quest string) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, workspace+"/"+mesa+"/"+quest)
	}})
	q := w.run(w.create(reviewDef))
	w.finish("Dev Claude 1", "feito", nil, "")
	mu.Lock()
	n := len(got)
	mu.Unlock()
	if n != 0 {
		t.Fatalf("told before the mission ended: %v", got)
	}
	w.finish("Revisor Codex 1", "ok", nil, "")
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != w.slug+"/"+w.table.ID+"/"+q.ID {
		t.Errorf("told = %v", got)
	}
}
