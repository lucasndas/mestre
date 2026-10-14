package providers

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// codex is the Codex adapter (docs/provedores/codex.md).
type codex struct {
	paths   pathNormalizer
	failure func(Event) (StopReason, string, bool)
	screens screenSet
}

// NewCodex returns the Codex adapter.
func NewCodex(opts Options) Adapter {
	return &codex{paths: opts.paths(), failure: opts.CodexTurnFailure, screens: screenSet{
		prompt: agentPrompt,
		busy:   regexp.MustCompile(`(?i)esc to interrupt|Working…`),
		known: []KnownScreen{
			{ID: "trust", Pattern: regexp.MustCompile(`Do you trust the contents of this directory\?`), Action: ActionAnswer,
				Answers: map[string][]byte{"trust": menuFirst}},
			{ID: "hook-review", Pattern: regexp.MustCompile(`Hooks need review|Trust all and continue`), Action: ActionEscalate},
			{ID: "login", Pattern: regexp.MustCompile(`Sign in with ChatGPT`), Action: ActionAlert, Checklist: "ALL-11"},
		},
	}}
}

func (c *codex) Info() Info { return Info{ID: Codex, Hooks: true} }

var codexEvents = map[string]EventKind{
	"SessionStart":      EventSessionStart,
	"UserPromptSubmit":  EventTurnStart,
	"PreToolUse":        EventPreTool,
	"PostToolUse":       EventPostTool,
	"PermissionRequest": EventPermissionRequest,
	"Stop":              EventTurnEnd, // turn_failure comes from the transcript
	"Interrupt":         EventInterrupt,
	"PreCompact":        EventPreCompact,
	"SessionEnd":        EventSessionEnd,
}

var codexTools = toolTable{
	names: map[string]ToolKind{
		"Bash":        KindShell,
		"apply_patch": KindEdit, // KindWrite when it only adds files
		"update_plan": KindInternal,
	},
	prefixes: []struct {
		prefix string
		kind   ToolKind
	}{{"mcp__", KindMCP}},
}

// BuildLaunch: `codex <base args>`, or `codex resume <SESSION_ID> <base
// args>` with the same provider flags (-c) as the start.
func (c *codex) BuildLaunch(req LaunchRequest) (Launch, error) {
	l, err := newLaunch(req)
	if err != nil {
		return Launch{}, err
	}
	if err := refuseLatest(Codex, slices.Concat(req.Base.Args, req.ProviderArgs), "--last"); err != nil {
		return Launch{}, err
	}
	if req.ResumeSessionID != "" {
		l.Args = []string{"resume", req.ResumeSessionID}
		l.SessionID, l.Resumed = req.ResumeSessionID, true
	}
	l.Args = slices.Concat(l.Args, req.Base.Args, req.ProviderArgs)
	return l, nil
}

func (c *codex) NormalizeHook(in HookInput) (Event, error) {
	kind, ok := codexEvents[in.NativeEvent]
	if !ok {
		return Event{}, fmt.Errorf("codex %q: %w", in.NativeEvent, ErrUnknownEvent)
	}
	m, err := decodePayload(Codex, in)
	if err != nil {
		return Event{}, err
	}
	ev := in.event(Codex, kind)
	ev.SessionID, ev.TurnID = str(m, "session_id"), str(m, "turn_id")
	ev.Cwd = c.paths.normalize(str(m, "cwd"), "")
	ev.TranscriptPath, ev.Model = str(m, "transcript_path"), str(m, "model")
	if kind.ToolEvent() {
		ev.Tool = c.tool(m, ev.Cwd)
	}
	switch kind {
	case EventTurnStart:
		ev.Prompt = truncate(str(m, "prompt"))
	case EventPostTool:
		ev.ToolResult = &ToolResult{OK: true, Output: truncate(text(m["tool_response"]))}
	case EventInterrupt:
		ev.StopReason = StopInterrupted
	case EventTurnEnd:
		ev.LastMessage = truncate(str(m, "last_assistant_message"))
		ev.StopReason = StopNormal
		if c.failure != nil {
			if reason, detail, failed := c.failure(ev); failed {
				ev.Event, ev.StopReason, ev.Error = EventTurnFailure, reason, truncate(detail)
			}
		}
	}
	return ev, nil
}

func (c *codex) tool(m map[string]any, cwd string) *Tool {
	name := str(m, "tool_name")
	kind := codexTools.kind(name)
	var f toolFields
	switch kind {
	case KindShell:
		f.command = str(obj(m, "tool_input"), "command")
	case KindEdit:
		var added bool
		f.paths, added = patchPaths(patchText(m["tool_input"]))
		f.paths = c.paths.all(cwd, f.paths...)
		if added {
			kind = KindWrite
		}
	}
	return finishTool(name, kind, f, m["tool_input"])
}

// patchText finds the patch of an apply_patch call: the input itself or
// its command, input or patch field.
func patchText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	m, _ := v.(map[string]any)
	for _, k := range []string{"command", "input", "patch"} {
		if s := str(m, k); s != "" {
			return s
		}
	}
	return ""
}

var patchHeader = regexp.MustCompile(`(?m)^\*\*\* (Add|Update|Delete) File: (.+?)\s*$|^\*\*\* Move to: (.+?)\s*$`)

// patchPaths reads the file headers of a patch; onlyAdds is true when every
// header adds a new file.
func patchPaths(patch string) (paths []string, onlyAdds bool) {
	onlyAdds = true
	for _, h := range patchHeader.FindAllStringSubmatch(patch, -1) {
		p := h[2]
		if p == "" {
			p = h[3]
		}
		if h[1] != "Add" {
			onlyAdds = false
		}
		paths = append(paths, strings.TrimSpace(p))
	}
	return paths, onlyAdds && len(paths) > 0
}

func (c *codex) Screens(in ScreenInput) ScreenResult { return c.screens.evaluate(in) }

// TypeMessage: bracketed paste and \r (docs/provedores/codex.md#digitação).
func (c *codex) TypeMessage(text string) ([]byte, error) { return pasteAndSend(text, "\r") }
