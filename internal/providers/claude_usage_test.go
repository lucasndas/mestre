package providers_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/providers"
)

// CA-CLAUDE-04
func TestReadUsageGivesCostContextAndTheTwoWindows(t *testing.T) {
	t.Parallel()
	claude := providers.NewClaudeCode(providers.Options{})
	payload := recorded(t, "claude-code", "StatusLine") // the JSON of the statusLine, with rate_limits
	want := providers.Usage{
		InputTokens: 15234, OutputTokens: 4521, CostUSD: 0.01234, ContextPct: 8,
		LimitWindows: []providers.LimitWindow{
			{Window: providers.WindowFiveHour, UsedPct: 23.5, ResetsAt: time.Unix(1791828000, 0).UTC()},
			{Window: providers.WindowSevenDay, UsedPct: 41.2, ResetsAt: time.Unix(1792195200, 0).UTC()},
		},
	}

	// From the raw JSON the engine kept.
	got, err := claude.ReadUsage(providers.UsageRequest{AgentID: "a1", Payload: payload})
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("ReadUsage(payload) = %+v, %v\nwant %+v", got, err, want)
	}
	// From the event `mestre hook --event StatusLine` normalized.
	ev, err := claude.NormalizeHook(providers.HookInput{NativeEvent: "StatusLine", Stdin: payload, AgentID: "a1", ReceivedAt: at})
	if err != nil || ev.Event != providers.EventUsage {
		t.Fatalf("NormalizeHook = %+v, %v", ev, err)
	}
	got, err = claude.ReadUsage(providers.UsageRequest{AgentID: "a1", Last: ev})
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("ReadUsage(event) = %+v, %v\nwant %+v", got, err, want)
	}
	// The record is a copy: changing it does not change the event.
	got.LimitWindows[0].UsedPct = 99
	if ev.Usage.LimitWindows[0].UsedPct != 23.5 {
		t.Error("ReadUsage shares the windows of the event")
	}
}

func TestReadUsageWithoutLimitsOrWithNothing(t *testing.T) {
	t.Parallel()
	claude := providers.NewClaudeCode(providers.Options{})
	read := func(payload string) (providers.Usage, error) {
		return claude.ReadUsage(providers.UsageRequest{AgentID: "a1", Payload: []byte(payload)})
	}
	// Plans without the limits, or before the first answer: no windows.
	u, err := read(`{"cost": {"total_cost_usd": 0}, "context_window": {"used_percentage": 0}}`)
	if err != nil || len(u.LimitWindows) != 0 || u.LimitsReached {
		t.Errorf("no rate_limits = %+v, %v", u, err)
	}
	// One window only, reset given as RFC 3339; 100% is the limit reached.
	u, err = read(`{"cost": {"total_cost_usd": 1.5}, "rate_limits": {"five_hour": {"used_percentage": 100, "resets_at": "2026-10-14T18:00:00Z"}}}`)
	if want := []providers.LimitWindow{{Window: "5h", UsedPct: 100, ResetsAt: time.Date(2026, 10, 14, 18, 0, 0, 0, time.UTC)}}; err != nil ||
		!reflect.DeepEqual(u.LimitWindows, want) || !u.LimitsReached || u.CostUSD != 1.5 {
		t.Errorf("one window = %+v, %v", u, err)
	}
	for _, empty := range []string{`{}`, `{"model": {"id": "x"}}`, `{"rate_limits": {}}`} {
		if _, err := read(empty); !errors.Is(err, providers.ErrNoUsage) {
			t.Errorf("%s: err = %v, want ErrNoUsage", empty, err)
		}
	}
	if _, err := read(`not json`); err == nil {
		t.Error("a payload that is not JSON")
	}
	// An event that is not a statusLine has nothing to read.
	stop := providers.Event{Event: providers.EventTurnEnd, NativeEvent: "Stop"}
	if _, err := claude.ReadUsage(providers.UsageRequest{AgentID: "a1", Last: stop}); !errors.Is(err, providers.ErrNoUsage) {
		t.Errorf("Stop: err = %v, want ErrNoUsage", err)
	}
	// The statusLine of a session that has nothing yet is an event without usage.
	ev, err := claude.NormalizeHook(providers.HookInput{NativeEvent: "StatusLine", Stdin: []byte(`{"session_id": "s1", "cwd": "/w"}`), ReceivedAt: at})
	if err != nil || ev.Event != providers.EventUsage || ev.Usage != nil {
		t.Errorf("empty statusLine = %+v, %v", ev, err)
	}
}
