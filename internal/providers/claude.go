package providers

import (
	"fmt"
	"regexp"
	"slices"
)

// claudeCode is the Claude Code adapter (docs/provedores/claude-code.md).
type claudeCode struct {
	paths   pathNormalizer
	screens screenSet
}

// NewClaudeCode returns the Claude Code adapter.
func NewClaudeCode(opts Options) Adapter {
	return &claudeCode{paths: opts.paths(), screens: screenSet{
		prompt: agentPrompt,
		busy:   regexp.MustCompile(`(?i)esc to interrupt|Working…`),
		known: []KnownScreen{
			{ID: "trust", Pattern: regexp.MustCompile(`Do you trust the files in this folder\?`), Action: ActionAvoid, Checklist: "ALL-12"},
			{ID: "login", Pattern: regexp.MustCompile(`Select login method:`), Action: ActionAlert, Checklist: "ALL-11"},
		},
	}}
}

// agentPrompt is the input line of the agent CLIs (not the ❯ that marks the
// selected option of a menu).
var agentPrompt = regexp.MustCompile(`^\s*[│|]?\s*[>›](\s|$)`)

func (c *claudeCode) Info() Info { return Info{ID: ClaudeCode, Hooks: true} }

var claudeEvents = map[string]EventKind{
	"SessionStart":       EventSessionStart,
	"UserPromptSubmit":   EventTurnStart,
	"PreToolUse":         EventPreTool,
	"PostToolUse":        EventPostTool,
	"PostToolUseFailure": EventPostTool,
	"PermissionRequest":  EventPermissionRequest,
	"Stop":               EventTurnEnd,
	"StopFailure":        EventTurnFailure,
	"PreCompact":         EventPreCompact,
	"SessionEnd":         EventSessionEnd,
	"Notification":       EventNotification,
	"StatusLine":         EventUsage, // pseudo-event: the statusLine command
}

var claudeTools = toolTable{
	names: map[string]ToolKind{
		"Bash": KindShell, "PowerShell": KindShell,
		"Read":  KindRead,
		"Write": KindWrite,
		"Edit":  KindEdit, "NotebookEdit": KindEdit,
		"Glob": KindSearch, "Grep": KindSearch,
		"WebFetch": KindWeb, "WebSearch": KindWeb,
		"Agent":     KindSubagent,
		"TodoWrite": KindInternal,
	},
	prefixes: []struct {
		prefix string
		kind   ToolKind
	}{{"mcp__", KindMCP}},
}

// BuildLaunch: `claude <base args> --session-id <uuid>`, or `--resume
// <uuid>` with the saved id.
func (c *claudeCode) BuildLaunch(req LaunchRequest) (Launch, error) {
	l, err := newLaunch(req)
	if err != nil {
		return Launch{}, err
	}
	if err := refuseLatest(ClaudeCode, slices.Concat(req.Base.Args, req.ProviderArgs), "--continue", "-c"); err != nil {
		return Launch{}, err
	}
	l.Args = slices.Clone(req.Base.Args)
	switch {
	case req.ResumeSessionID != "":
		l.Args = append(l.Args, "--resume", req.ResumeSessionID)
		l.SessionID, l.Resumed = req.ResumeSessionID, true
	case req.NewSessionID != "":
		l.Args = append(l.Args, "--session-id", req.NewSessionID)
		l.SessionID = req.NewSessionID
	default:
		return Launch{}, fmt.Errorf("claude-code: the engine generates the session id (--session-id); none given")
	}
	l.Args = append(l.Args, req.ProviderArgs...)
	return l, nil
}

func (c *claudeCode) NormalizeHook(in HookInput) (Event, error) {
	kind, ok := claudeEvents[in.NativeEvent]
	if !ok {
		return Event{}, fmt.Errorf("claude-code %q: %w", in.NativeEvent, ErrUnknownEvent)
	}
	m, err := decodePayload(ClaudeCode, in)
	if err != nil {
		return Event{}, err
	}
	ev := in.event(ClaudeCode, kind)
	ev.SessionID, ev.TurnID = str(m, "session_id"), str(m, "prompt_id")
	ev.Cwd = c.paths.normalize(str(m, "cwd"), "")
	ev.TranscriptPath = str(m, "transcript_path")
	ev.Model = str(m, "model")
	if mo := obj(m, "model"); mo != nil { // statusLine: {id, display_name}
		ev.Model = str(mo, "id")
	}
	if kind.ToolEvent() {
		ev.Tool = c.tool(m, ev.Cwd)
	}
	switch in.NativeEvent {
	case "UserPromptSubmit":
		ev.Prompt = truncate(str(m, "prompt"))
	case "PostToolUse":
		ev.ToolResult = &ToolResult{OK: true, Output: truncate(text(m["tool_response"]))}
	case "PostToolUseFailure":
		ev.ToolResult = &ToolResult{Error: truncate(str(m, "error")), Output: truncate(text(m["tool_response"]))}
	case "Stop":
		ev.LastMessage = truncate(str(m, "last_assistant_message"))
		ev.StopReason = StopNormal
	case "StopFailure":
		ev.StopReason = claudeStopReason(str(m, "error"))
		ev.Error = truncate(joinNonEmpty(str(m, "error"), text(m["error_details"])))
	}
	return ev, nil
}

// claudeStopReason maps the error of StopFailure.
func claudeStopReason(e string) StopReason {
	switch e {
	case "rate_limit":
		return StopRateLimit
	case "authentication_failed":
		return StopAuth
	}
	return StopError
}

func (c *claudeCode) tool(m map[string]any, cwd string) *Tool {
	name := str(m, "tool_name")
	in := obj(m, "tool_input")
	kind := claudeTools.kind(name)
	var f toolFields
	switch kind {
	case KindShell:
		f.command = str(in, "command")
	case KindRead, KindWrite, KindEdit:
		f.paths = c.paths.all(cwd, str(in, "file_path"), str(in, "notebook_path"))
	case KindSearch: // Glob and Grep search the folder of `path`, or cwd
		p := str(in, "path")
		if p == "" {
			p = cwd
		}
		f.paths = c.paths.all(cwd, p)
	case KindWeb:
		f.url = str(in, "url")
	}
	return finishTool(name, kind, f, m["tool_input"])
}

func (c *claudeCode) Screens(in ScreenInput) ScreenResult { return c.screens.evaluate(in) }

// TypeMessage: bracketed paste and \r
// (docs/provedores/claude-code.md#digitação).
func (c *claudeCode) TypeMessage(text string) ([]byte, error) { return pasteAndSend(text, "\r") }

func joinNonEmpty(parts ...string) string {
	var out string
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out != "" {
			out += ": "
		}
		out += p
	}
	return out
}
