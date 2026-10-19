package classes

import (
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// MaxEditPaths bounds the files one character holds in its turn in progress:
// beyond it the new ones are not noted. An informative event can be forged by
// the character, so what it can pile up in the memory of the engine is bounded.
const MaxEditPaths = 1000

// EditTracker is the TurnEdits of a machine: the files each character of a
// table edited in its turn in progress. The engine records the writes that
// went through (post_tool) and ends the turn at turn_end; a table's worktree
// is on one machine, so the tracker is local.
//
// A file may be held by several characters (the one who edited it first does
// not lose its mark to a later one), and a character only ever releases its
// own marks: an event it forges can deny files to the others, never free one.
type EditTracker struct {
	mu      sync.Mutex
	windows bool
	// table → path key → agent → name
	edits map[string]map[string]map[string]string
	held  map[string]int // paths a character holds
}

// NewEditTracker returns an empty tracker that compares paths as the system
// of this machine does.
func NewEditTracker() *EditTracker { return NewEditTrackerFor(runtime.GOOS) }

// NewEditTrackerFor returns an empty tracker that compares paths as goos does
// (the same way the decision does: the spellings Windows takes for one file
// are one key).
func NewEditTrackerFor(goos string) *EditTracker {
	return &EditTracker{windows: goos == "windows", edits: map[string]map[string]map[string]string{}, held: map[string]int{}}
}

// key is the form the decision compares paths in.
func (t *EditTracker) key(p string) string {
	e := &evaluator{windows: t.windows, fold: t.windows}
	return e.key(p)
}

// Record notes that agent (shown as name) edited the paths in its turn.
func (t *EditTracker) Record(tableID, agentID, name string, paths []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	m := t.edits[tableID]
	if m == nil {
		m = map[string]map[string]string{}
		t.edits[tableID] = m
	}
	for _, p := range paths {
		k := t.key(p)
		if k == "" {
			continue
		}
		if _, held := m[k][agentID]; !held {
			if t.held[agentID] >= MaxEditPaths {
				continue
			}
			t.held[agentID]++
		}
		if m[k] == nil {
			m[k] = map[string]string{}
		}
		m[k][agentID] = name
	}
}

// EndTurn forgets what agent edited in the table: its turn is over.
func (t *EditTracker) EndTurn(tableID, agentID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.release(tableID, agentID)
}

// Forget forgets what agent edited in every table: the character is gone (its
// terminal ended or was lost) and will send no turn_end.
func (t *EditTracker) Forget(agentID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for table := range t.edits {
		t.release(table, agentID)
	}
}

func (t *EditTracker) release(tableID, agentID string) {
	m := t.edits[tableID]
	for k, who := range m {
		if _, ok := who[agentID]; ok {
			delete(who, agentID)
			t.held[agentID]--
			if len(who) == 0 {
				delete(m, k)
			}
		}
	}
	if len(m) == 0 {
		delete(t.edits, tableID)
	}
	if t.held[agentID] <= 0 {
		delete(t.held, agentID)
	}
}

// OtherEditor implements TurnEdits: who, other than agent, edited the path in a
// turn in progress (the one with the smallest id when there are several).
func (t *EditTracker) OtherEditor(tableID, agentID, path string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	who := t.edits[tableID][t.key(path)]
	ids := make([]string, 0, len(who))
	for id := range who {
		if id != agentID {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return "", false
	}
	sort.Strings(ids)
	return who[ids[0]], true
}

// pathKey is the loose key the git state compares folders with.
func pathKey(p string) string { return strings.ToLower(filepath.ToSlash(filepath.Clean(p))) }
