package providers

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// codex is the Codex adapter (docs/provedores/codex.md).
type codex struct {
	paths     pathNormalizer
	failure   func(Event) (StopReason, string, bool)
	multiline bool // CodexMultilineViaFile
	noFlush   bool // NoFlush
	screens   screenSet
}

// NewCodex returns the Codex adapter.
func NewCodex(opts Options) Adapter {
	return &codex{paths: opts.paths(), failure: opts.CodexTurnFailure, multiline: opts.CodexMultilineViaFile, noFlush: opts.NoFlush, screens: screenSet{
		prompt:   agentPrompt,
		busy:     regexp.MustCompile(`(?i)esc to interrupt|Working…`),
		known:    codexScreens(),
		box:      codexBox,
		busyNear: regexp.MustCompile(`(?i)esc to interrupt`),
	}}
}

// codexBox is the input line of Codex (0.162.1, seen in the conformance
// test): › with the placeholder or the text typed, above the line of the
// model and the "? for shortcuts" footer. A menu option (› 1. …) is not it.
var codexBox = regexp.MustCompile(`(?m)^\s*›(?:[ \x{a0}]+(?:[^\d\s][^\n]*)?)?\s*$`)

// codexScreens is the table of
// docs/provedores/codex.md#telas-que-o-pty-precisa-tratar, with the texts of
// the Codex TUI.
func codexScreens() []KnownScreen {
	return []KnownScreen{
		// The login lists the ways in: a checklist alert.
		{ID: "login", Pattern: regexp.MustCompile(`Sign in with ChatGPT`), Action: ActionAlert, Checklist: "ALL-11"},
		// Pre-trust avoids it; in a worktree of the Mestre the engine's
		// decision answers "Trust and continue", the first option.
		{ID: "trust", Pattern: regexp.MustCompile(`Trust this folder\?`), Action: ActionAnswer, StartOnly: true,
			Answers: map[string][]byte{"trust": menuFirst}},
		// Trusting all could approve hooks of third parties that came with
		// the repository: escalate, and the checklist asks for the trust
		// of the profile (ALL-11).
		{ID: "hook-review", Pattern: regexp.MustCompile(`Hooks need review|Trust all and continue`), Action: ActionEscalate, Checklist: "ALL-11"},
		// The Mestre lets the CLI update itself: "Update now", the first
		// option.
		{ID: "update", Pattern: regexp.MustCompile(`(?s)Update now.*Skip until next version`), Action: ActionAnswer, StartOnly: true,
			Answers: map[string][]byte{"update": menuFirst}, Default: "update"},
		// The setup of the Windows sandbox: escalated the first time of a
		// profile.
		{ID: "windows-sandbox", Pattern: regexp.MustCompile(`Set up the Codex agent sandbox`), Action: ActionEscalate},
		// A native approval (the PermissionRequest hook failed): the
		// decision recorded for the call, or deny. y approves; Esc is
		// "No, and tell Codex what to do differently".
		{ID: "approval", Pattern: regexp.MustCompile(`Would you like to run the following command\?|Do you want to approve network access to`), Action: ActionAnswer,
			Option:  regexp.MustCompile(`^Yes\b`),
			Answers: map[string][]byte{"allow": []byte("y"), "deny": []byte("\x1b")}, Default: "deny"},
	}
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
		// The native subagents of Codex 0.162.1, named this way in the hook
		// (conformance test, 2026-10-14).
		"collaborationspawn_agent": KindSubagent,
		"collaborationwait_agent":  KindSubagent,
	},
	prefixes: []struct {
		prefix string
		kind   ToolKind
	}{{"mcp__", KindMCP}},
}

// BuildLaunch: `codex <base args> <provider args>`, or `codex resume
// <SESSION_ID>` with the same flags, plus the ones the base always has
// (codex_launch.go: the hooks switched on last), and CODEX_HOME of the
// account profile.
func (c *codex) BuildLaunch(req LaunchRequest) (Launch, error) {
	l, err := newLaunch(Codex, req)
	if err != nil {
		return Launch{}, err
	}
	if err := refuseLatest(Codex, slices.Concat(req.Base.Args, req.ProviderArgs), "--last"); err != nil {
		return Launch{}, err
	}
	flags, err := codexStartArgs(req.Base.Args, req.ProviderArgs, req.Model)
	if err != nil {
		return Launch{}, err
	}
	if l.Env, err = codexEnv(l.Env, req.Profile); err != nil {
		return Launch{}, err
	}
	if err := checkResumeID(Codex, req.ResumeSessionID); err != nil {
		return Launch{}, err
	}
	if req.ResumeSessionID != "" {
		l.Args = []string{"resume", req.ResumeSessionID}
		l.SessionID, l.Resumed = req.ResumeSessionID, true
	}
	l.Args = slices.Concat(l.Args, flags)
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
// With the fallback of the multi-line item, a text of several lines goes by
// the long-message file instead (ErrTooLong, as a long text).
func (c *codex) TypeMessage(text string) ([]byte, error) {
	if c.multiline && strings.Contains(strings.TrimRight(text, "\r\n"), "\n") {
		return nil, fmt.Errorf("%w: several lines go by the long-message file in this Codex version", ErrTooLong)
	}
	return pasteAndSend(text, "\r")
}
