package providers

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/lucasndas/mestre/pkg/clock"
)

// antigravity is the Antigravity CLI (`agy`) adapter
// (docs/provedores/antigravity.md). Its payloads are camelCase and do not
// name their event: the dispatcher passes the --event of the command line.
type antigravity struct {
	paths   pathNormalizer
	screens screenSet
	exec    ExecFunc
	clk     clock.Clock
	noPaste bool
}

var _ NativeTranslator = (*antigravity)(nil)

// agyBox is the input area of Antigravity (1.3.5, seen in the conformance
// test): the prompt > between two rules, above the footer with the agent.
var agyBox = regexp.MustCompile(`(?m)^\s*─{10,}\s*\n\s*>(?:[ \x{a0}]+(?:[^\d\s][^\n]*)?)?\s*\n\s*─{10,}`)

// NewAntigravity returns the Antigravity adapter.
func NewAntigravity(opts Options) Adapter {
	clk := opts.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	return &antigravity{paths: opts.paths(), exec: opts.Exec, clk: clk, noPaste: opts.AntigravityNoPaste, screens: screenSet{
		prompt:   agentPrompt,
		busy:     regexp.MustCompile(`(?i)esc to (interrupt|cancel)|Working…`),
		box:      agyBox,
		busyNear: regexp.MustCompile(`(?i)esc to (interrupt|cancel)`),
		known: []KnownScreen{
			{ID: "trust", Pattern: regexp.MustCompile(`Do you trust the contents of this project\?`), Action: ActionAnswer,
				Answers: map[string][]byte{"trust": menuFirst}},
			{ID: "login", Pattern: regexp.MustCompile(`Sign in with Google`), Action: ActionAlert, Checklist: "ALL-11"},
			// Antigravity has no permission_request hook: its own screen asks
			// (a tool the hook allowed that the native rules still ask about,
			// or one the permissionOverrides did not cover). The answer is the
			// decision recorded for that call; without one, deny.
			// 1.3.5 asks per action, even after an allow of the hook with
			// permissionOverrides: "Allow creation of this file?" (1. Yes,
			// allow creation / 2. No, deny creation) and "Run this command?"
			// (1. Yes, run command / … / 4. No, cancel). Enter takes the first,
			// once; Esc cancels, which interrupts the turn.
			{ID: "permission", Pattern: regexp.MustCompile(`Yes, allow tool call|(?s)(?:Allow [^\n]*|Run this command)\?\s*\n\s*>\s*1\.\s*Yes, (?:allow|run)`), Action: ActionAnswer,
				Answers: map[string][]byte{"allow": menuFirst, "deny": escape}, Default: "deny"},
			// A native subagent that asks to use a tool (1.3.5: "<name> needs
			// approval for Create", ctrl+k approves): no key denies it, so it
			// escalates.
			{ID: "subagent-approval", Pattern: regexp.MustCompile(`needs approval for`), Action: ActionEscalate},
			{ID: "review", Pattern: regexp.MustCompile(`(?i)review artifact|review plan`), Action: ActionEscalate},
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

// antigravityTools is the tool table. A file tool whose path argument the
// conformance test has not recorded comes out as `other` (finishTool).
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

// AntigravityBypassSandbox is the name a run_command with BypassSandbox
// gets: the tools rules of the class decide it.
const AntigravityBypassSandbox = "run_command:BypassSandbox"

// antigravityPathArgs are the path arguments seen in recorded payloads, by
// tool (agy 1.3.5, conformance test of 2026-10-14).
var antigravityPathArgs = map[string][]string{
	"write_to_file": {"TargetFile"},
	"view_file":     {"AbsolutePath"},
}

// escape is the Esc key: it cancels the native permission screen, which is a
// denial.
var escape = []byte("\x1b")

// agyRefused are the flags the Mestre never lets a base or a sheet give
// (docs/provedores/antigravity.md#flags), with why.
var agyRefused = map[string]string{
	"--dangerously-skip-permissions": "it approves every native permission request by itself, and voids --sandbox",
	"-p":                             "the base is interactive",
	"--print":                        "the base is interactive",
	"--prompt":                       "the base is interactive",
	"--add-dir":                      "the workspace is the worktree of the table",
	"--agent":                        "the Mestre sets the agent of the character",
	"--conversation":                 "the Mestre resumes by the id saved for the character",
	"--continue":                     "it resumes the most recent conversation of the folder; the Mestre resumes by id",
	"-c":                             "it resumes the most recent conversation of the folder; the Mestre resumes by id",
}

func refuseAgyFlags(args []string) error {
	for _, arg := range args {
		name := arg
		if i := strings.IndexByte(arg, '='); i > 0 && strings.HasPrefix(arg, "-") {
			name = arg[:i]
		}
		if why, bad := agyRefused[name]; bad {
			return fmt.Errorf("antigravity: option %s is not allowed: %s", name, why)
		}
	}
	return nil
}

// validAgentID reports whether id can name the agent file: letters, digits,
// "_" and "-", so it is also safe in the frontmatter.
func validAgentID(id string) bool {
	if id == "" || id[0] == '-' || id[0] == '_' {
		return false
	}
	for _, r := range id {
		if !idChar(r) {
			return false
		}
	}
	return true
}

func idChar(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_'
}

// BuildLaunch: `agy <base args> --agent mestre-<id>`, with `--conversation
// <id>` before it on a resume, the HOME of the account profile (and, on
// Windows, the USERPROFILE that follows it) and the check of the agent file
// that the engine runs before the start (Launch.Verify): a name that does
// not exist falls to the default agent without an error.
func (a *antigravity) BuildLaunch(req LaunchRequest) (Launch, error) {
	l, err := newLaunch(req)
	if err != nil {
		return Launch{}, err
	}
	if !validAgentID(req.AgentID) {
		return Launch{}, fmt.Errorf("antigravity: invalid character id %q for --agent mestre-<id>", req.AgentID)
	}
	if req.Worktree == "" || !absolute(req.Worktree) {
		return Launch{}, fmt.Errorf("antigravity: the worktree %q is not absolute: the agent file lives there", req.Worktree)
	}
	for _, args := range [][]string{req.Base.Args, req.ProviderArgs} {
		if err := refuseAgyFlags(args); err != nil {
			return Launch{}, err
		}
	}
	home, _ := envValue(l.Env, "HOME")
	if home == "" || !absolute(home) {
		return Launch{}, fmt.Errorf("antigravity: the HOME of the account profile is required (got %q): without a writable HOME agy does not start", home)
	}
	if req.OS == "windows" {
		l.Env = append(l.Env, "USERPROFILE="+home)
	}
	agent := "mestre-" + req.AgentID
	l.Args = slices.Clone(req.Base.Args)
	if req.ResumeSessionID != "" {
		l.Args = append(l.Args, "--conversation", req.ResumeSessionID)
		l.SessionID, l.Resumed = req.ResumeSessionID, true
	}
	l.Args = append(l.Args, "--agent", agent)
	l.Args = append(l.Args, req.ProviderArgs...)
	file := AntigravityAgentPath(req.Worktree, req.AgentID)
	l.Verify = func() error { return CheckAntigravityAgentFile(file, agent) }
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
	if kind == KindShell && args["BypassSandbox"] == true {
		// A command that asks to leave the --sandbox (1.3.5 does it by itself
		// when the sandbox fails, conformance test of 2026-10-14) is not a
		// plain shell command: it is decided by the tools rules of the class,
		// under its own name, like a tool the map does not know.
		return finishTool(AntigravityBypassSandbox, KindOther, toolFields{}, call["args"])
	}
	for _, k := range antigravityPathArgs[name] {
		f.paths = append(f.paths, a.paths.all(cwd, str(args, k))...)
	}
	return finishTool(name, kind, f, call["args"])
}

func (a *antigravity) Screens(in ScreenInput) ScreenResult { return a.screens.evaluate(in) }

// TypeMessage: bracketed paste and \r
// (docs/provedores/antigravity.md#digitação).
func (a *antigravity) TypeMessage(text string) ([]byte, error) {
	if a.noPaste && strings.Contains(clean(text), "\n") {
		return nil, fmt.Errorf("%w: antigravity does not take a bracketed paste, the text goes through the file of the long messages", ErrTooLong)
	}
	return pasteAndSend(text, "\r")
}
