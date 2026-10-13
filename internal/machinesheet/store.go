package machinesheet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// SheetStore keeps the sheet the agent last told the Citadel across restarts.
// What it holds is the starting point of the next process: a probe that fails
// as the engine comes up (the CLIs are not answering yet) keeps its part of
// that sheet instead of reporting the machine without providers, and the
// revision goes on above the last one sent, whatever the clock says.
type SheetStore interface {
	// Load returns the sheet kept; ok is false when there is none.
	Load(ctx context.Context) (s Sheet, ok bool, err error)
	Save(ctx context.Context, s Sheet) error
}

// FileStore is a SheetStore in a JSON file of the engine's private folder.
type FileStore string

// Load reads the file. A file that does not parse is not an error of the agent:
// it counts as no sheet kept.
func (f FileStore) Load(context.Context) (Sheet, bool, error) {
	b, err := os.ReadFile(string(f))
	if errors.Is(err, fs.ErrNotExist) {
		return Sheet{}, false, nil
	}
	if err != nil {
		return Sheet{}, false, fmt.Errorf("machinesheet: %w", err)
	}
	var s Sheet
	if json.Unmarshal(b, &s) != nil || s.Validate() != nil {
		return Sheet{}, false, nil
	}
	return s, true, nil
}

// Save writes the file next to its place and moves it over it.
func (f FileStore) Save(_ context.Context, s Sheet) error {
	b, err := json.Marshal(s.normalized())
	if err != nil {
		return err
	}
	tmp := string(f) + ".tmp"
	if err := os.MkdirAll(filepath.Dir(tmp), 0o750); err != nil {
		return fmt.Errorf("machinesheet: %w", err)
	}
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("machinesheet: %w", err)
	}
	if err := os.Rename(tmp, string(f)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("machinesheet: %w", err)
	}
	return nil
}
