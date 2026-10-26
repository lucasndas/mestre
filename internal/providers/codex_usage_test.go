package providers_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/testkit"
)

// writeRollout appends the recorded transcript lines [from, to) to a
// rollout file inside the profile, as Codex appends them.
func writeRollout(t *testing.T, path string, from, to int) {
	t.Helper()
	var lines []json.RawMessage
	if err := json.Unmarshal(recorded(t, "codex", "rollout"), &lines); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines[from:to] {
		var compact bytes.Buffer
		if err := json.Compact(&compact, l); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(append(compact.Bytes(), '\n')); err != nil {
			t.Fatal(err)
		}
	}
}

func rolloutPath(profile string) string {
	return filepath.Join(profile, "sessions", "2026", "10", "14", "rollout-2026-10-14T12-00-00-019a0000-0000-7000-8000-000000000001.jsonl")
}

func window(t *testing.T, u providers.Usage, name string) providers.LimitWindow {
	t.Helper()
	for _, w := range u.LimitWindows {
		if w.Window == name {
			return w
		}
	}
	t.Fatalf("no %s window in %+v", name, u.LimitWindows)
	return providers.LimitWindow{}
}

// CA-CODEX-04
func TestReadUsageHasTheTokensTheTwoWindowsAndTheLimitReached(t *testing.T) {
	t.Parallel()
	a := codexAdapter(providers.Options{})
	profile := t.TempDir()
	path := rolloutPath(profile)
	last := providers.Event{Provider: providers.Codex, Event: providers.EventTurnEnd, SessionID: "019a0000-0000-7000-8000-000000000001", TranscriptPath: path}

	// The first turn: below the limits.
	writeRollout(t, path, 0, 3)
	r, err := a.ReadUsage(providers.UsageRequest{AgentID: "a1", Profile: profile, Last: last})
	if err != nil {
		t.Fatal(err)
	}
	u := r
	if u.InputTokens != 12000 || u.CacheReadTokens != 8000 || u.CacheWriteTokens != 1500 || u.OutputTokens != 900 || u.ReasoningTokens != 400 {
		t.Fatalf("usage = %+v, want the tokens of the token_count", u)
	}
	if len(u.LimitWindows) != 2 || u.LimitsReached {
		t.Fatalf("usage = %+v, want the two windows and no limit reached", u)
	}
	if w := window(t, u, "5h"); w.UsedPct != 42.5 || !w.ResetsAt.Equal(time.Unix(1792000800, 0)) {
		t.Fatalf("primary = %+v", w)
	}
	if w := window(t, u, "7d"); w.UsedPct != 12 || !w.ResetsAt.Equal(time.Unix(1792512000, 0)) {
		t.Fatalf("secondary = %+v", w)
	}
	if len(r.Failures) != 0 {
		t.Fatalf("failures = %+v", r.Failures)
	}

	// The second turn ends at the usage limit: rate_limit_reached_type filled.
	writeRollout(t, path, 3, 6)
	r, err = a.ReadUsage(providers.UsageRequest{AgentID: "a1", Profile: profile, Last: last, Cursor: r.Cursor})
	if err != nil {
		t.Fatal(err)
	}
	u = r
	if u.InputTokens != 8000 || u.OutputTokens != 100 || u.CacheReadTokens != 7000 {
		t.Fatalf("usage = %+v, want only the tokens since the previous reading", u)
	}
	if !u.LimitsReached || !u.LimitsResetAt.Equal(time.Unix(1792000800, 0)) || len(u.LimitWindows) != 2 || window(t, u, "5h").UsedPct != 100 {
		t.Fatalf("usage = %+v, want the limit reached until the primary window resets", u)
	}
	if len(r.Failures) != 1 || r.Failures[0].Event != providers.EventTurnFailure || r.Failures[0].TurnID != "t2" || r.Failures[0].StopReason != providers.StopRateLimit {
		t.Fatalf("failures = %+v, want the failed turn t2 (no Stop hook runs on it)", r.Failures)
	}

	// Nothing new: no usage, the same cursor.
	again, err := a.ReadUsage(providers.UsageRequest{Profile: profile, Last: last, Cursor: r.Cursor})
	if !errors.Is(err, providers.ErrNoUsage) || again.Cursor != r.Cursor || len(again.Failures) != 0 {
		t.Fatalf("again = %+v, %v", again, err)
	}
}

