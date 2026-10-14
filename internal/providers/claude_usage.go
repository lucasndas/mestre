package providers

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Names of the limit windows of Claude Code in the usage record.
const (
	WindowFiveHour = "5h"
	WindowSevenDay = "7d"
)

// claudeUsage reads the JSON of the statusLine
// (docs/provedores/claude-code.md#uso-custo-e-rate-limit): the cost
// (estimated at list price, zeroed by /clear), the tokens and the percentage
// of the context, and the windows of the limit of 5 h and of 7 days, which
// only the Pro and Max plans have and only after the first answer. The
// payload is read, never copied: nothing of it but the numbers is kept.
func claudeUsage(m map[string]any) (*Usage, error) {
	cost, hasCost := num(obj(m, "cost"), "total_cost_usd")
	ctx := obj(m, "context_window")
	pct, hasCtx := num(ctx, "used_percentage")
	in, _ := num(ctx, "total_input_tokens")
	out, _ := num(ctx, "total_output_tokens")
	u := &Usage{
		InputTokens: int64(in), OutputTokens: int64(out), CostUSD: cost, ContextPct: pct,
	}
	rl := obj(m, "rate_limits")
	hasWindow := false
	for _, w := range []struct{ key, name string }{{"five_hour", WindowFiveHour}, {"seven_day", WindowSevenDay}} {
		src := obj(rl, w.key)
		used, ok := num(src, "used_percentage")
		if !ok {
			continue
		}
		hasWindow = true
		win := LimitWindow{Window: w.name, UsedPct: used, ResetsAt: epoch(src["resets_at"])}
		u.LimitWindows = append(u.LimitWindows, win)
		u.LimitsReached = u.LimitsReached || used >= 100
	}
	if !hasCost && !hasCtx && !hasWindow {
		return nil, fmt.Errorf("claude-code statusLine: no cost, context or rate limit in the payload: %w", ErrNoUsage)
	}
	return u, nil
}

// num reads a number of a decoded payload (json.Number or float64).
func num(m map[string]any, key string) (float64, bool) {
	switch v := m[key].(type) {
	case float64:
		return v, true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	}
	return 0, false
}

// epoch reads a moment given in seconds since 1970 (as the statusLine does)
// or as RFC 3339; the zero time if it is neither.
func epoch(v any) time.Time {
	switch t := v.(type) {
	case float64:
		return time.Unix(int64(t), 0).UTC()
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return time.Unix(int64(f), 0).UTC()
		}
	case string:
		if at, err := time.Parse(time.RFC3339, t); err == nil {
			return at.UTC()
		}
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			return time.Unix(int64(f), 0).UTC()
		}
	}
	return time.Time{}
}

// ReadUsage: the usage record of the last statusLine of the character. The
// raw payload, when the engine kept it, is read; else the record the event
// already carries.
func (c *claudeCode) ReadUsage(req UsageRequest) (Usage, error) {
	if len(req.Payload) > 0 {
		m, err := decodePayload(ClaudeCode, HookInput{NativeEvent: "StatusLine", Stdin: req.Payload})
		if err != nil {
			return Usage{}, err
		}
		u, err := claudeUsage(m)
		if err != nil {
			return Usage{}, err
		}
		return *u, nil
	}
	if req.Last.Usage == nil {
		return Usage{}, fmt.Errorf("claude-code usage of %s: the last event is %q, not a statusLine: %w", req.AgentID, req.Last.NativeEvent, ErrNoUsage)
	}
	u := *req.Last.Usage
	u.LimitWindows = append([]LimitWindow(nil), u.LimitWindows...)
	return u, nil
}
