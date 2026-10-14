package providers

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
	"unicode/utf8"
)

// EventVersion is the version of the internal event format.
const EventVersion = 1

// MaxText is how much of a tool output, prompt or last message an event
// keeps (16 KiB, docs/provedores/adaptador.md#formato-interno-normalizado-de-evento).
const MaxText = 16 << 10

// EventKind is an event of the Mestre (the map of events).
type EventKind string

// Events of the Mestre. EventNone is a native event the map does not use
// (Antigravity's PostInvocation only records the invocation).
const (
	EventNone              EventKind = ""
	EventSessionStart      EventKind = "session_start"
	EventTurnStart         EventKind = "turn_start"
	EventPreTool           EventKind = "pre_tool"
	EventPostTool          EventKind = "post_tool"
	EventPermissionRequest EventKind = "permission_request"
	EventTurnEnd           EventKind = "turn_end"
	EventTurnFailure       EventKind = "turn_failure"
	EventInterrupt         EventKind = "interrupt"
	EventPreCompact        EventKind = "pre_compact"
	EventSessionEnd        EventKind = "session_end"
	EventNotification      EventKind = "notification"
	EventUsage             EventKind = "usage"
)

// ToolEvent reports whether the event is about a tool call.
func (k EventKind) ToolEvent() bool {
	return k == EventPreTool || k == EventPostTool || k == EventPermissionRequest
}

// StopReason is why a turn ended.
type StopReason string

// Stop reasons.
const (
	StopNormal      StopReason = "normal"
	StopInterrupted StopReason = "interrupted"
	StopMaxSteps    StopReason = "max_steps"
	StopRateLimit   StopReason = "rate_limit"
	StopAuth        StopReason = "auth"
	StopError       StopReason = "error"
)

// Event is the internal normalized event.
type Event struct {
	Version        int         `json:"version"`
	Provider       ID          `json:"provider"`
	NativeEvent    string      `json:"native_event"`
	Event          EventKind   `json:"event"`
	AgentID        string      `json:"agent_id,omitempty"`
	TableID        string      `json:"table_id,omitempty"`
	SessionID      string      `json:"session_id,omitempty"`
	TurnID         string      `json:"turn_id,omitempty"`
	Cwd            string      `json:"cwd,omitempty"`
	TranscriptPath string      `json:"transcript_path,omitempty"`
	Model          string      `json:"model,omitempty"`
	Tool           *Tool       `json:"tool,omitempty"`
	ToolResult     *ToolResult `json:"tool_result,omitempty"`
	Prompt         string      `json:"prompt,omitempty"`
	LastMessage    string      `json:"last_message,omitempty"`
	StopReason     StopReason  `json:"stop_reason,omitempty"`
	Error          string      `json:"error,omitempty"`
	Usage          *Usage      `json:"usage,omitempty"`
	ReceivedAt     time.Time   `json:"received_at"`
	// Raw is the payload of the provider as the hook got it, only when the
	// character records its hooks (the conformance test, MESTRE_HOOK_RECORD):
	// the recordings of the fake agent come from it. Nothing decides on it.
	Raw json.RawMessage `json:"raw,omitempty"`
}

// Tool is the tool of a tool event.
type Tool struct {
	NativeName string          `json:"native_name"`
	Kind       ToolKind        `json:"kind"`
	Command    string          `json:"command,omitempty"`
	Paths      []string        `json:"paths,omitempty"`
	URL        string          `json:"url,omitempty"`
	RawInput   json.RawMessage `json:"raw_input,omitempty"`
}

// ToolResult is the outcome of a tool, only in post_tool.
type ToolResult struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	Output string `json:"output,omitempty"`
}

// Usage is the normalized usage record, only in usage events. ReadUsage
// fills it (provider documents and limites-de-uso.md).
type Usage struct {
	InputTokens   int64         `json:"input_tokens,omitempty"`
	OutputTokens  int64         `json:"output_tokens,omitempty"`
	CostUSD       float64       `json:"cost_usd,omitempty"`
	ContextPct    float64       `json:"context_pct,omitempty"`
	LimitWindows  []LimitWindow `json:"limit_windows,omitempty"`
	LimitsReached bool          `json:"limits_reached,omitempty"`
	// The rest of the tokens of limites-de-uso.md: cache read and written,
	// and reasoning.
	CacheReadTokens  int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64 `json:"cache_write_tokens,omitempty"`
	ReasoningTokens  int64 `json:"reasoning_tokens,omitempty"`
	// LimitsResetAt is when the quota comes back, with LimitsReached, when
	// the provider says.
	LimitsResetAt time.Time `json:"limits_reset_at,omitzero"`

	// Of a reading of a transcript (Codex), not of the record: Cursor goes
	// in the next UsageRequest of the session, and Failures are the
	// turn_failure events the reading found and no hook brought (Codex runs
	// no Stop hook on a turn that ends in error).
	Cursor   string  `json:"-"`
	Failures []Event `json:"-"`
}

// LimitWindow is one rate-limit window.
type LimitWindow struct {
	Window string `json:"window"`
	// Bucket tells apart windows of the same name that belong to different
	// model groups (Antigravity); empty where the provider has one of each.
	Bucket   string    `json:"bucket,omitempty"`
	UsedPct  float64   `json:"used_pct"`
	ResetsAt time.Time `json:"resets_at"`
}

// HookInput is one `mestre hook` call.
type HookInput struct {
	// NativeEvent is the --event of the command line (Antigravity payloads
	// do not name their event).
	NativeEvent string
	Stdin       []byte
	// AgentID and TableID come from MESTRE_AGENT_ID and MESTRE_MESA.
	AgentID, TableID string
	// ReceivedAt is the machine clock when the call came in.
	ReceivedAt time.Time
}

func (in HookInput) event(p ID, kind EventKind) Event {
	return Event{
		Version: EventVersion, Provider: p, NativeEvent: in.NativeEvent, Event: kind,
		AgentID: in.AgentID, TableID: in.TableID, ReceivedAt: in.ReceivedAt,
	}
}

// decodePayload reads the stdin JSON object.
func decodePayload(p ID, in HookInput) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(in.Stdin, &m); err != nil {
		return nil, fmt.Errorf("%s %s: payload is not a JSON object: %w", p, in.NativeEvent, err)
	}
	return m, nil
}

// truncate keeps at most MaxText bytes, cut at a rune boundary.
func truncate(s string) string {
	if len(s) <= MaxText {
		return s
	}
	cut := MaxText
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// Helpers that read loosely typed payload fields.

func str(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func obj(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}

func raw(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// text renders a tool response: a string as is, {stdout, stderr} joined,
// anything else as JSON.
func text(v any) string {
	switch r := v.(type) {
	case nil:
		return ""
	case string:
		return r
	case map[string]any:
		if _, ok := r["stdout"]; ok {
			return str(r, "stdout") + str(r, "stderr")
		}
	}
	return string(raw(v))
}
