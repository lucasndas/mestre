package providers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/safefile"
)

// The session transcript of Codex ($CODEX_HOME/sessions/AAAA/MM/DD/
// rollout-*.jsonl, docs/provedores/codex.md#uso-custo-e-rate-limit) gives
// the use (token_count events: tokens, the context window and the rate
// limits) and the turns that ended in error (task_complete or turn_aborted
// with error): Codex runs no Stop hook on such a turn. The format is
// internal to Codex; the conformance test checks it at each version.

// rolloutLine is one line of the transcript; only event_msg lines count.
type rolloutLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type rolloutEvent struct {
	Type       string            `json:"type"`
	TurnID     string            `json:"turn_id"`
	Info       *rolloutInfo      `json:"info"`
	RateLimits *rolloutLimits    `json:"rate_limits"`
	Error      *rolloutTurnError `json:"error"`
}

type rolloutInfo struct {
	Total         rolloutTokens `json:"total_token_usage"`
	Last          rolloutTokens `json:"last_token_usage"`
	ContextWindow *int64        `json:"model_context_window"`
}

type rolloutTokens struct {
	Input      int64 `json:"input_tokens"`
	CachedIn   int64 `json:"cached_input_tokens"`
	CacheWrite int64 `json:"cache_write_input_tokens"`
	Output     int64 `json:"output_tokens"`
	Reasoning  int64 `json:"reasoning_output_tokens"`
	Total      int64 `json:"total_tokens"`
}

func (a rolloutTokens) minus(b rolloutTokens) rolloutTokens {
	d := rolloutTokens{a.Input - b.Input, a.CachedIn - b.CachedIn, a.CacheWrite - b.CacheWrite, a.Output - b.Output, a.Reasoning - b.Reasoning, a.Total - b.Total}
	if d.Input < 0 || d.Output < 0 || d.Total < 0 { // a total that went back: a new count
		return a
	}
	return d
}

type rolloutLimits struct {
	Primary   *rolloutWindow `json:"primary"`
	Secondary *rolloutWindow `json:"secondary"`
	Reached   string         `json:"rate_limit_reached_type"`
}

type rolloutWindow struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes *int64  `json:"window_minutes"`
	ResetsAt      *int64  `json:"resets_at"` // Unix seconds
}

type rolloutTurnError struct {
	Message string          `json:"message"`
	Info    json.RawMessage `json:"codex_error_info"`
}

// codexCursor is where a reading stopped: the offset after the last whole
// line read and the session totals at that point.
type codexCursor struct {
	Offset int64         `json:"offset"`
	Total  rolloutTokens `json:"total"`
}

