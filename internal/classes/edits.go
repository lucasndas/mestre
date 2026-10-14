package classes

import (
	"path/filepath"
	"strings"
	"sync"
)

// EditTracker is the TurnEdits of a machine: the files each character of a
// table edited in its turn in progress. The dispatcher records the writes
// that went through (post_tool) and ends the turn at turn_end; a table's
// worktree is on one machine, so the tracker is local.
type EditTracker struct {
	mu    sync.Mutex
	edits map[string]map[string]editor // table → path key → editor
}

type editor struct{ agent, name string }

// NewEditTracker returns an empty tracker.
func NewEditTracker() *EditTracker { return &EditTracker{edits: map[string]map[string]editor{}} }

// Record notes that agent (shown as name) edited the paths in its turn.
func (t *EditTracker) Record(tableID, agentID, name string, paths []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	m := t.edits[tableID]
	if m == nil {
		m = map[string]editor{}
		t.edits[tableID] = m
	}
	for _, p := range paths {
		m[pathKey(p)] = editor{agentID, name}
	}
}

// EndTurn forgets what agent edited: its turn is over.
func (t *EditTracker) EndTurn(tableID, agentID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for p, ed := range t.edits[tableID] {
		if ed.agent == agentID {
			delete(t.edits[tableID], p)
		}
	}
}

// OtherEditor implements TurnEdits.
func (t *EditTracker) OtherEditor(tableID, agentID, path string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ed, ok := t.edits[tableID][pathKey(path)]
	if !ok || ed.agent == agentID {
		return "", false
	}
	return ed.name, true
}

func pathKey(p string) string { return strings.ToLower(filepath.ToSlash(filepath.Clean(p))) }