func TestReadUsageReadsOnlyTheTranscriptsOfTheProfile(t *testing.T) {
	t.Parallel()
	a := codexAdapter(providers.Options{})
	profile := t.TempDir()
	testkit.WriteFiles(t, profile, map[string]string{"auth.json": `{"tokens": "x"}`, "sessions/rollout-x.jsonl": "", "other/rollout-y.jsonl": ""})
	for _, p := range []string{
		filepath.Join(profile, "auth.json"),
		filepath.Join(profile, "other", "rollout-y.jsonl"),
		filepath.Join(profile, "sessions", "..", "auth.json"),
		filepath.Join(profile, "sessions", "notes.txt"),
		"sessions/rollout-x.jsonl",
		"",
	} {
		if _, err := a.ReadUsage(providers.UsageRequest{Profile: profile, Last: providers.Event{TranscriptPath: p}}); err == nil || errors.Is(err, providers.ErrNoUsage) {
			t.Errorf("%q was read", p)
		}
	}
	if _, err := a.ReadUsage(providers.UsageRequest{Profile: profile, Last: providers.Event{TranscriptPath: filepath.Join(profile, "sessions", "rollout-x.jsonl")}}); !errors.Is(err, providers.ErrNoUsage) {
		t.Errorf("the profile's transcript: %v", err)
	}
	if runtime.GOOS != "windows" { // creating a link takes a privilege there
		link := filepath.Join(profile, "sessions", "rollout-link.jsonl")
		if err := os.Symlink(filepath.Join(profile, "auth.json"), link); err != nil {
			t.Fatal(err)
		}
		if _, err := a.ReadUsage(providers.UsageRequest{Profile: profile, Last: providers.Event{TranscriptPath: link}}); err == nil || errors.Is(err, providers.ErrNoUsage) {
			t.Error("a link to auth.json was read")
		}
	}
	// A folder on the way that leads elsewhere (M3 audit; the junction of
	// Windows is refused by internal/safefile, which has its own test of it).
	if runtime.GOOS != "windows" {
		testkit.WriteFiles(t, profile, map[string]string{"elsewhere/rollout-z.jsonl": ""})
		dir := filepath.Join(profile, "sessions", "2026")
		if err := os.Symlink(filepath.Join(profile, "elsewhere"), dir); err != nil {
			t.Fatal(err)
		}
		if _, err := a.ReadUsage(providers.UsageRequest{Profile: profile, Last: providers.Event{TranscriptPath: filepath.Join(dir, "rollout-z.jsonl")}}); err == nil || errors.Is(err, providers.ErrNoUsage) {
			t.Error("a transcript through a linked folder was read")
		}
	}
}

func TestCodexTurnFailureReadsTheTurnOfTheStop(t *testing.T) {
	t.Parallel()
	profile := t.TempDir()
	path := rolloutPath(profile)
	writeRollout(t, path, 0, 6)
	read := providers.CodexTurnFailure(profile)
	if reason, detail, failed := read(providers.Event{TurnID: "t2", TranscriptPath: path}); !failed || reason != providers.StopRateLimit || !strings.Contains(detail, "usage_limit_exceeded") {
		t.Fatalf("t2 = %v %q %v, want a rate_limit failure", reason, detail, failed)
	}
	if _, _, failed := read(providers.Event{TurnID: "t1", TranscriptPath: path}); failed {
		t.Fatal("t1 ended well")
	}
	if _, _, failed := providers.CodexTurnFailure("")(providers.Event{TurnID: "t2", TranscriptPath: path}); failed {
		t.Fatal("without CODEX_HOME no transcript is read")
	}
}
