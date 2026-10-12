package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"time"
)

// HistoryPath keeps the heavy-suite timings, outside git.
const HistoryPath = ".testrun/heavy-history.json"

// Heavy-suite comparison: warn above this ratio of the median of the last
// historyWindow runs.
const (
	historyWindow = 7
	heavyWarnPct  = 20
)

type heavyRun struct {
	At      time.Time `json:"at"`
	Seconds float64   `json:"seconds"`
}

func loadHistory(fsys fs.FS) ([]heavyRun, error) {
	b, err := fs.ReadFile(fsys, HistoryPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", HistoryPath, err)
	}
	var h []heavyRun
	if err := json.Unmarshal(b, &h); err != nil {
		return nil, fmt.Errorf("parse %s: %w", HistoryPath, err)
	}
	return h, nil
}

// median of the last historyWindow runs; ok=false without history.
func median(h []heavyRun) (time.Duration, bool) {
	if len(h) > historyWindow {
		h = h[len(h)-historyWindow:]
	}
	if len(h) == 0 {
		return 0, false
	}
	s := make([]float64, len(h))
	for i, r := range h {
		s[i] = r.Seconds
	}
	sort.Float64s(s)
	m := s[len(s)/2]
	if len(s)%2 == 0 {
		m = (s[len(s)/2-1] + s[len(s)/2]) / 2
	}
	return time.Duration(m * float64(time.Second)), true
}

// heavyWarning returns the warning for a run of d, or "".
func heavyWarning(h []heavyRun, d time.Duration) string {
	m, ok := median(h)
	if !ok || d*100 <= m*(100+heavyWarnPct) {
		return ""
	}
	n := min(len(h), historyWindow)
	return fmt.Sprintf("heavy suite took %v, more than %d%% above the median %v of the last %d runs", d.Round(time.Second), heavyWarnPct, m.Round(time.Second), n)
}
