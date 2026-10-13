package launcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/lucasndas/mestre/internal/privdir"
)

// PTYEntry is a mestre-pty the launcher started, by PID and start identity
// (terminal.ProcID): the creation time on Windows, so a reused PID is never
// taken for it.
type PTYEntry struct {
	ID    string `json:"id"`
	PID   int    `json:"pid"`
	Start string `json:"start"`
}

// PTYList is the list of live mestre-pty a launcher keeps on disk, to take
// them back after a restart (Windows: to keep unloading the profile and
// closing the logon session of each when it ends). It holds no secret.
type PTYList struct {
	path string
	mu   sync.Mutex
	byID map[string]PTYEntry
}

// OpenPTYList reads the list at path; a file that does not exist is an empty
// list. A file that cannot be read gives an empty list and the error, so the
// caller can log it and carry on.
func OpenPTYList(path string) (*PTYList, error) {
	l := &PTYList{path: path, byID: map[string]PTYEntry{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return l, fmt.Errorf("read the list of mestre-pty: %w", err)
	}
	var es []PTYEntry
	if err := json.Unmarshal(b, &es); err != nil {
		return l, fmt.Errorf("the list of mestre-pty %s is corrupt: %w", path, err)
	}
	for _, e := range es {
		l.byID[e.ID] = e
	}
	return l, nil
}

// Entries returns the list, ordered by terminal.
func (l *PTYList) Entries() []PTYEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sorted()
}

func (l *PTYList) sorted() []PTYEntry {
	es := make([]PTYEntry, 0, len(l.byID))
	for _, e := range l.byID {
		es = append(es, e)
	}
	slices.SortFunc(es, func(a, b PTYEntry) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		}
		return 0
	})
	return es
}

// Add records e (replacing the entry of the same terminal) and writes the
// list.
func (l *PTYList) Add(e PTYEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.byID[e.ID] = e
	return l.save()
}

// Remove drops the terminal id and writes the list.
func (l *PTYList) Remove(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.byID[id]; !ok {
		return nil
	}
	delete(l.byID, id)
	return l.save()
}

// save writes the list whole to a temporary file and renames it over the
// old one, so a crash leaves one or the other.
func (l *PTYList) save() error {
	b, err := json.Marshal(l.sorted())
	if err != nil {
		return err
	}
	dir := filepath.Dir(l.path)
	if err := privdir.Ensure(dir); err != nil {
		return fmt.Errorf("folder of the list of mestre-pty: %w", err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(l.path)+".*")
	if err != nil {
		return fmt.Errorf("write the list of mestre-pty: %w", err)
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write the list of mestre-pty: %w", err)
	}
	if err := os.Rename(tmp.Name(), l.path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("replace the list of mestre-pty: %w", err)
	}
	return nil
}
