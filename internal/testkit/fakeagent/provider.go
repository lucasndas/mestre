// Package fakeagent is the fake agent of the tests (docs/qualidade/dubles.md
// #agente-falso-fakeagent): one program that imitates Claude Code, Codex
// and Antigravity. The same code runs inside the test process (MemLauncher,
// fast suite) and as the cmd/fakeagent binary in a real terminal (full
// suite).
//
// M0 delivers the skeleton: minimal TUI, start screens (login, folder
// trust, Codex hook review, and FAKEAGENT_START_SCREEN, a screen no adapter
// knows), hooks read from the provider files and run
// with each provider's semantics, recorded payloads, scripts and
// /fake-dump. The fake Codex fires the nine events the Mestre binds and
// writes its session transcript with the use (token_count), and simulates
// the usage limit with FAKEAGENT_CODEX_USAGE_LIMIT=1 (codex.go). The rest of
// the usage and limit emulation and the full instruction dump are in the
// other adapters and in the conformance test.
package fakeagent

import (
	"path/filepath"
	"strings"
)

// Provider is the imitated CLI.
type Provider string

// Providers.
const (
	Claude      Provider = "claude"
	Codex       Provider = "codex"
	Antigravity Provider = "antigravity"
)

// ProviderFromName maps the program name (claude, codex, agy, with .exe on
// Windows) to a provider.
func ProviderFromName(argv0 string) (Provider, bool) {
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(argv0)), ".exe")
	switch base {
	case "claude":
		return Claude, true
	case "codex":
		return Codex, true
	case "agy":
		return Antigravity, true
	}
	return "", false
}

// ParseProvider reads the value of --provider.
func ParseProvider(s string) (Provider, bool) {
	switch Provider(s) {
	case Claude, Codex, Antigravity:
		return Provider(s), true
	}
	return "", false
}

// profile holds what changes between providers.
type profile struct {
	title       string
	recordings  string // recordings origin
	shellTool   string
	editTool    string
	shellArg    string // key of the command in the tool input
	trustQ      string
	trustYes    string
	trustNo     string
	loginTitle  string
	loginOpts   []string
	credFile    string // relative to the account dir; "" = no login check yet
	startEvent  string // "" when the provider has none
	promptEvent string
	events      []string // hook events the fake fires; any other fails loudly
}

var profiles = map[Provider]profile{
	Claude: {
		title: "Claude Code (fake)", recordings: "claude-code",
		shellTool: "Bash", editTool: "Write", shellArg: "command",
		trustQ: "Do you trust the files in this folder?", trustYes: "Yes, proceed", trustNo: "No, exit",
		loginTitle: "Select login method:",
		loginOpts:  []string{"Claude account with subscription", "Anthropic Console account"},
		credFile:   ".credentials.json", startEvent: "SessionStart", promptEvent: "UserPromptSubmit",
		events: []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PermissionRequest", "PostToolUse", "PostToolUseFailure",
			"Stop", "StopFailure", "Notification", "PreCompact", "SessionEnd"},
	},
	Codex: {
		title: "Codex (fake)", recordings: "codex",
		shellTool: "Bash", editTool: "apply_patch", shellArg: "command",
		trustQ: "Trust this folder? Codex can read, edit, and run files here.", trustYes: "Trust and continue", trustNo: "Quit",
		loginTitle: "Choose how you want to use Codex.",
		loginOpts:  []string{"Sign in with ChatGPT", "Sign in with Device Code", "Use an OpenAI API key"},
		credFile:   "auth.json", startEvent: "SessionStart", promptEvent: "UserPromptSubmit",
		// The nine events the Mestre binds (docs/provedores/codex.md#hooks).
		events: []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PermissionRequest", "PostToolUse",
			"Stop", "PreCompact", "SessionEnd", "Interrupt"},
	},
	Antigravity: {
		title: "Antigravity (fake)", recordings: "antigravity",
		shellTool: "run_command", editTool: "write_to_file", shellArg: "CommandLine",
		trustQ: "Do you trust the contents of this project?", trustYes: "Yes, I trust this folder", trustNo: "No, exit",
		loginTitle:  "Sign in with Google to use Antigravity",
		loginOpts:   []string{"Sign in with Google (URL and code)"},
		credFile:    "antigravity-oauth-token",
		promptEvent: "PreInvocation",
		events:      []string{"PreInvocation", "PreToolUse", "PostToolUse", "PostInvocation", "Stop"},
	},
}

// Screen texts the tests and the adapter look for.
const (
	// PromptMarker ends the screen when the agent is ready for input.
	PromptMarker = "\r\n> "
	// BusyMarker is printed when a turn starts.
	BusyMarker = "* Working…"
	// HookReviewTitle opens the Codex review of new or modified hooks.
	HookReviewTitle = "Hooks need review"
)

// Codex hook review options, in order.
var hookReviewOpts = []string{"Review hooks", "Trust all and continue", "Continue without trusting"}