// ReadUsage reads the transcript of the last event from where the previous
// reading stopped (req.Cursor): the use is the difference of the session
// totals of the newest token_count, with its context window and the two
// limit windows; rate_limit_reached_type filled marks the limits reached.
// The turns that ended in error come back in Failures, and Cursor is where
// the next reading starts. Without a new token_count the answer is
// ErrNoUsage, still with Cursor and Failures.
func (c *codex) ReadUsage(req UsageRequest) (Usage, error) {
	var cur codexCursor
	if req.Cursor != "" {
		if err := json.Unmarshal([]byte(req.Cursor), &cur); err != nil {
			return Usage{}, fmt.Errorf("codex usage: bad cursor: %w", err)
		}
	}
	f, err := openCodexTranscript(req.Profile, req.Last.TranscriptPath)
	if err != nil {
		return Usage{}, err
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() < cur.Offset {
		cur = codexCursor{} // the file was replaced: read it again
	}
	if _, err := f.Seek(cur.Offset, io.SeekStart); err != nil {
		return Usage{}, fmt.Errorf("codex usage: %w", err)
	}
	var (
		failures []Event
		counted  bool
		info     *rolloutInfo   // the newest session totals
		limits   *rolloutLimits // the newest windows
		reached  *rolloutLimits // the newest that reached a limit: later counts carry null again
	)
	next, err := scanRollout(f, cur.Offset, func(line rolloutLine, ev rolloutEvent) {
		switch {
		case ev.Type == "token_count":
			counted = true
			if ev.Info != nil {
				info = ev.Info
			}
			if ev.RateLimits != nil {
				limits = ev.RateLimits
				if ev.RateLimits.Reached != "" {
					reached = ev.RateLimits
				}
			}
		case ev.Error != nil && (ev.Type == "task_complete" || ev.Type == "turn_complete" || ev.Type == "turn_aborted"):
			failures = append(failures, codexFailure(req, line, ev))
		}
	})
	if err != nil {
		return Usage{}, err
	}
	nextCur := codexCursor{Offset: next, Total: cur.Total}
	u := Usage{}
	if counted {
		u = codexUsage(info, limits, reached, cur.Total)
		if info != nil {
			nextCur.Total = info.Total
		}
	}
	b, _ := json.Marshal(nextCur) // plain numbers always encode
	u.Cursor, u.Failures = string(b), failures
	if !counted {
		return u, fmt.Errorf("codex usage of %s: no token_count since the last reading: %w", req.AgentID, ErrNoUsage)
	}
	return u, nil
}

func codexUsage(info *rolloutInfo, limits, reached *rolloutLimits, before rolloutTokens) Usage {
	u := Usage{}
	if info != nil {
		d := info.Total.minus(before)
		// Codex counts the cached input inside the input, as the OpenAI API
		// does.
		u.InputTokens, u.CacheReadTokens, u.CacheWriteTokens = d.Input, d.CachedIn, d.CacheWrite
		u.OutputTokens, u.ReasoningTokens = d.Output, d.Reasoning
		if w := info.ContextWindow; w != nil && *w > 0 {
			u.ContextPct = float64(info.Last.Total) * 100 / float64(*w)
		}
	}
	if limits != nil {
		u.LimitWindows = limitWindows(limits)
	}
	if reached != nil {
		u.LimitsReached = true
		u.LimitsResetAt = resetOfReached(limitWindows(reached))
	}
	return u
}

func limitWindows(rl *rolloutLimits) []LimitWindow {
	var out []LimitWindow
	for _, w := range []*rolloutWindow{rl.Primary, rl.Secondary} {
		if w == nil {
			continue
		}
		lw := LimitWindow{Window: windowName(w.WindowMinutes), UsedPct: w.UsedPercent}
		if w.ResetsAt != nil {
			lw.ResetsAt = time.Unix(*w.ResetsAt, 0).UTC()
		}
		out = append(out, lw)
	}
	return out
}

// windowName names a window by its length: "300m" is "5h", 10080 is "7d".
func windowName(minutes *int64) string {
	if minutes == nil || *minutes <= 0 {
		return ""
	}
	m := *minutes
	switch {
	case m%1440 == 0:
		return strconv.FormatInt(m/1440, 10) + "d"
	case m%60 == 0:
		return strconv.FormatInt(m/60, 10) + "h"
	}
	return strconv.FormatInt(m, 10) + "m"
}

// resetOfReached is when the quota comes back: the reset of the full
// window, or the latest reset when none says 100%.
func resetOfReached(ws []LimitWindow) time.Time {
	var full, latest time.Time
	for _, w := range ws {
		if w.UsedPct >= 100 && w.ResetsAt.After(full) {
			full = w.ResetsAt
		}
		if w.ResetsAt.After(latest) {
			latest = w.ResetsAt
		}
	}
	if !full.IsZero() {
		return full
	}
	return latest
}

func codexFailure(req UsageRequest, line rolloutLine, ev rolloutEvent) Event {
	reason, detail := codexStopReason(ev.Error)
	at := req.Last.ReceivedAt
	if t, err := time.Parse(time.RFC3339Nano, line.Timestamp); err == nil {
		at = t
	}
	return Event{
		Version: EventVersion, Provider: Codex, NativeEvent: ev.Type, Event: EventTurnFailure,
		AgentID: req.Last.AgentID, TableID: req.Last.TableID, SessionID: req.Last.SessionID, TurnID: ev.TurnID,
		Cwd: req.Last.Cwd, TranscriptPath: req.Last.TranscriptPath, Model: req.Last.Model,
		StopReason: reason, Error: truncate(detail), ReceivedAt: at,
	}
}

// codexStopReason maps codex_error_info (a name, or an object named by its
// only key) to the stop reason.
func codexStopReason(e *rolloutTurnError) (StopReason, string) {
	var name string
	if json.Unmarshal(e.Info, &name) != nil {
		var m map[string]json.RawMessage
		if json.Unmarshal(e.Info, &m) == nil {
			for k := range m {
				name = k
			}
		}
	}
	detail := joinNonEmpty(name, e.Message)
	switch name {
	case "usage_limit_exceeded", "rate_limit_exceeded":
		return StopRateLimit, detail
	case "unauthorized":
		return StopAuth, detail
	}
	return StopError, detail
}

// scanRollout reads the whole lines from r (which starts at offset) and
// calls fn with each event_msg; it returns the offset after the last whole
// line. A line still being written is left for the next reading. Only the
// lines that name an event of interest are decoded.
func scanRollout(r io.Reader, offset int64, fn func(rolloutLine, rolloutEvent)) (int64, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		b, err := br.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			return offset, nil // b, without its newline, is not whole yet
		}
		if err != nil {
			return offset, fmt.Errorf("codex transcript: %w", err)
		}
		offset += int64(len(b))
		if !bytes.Contains(b, []byte(`"event_msg"`)) {
			continue
		}
		var line rolloutLine
		var ev rolloutEvent
		if json.Unmarshal(b, &line) != nil || line.Type != "event_msg" || json.Unmarshal(line.Payload, &ev) != nil {
			continue // a line of another shape is not the Mestre's business
		}
		fn(line, ev)
	}
}

