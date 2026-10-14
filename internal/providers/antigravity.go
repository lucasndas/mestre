package providers

import (
	"fmt"
	"regexp"
	"slices"
)

// antigravity is the Antigravity CLI (`agy`) adapter
// (docs/provedores/antigravity.md). Its payloads are camelCase and do not
// name their event: the dispatcher passes the --event of the command line.
type antigravity struct {
	paths   pathNormalizer
	screens screenSet
}

// NewAntigravity returns the Antigravity adapter.
func NewAntigravity(opts Options) Adapter {
	return &antigravity{paths: opts.paths(), screens: screenSet{
		prompt: agentPrompt,
		busy:   regexp.MustCompile(`(?i)esc to (interrupt|cancel)|Working…`),
		known: []KnownScreen{
			{ID: "trust", Pattern: regexp.MustCompile(`Do you trust the contents of this project\?`), Action: ActionAnswer,
				Answers: map[string][]byte{"trust": menuFirst}},
			{ID: "login", Pattern: regexp.MustCompile(`Sign in with Google`), Action: ActionAlert, Checklist: "ALL-11"},
		},
	}}
}

func (a *antigravity) Info() Info {
	return Info{
		ID: Antigravity, Hooks: true, DerivedSessionStart: true,
		Reduced: "antigravity has only PreToolUse, PostToolUse, PreInvocation, PostInvocation and Stop: no permission_request, interrupt, pre_compact, session_end or notification events",
	}
}

var antigravityEvents = map[string]EventKind{
	"PreInvocation":  EventTurnStart, // the first one of a conversation is also session_start (Turns)
	"PreToolUse":     EventPreTool,
	"PostToolUse":    EventPostTool,
	"Stop":           EventTurnEnd,
	"PostInvocation": EventNone, // only records the invocation
}

// antigravityTools is the tool table. The path arguments of the file tools
// are not in the recorded payloads yet, so those tools come out as `other`
// (finishTool) until the conformance test records them.
var antigravityTools = toolTable{
	names: map[string]ToolKind{
		"run_command":          KindShell,
		"view_file":            KindRead,
		"write_to_file":        KindWrite,
		"replace_file_content": KindEdit, "multi_replace_file_content": KindEdit,
		"list_dir": KindSearch, "find_by_name": KindSearch, "grep_search": KindSearch,
		"search_web": KindWeb, "read_url_content": KindWeb,
		"invoke_subagent": KindSubagent, "define_subagent": KindSubagent, "send_message": KindSubagent, "manage_subagents": KindSubagent,
		"manage_task": KindInternal, "ask_question": KindInternal, "list_permissions": KindInternal, "ask_permission": KindInternal,
		"generate_image": KindOther, "schedule": KindOther,
	},
	prefixes: []struct {
		prefix string
		kind   ToolKind
	}{{"browser_", KindWeb}},
}

// antigravityPathArgs are the path arguments seen in recorded payloads, by
// tool. None yet (only run_command was captured).
var antigravityPathArgs = map[string][]string{}

// BuildLaunch: `agy <base args> --agent mestre-<id>`, with `--conversation
// <id>` before it on a resume.
func (a *antigravity) BuildLaunch(req LaunchRequest) (Launch, error) {
	l, err := newLaunch(req)
	if err != nil {
		return Launch{}, err
	}
	if req.AgentID == "" {
		return Launch{}, fmt.Errorf("antigravity: no character id for --agent mestre-<id>")
	}
	l.Args = slices.Clone(req.Base.Args)
	if req.ResumeSessionID != "" {
		l.Args = append(l.Args, "--conversation", req.ResumeSessionID)
		l.SessionID, l.Resumed = req.ResumeSessionID, true
	}
	l.Args = append(l.Args, "--agent", "mestre-"+req.AgentID)
	l.Args = append(l.Args, req.ProviderArgs...)
	return l, nil
}

func (a *antigravity) NormalizeHook(in HookInput) (Event, error) {
	kind, ok := antigravityEvents[in.NativeEvent]
	if !ok {
		return Event{}, fmt.Errorf("antigravity %q: %w", in.NativeEvent, ErrUnknownEvent)
	}
	m, err := decodePayload(Antigravity, in)
	if err != nil {
		return Event{}, err
	}
	ev := in.event(Antigravity, kind)
	ev.SessionID, ev.TurnID = str(m, "conversationId"), str(m, "executionNum")
	ev.TranscriptPath, ev.Model = str(m, "transcriptPath"), str(m, "modelName")
	call := obj(m, "toolCall")
	args := obj(call, "args")
	cwd := str(args, "Cwd")
	if ws, _ := m["workspacePaths"].([]any); cwd == "" && len(ws) > 0 {
		cwd, _ = ws[0].(string)
	}
	ev.Cwd = a.paths.normalize(cwd, "")
	if kind.ToolEvent() {
		ev.Tool = a.tool(call, ev.Cwd)
	}
	switch kind {
	case EventPostTool:
		e := str(m, "error")
		ev.ToolResult = &ToolResult{OK: e == "", Error: truncate(e)}
	case EventTurnEnd:
		switch str(m, "terminationReason") {
		case "error":
			ev.Event, ev.StopReason = EventTurnFailure, StopError
			ev.Error = truncate(str(m, "error"))
		case "max_steps_exceeded":
			ev.StopReason = StopMaxSteps
		default:
			ev.StopReason = StopNormal
		}
	}
	return ev, nil
}

func (a *antigravity) tool(call map[string]any, cwd string) *Tool {
	name := str(call, "name")
	args := obj(call, "args")
	kind := antigravityTools.kind(name)
	var f toolFields
	if kind == KindShell {
		f.command = str(args, "CommandLine")
	}
	for _, k := range antigravityPathArgs[name] {
		f.paths = append(f.paths, a.paths.all(cwd, str(args, k))...)
	}
	return finishTool(name, kind, f, call["args"])
}

func (a *antigravity) Screens(in ScreenInput) ScreenResult { return a.screens.evaluate(in) }

// TypeMessage: bracketed paste and \r
// (docs/provedores/antigravity.md#digitação).
func (a *antigravity) TypeMessage(text string) ([]byte, error) { return pasteAndSend(text, "\r") }
