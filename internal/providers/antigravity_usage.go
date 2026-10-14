package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

// agyUsageTimeout bounds `agy -p "/usage"`: it calls no model, so a long wait
// means something is wrong.
const agyUsageTimeout = 30 * time.Second

// agyUsage is the output of `agy -p "/usage" --output-format json`
// (docs/provedores/antigravity.md#uso-custo-e-rate-limit).
type agyUsage struct {
	Command struct {
		Data struct {
			Groups []struct {
				Buckets []struct {
					ID                string          `json:"id"`
					Window            string          `json:"window"`
					RemainingFraction *float64        `json:"remaining_fraction"`
					ResetTime         json.RawMessage `json:"reset_time"`
				} `json:"buckets"`
			} `json:"groups"`
		} `json:"data"`
	} `json:"command"`
}

// ReadUsage normalizes the usage of the account profile: the windows of the
// JSON of /usage the engine kept (req.Payload), or else the output of `agy -p
// "/usage" --output-format json`, run with the HOME of the profile (it calls
// no model and spends no tokens). The use of a window, in percent, is 1 −
// remaining_fraction. The token of the login is never read here: agy reads it
// from the profile. Tokens per turn are not read in interactive mode, so the
// record has only the windows; LimitsReached is a window with nothing left,
// which together with a Stop with terminationReason error is the quota block
// of docs/plataforma/limites-de-uso.md.
func (a *antigravity) ReadUsage(req UsageRequest) (Usage, error) {
	if len(req.Payload) > 0 {
		return ParseAntigravityUsage(req.Payload)
	}
	home, _ := envValue(req.Env, "HOME")
	if home == "" || !absolute(home) {
		return Usage{}, fmt.Errorf("antigravity usage: the HOME of the account profile is required (got %q)", home)
	}
	if a.exec == nil {
		return Usage{}, fmt.Errorf("antigravity usage: %w", ErrNoExec)
	}
	env := make([]string, 0, len(req.Env)+1)
	for _, kv := range req.Env {
		// The command must not look like a character: no hook answers for it.
		if !strings.HasPrefix(kv, "MESTRE_AGENT_ID=") {
			env = append(env, kv)
		}
	}
	if req.OS == "windows" {
		env = append(env, "USERPROFILE="+home)
	}
	path, dir := req.Command, req.Dir
	if path == "" {
		path = "agy"
	}
	if dir == "" {
		dir = home
	}
	ctx, cancel := clock.WithTimeout(context.Background(), a.clk, agyUsageTimeout)
	defer cancel()
	out, err := a.exec(ctx, Command{Path: path, Args: []string{"-p", "/usage", "--output-format", "json"}, Env: env, Dir: dir})
	if err != nil {
		return Usage{}, fmt.Errorf("antigravity usage: %s -p /usage failed: %w", path, err)
	}
	return ParseAntigravityUsage(out)
}

// ParseAntigravityUsage normalizes the JSON of /usage.
func ParseAntigravityUsage(raw []byte) (Usage, error) {
	var in agyUsage
	if err := json.Unmarshal(raw, &in); err != nil {
		return Usage{}, fmt.Errorf("antigravity usage: the output is not the JSON of /usage: %w", err)
	}
	var u Usage
	for _, g := range in.Command.Data.Groups {
		for _, b := range g.Buckets {
			if b.RemainingFraction == nil {
				return Usage{}, fmt.Errorf("antigravity usage: window %q (%s) has no remaining_fraction", b.Window, b.ID)
			}
			left := math.Min(math.Max(*b.RemainingFraction, 0), 1)
			resets, err := resetTime(b.ResetTime)
			if err != nil {
				return Usage{}, fmt.Errorf("antigravity usage: window %q (%s): %w", b.Window, b.ID, err)
			}
			u.LimitWindows = append(u.LimitWindows, LimitWindow{
				Window: b.Window, Bucket: b.ID,
				UsedPct:  math.Round((1-left)*100*1e4) / 1e4,
				ResetsAt: resets,
			})
			u.LimitsReached = u.LimitsReached || left <= 0
		}
	}
	if len(u.LimitWindows) == 0 {
		return Usage{}, fmt.Errorf("antigravity usage: /usage has no windows (command.data.groups[].buckets[])")
	}
	return u, nil
}

// resetTime reads reset_time: a RFC 3339 text, or a number of seconds since
// the epoch; absent is the zero time.
func resetTime(raw json.RawMessage) (time.Time, error) {
	s := strings.TrimSpace(string(raw))
	switch {
	case s == "" || s == "null":
		return time.Time{}, nil
	case strings.HasPrefix(s, `"`):
		var txt string
		if err := json.Unmarshal(raw, &txt); err != nil {
			return time.Time{}, err
		}
		t, err := time.Parse(time.RFC3339, txt)
		if err != nil {
			return time.Time{}, fmt.Errorf("reset_time %q is not RFC 3339", txt)
		}
		return t.UTC(), nil
	}
	secs, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("reset_time %s is neither a text nor a number", s)
	}
	return time.Unix(int64(secs), 0).UTC(), nil
}