// codexTailBytes is how much of the end of the transcript the Stop hook
// reads: the events of the turn that just ended.
const codexTailBytes = 256 << 10

// CodexTurnFailure is the reading of the transcript at Stop that fills
// Options.CodexTurnFailure in `mestre hook`: a Stop whose turn has an error
// recorded in the end of the transcript is a turn_failure. Codex records the
// error of a turn when the turn completes, and today runs no Stop hook on a
// failed turn (the periodic ReadUsage finds those); the reading at Stop
// stays for a version that does. codexHome is the CODEX_HOME of the hook's
// environment; without it, no transcript is read.
func CodexTurnFailure(codexHome string) func(Event) (StopReason, string, bool) {
	return func(stop Event) (StopReason, string, bool) {
		if codexHome == "" || stop.TurnID == "" {
			return "", "", false
		}
		f, err := openCodexTranscript(codexHome, stop.TranscriptPath)
		if err != nil {
			return "", "", false
		}
		defer f.Close()
		var from int64
		if st, err := f.Stat(); err == nil && st.Size() > codexTailBytes {
			from = st.Size() - codexTailBytes
		}
		if _, err := f.Seek(from, io.SeekStart); err != nil {
			return "", "", false
		}
		r := bufio.NewReader(f)
		if from > 0 {
			_, _ = r.ReadBytes('\n') // a partial first line
		}
		var reason StopReason
		var detail string
		_, _ = scanRollout(r, from, func(_ rolloutLine, ev rolloutEvent) {
			if ev.Error != nil && ev.TurnID == stop.TurnID {
				reason, detail = codexStopReason(ev.Error)
			}
		})
		return reason, detail, reason != ""
	}
}

// openCodexTranscript opens a transcript only inside <profile>/sessions,
// named rollout-*.jsonl, a regular file reached through no link nor
// junction (internal/safefile). The path
// comes from a hook payload, which a process of the agent account could
// write: nothing else of the disk (auth.json of the profile included) is
// ever read through it.
func openCodexTranscript(profile, path string) (*os.File, error) {
	if profile == "" || path == "" || !filepath.IsAbs(path) || !filepath.IsAbs(profile) {
		return nil, fmt.Errorf("codex transcript %q: not an absolute path inside the profile", path)
	}
	sessions := filepath.Join(filepath.Clean(profile), "sessions")
	rel, err := filepath.Rel(sessions, filepath.Clean(path))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return nil, fmt.Errorf("codex transcript %q: outside %s", path, sessions)
	}
	base := filepath.Base(rel)
	if !strings.HasPrefix(base, "rollout-") || !strings.HasSuffix(base, ".jsonl") {
		return nil, fmt.Errorf("codex transcript %q: not a rollout-*.jsonl", path)
	}
	// No link or junction on the way, and a regular file, checked on the
	// open file: the folders under sessions are the agent account's.
	f, _, err := safefile.Open(filepath.Join(sessions, rel))
	if err != nil {
		return nil, fmt.Errorf("codex transcript %q: %w", path, err)
	}
	return f, nil
}
