package providers_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
)

// A text of each known screen of each adapter, as the dialog draws it.
var dialogSamples = map[providers.ID]map[string]string{
	providers.ClaudeCode: {
		"trust":           "Do you trust the files in this folder?\n❯ 1. Yes, proceed\n  2. No, exit",
		"login":           "Select login method:\n❯ 1. Claude account with subscription",
		"first-use":       "Choose the text style that looks best with your terminal\n❯ 1. Dark mode",
		"external-import": "Allow external CLAUDE.md file imports?\n❯ 1. Yes",
		"mcp-servers":     "New MCP server found in .mcp.json: x\n❯ 1. Use this and all future MCP servers in this project",
		"api-key":         "Detected a custom API key in your environment\n❯ 1. Yes",
		"usage-limit":     "You've hit your usage limit\n❯ 1. Stop and wait for limit to reset\n  2. Upgrade",
		"quota-stale":     "Your limit has reset. Press Enter to continue",
		"permission":      "Bash command\n  rm -rf build\nDo you want to proceed?\n❯ 1. Yes\n  2. No, and tell Claude what to do differently (esc)",
	},
	providers.Codex: {
		"login":           "Welcome to Codex\n› 1. Sign in with ChatGPT\n  2. Provide your own API key",
		"trust":           "You are in /w\nTrust this folder?\n› 1. Yes, continue\n  2. No, quit",
		"hook-review":     "Hooks need review\n› 1. Review hooks\n  2. Trust all and continue\n  3. Continue without trusting",
		"update":          "Update available!\n› 1. Update now\n  2. Skip\n  3. Skip until next version",
		"windows-sandbox": "Set up the Codex agent sandbox\n› 1. Set up",
		"approval":        "Would you like to run the following command?\n  $ rm -rf build\n› 1. Yes, proceed (y)\n  2. No, and tell Codex what to do differently (esc)",
	},
	providers.Antigravity: {
		"trust":             "Do you trust the contents of this project?\n> 1. Yes, trust\n  2. No",
		"login":             "Sign in with Google to continue",
		"permission":        "rm -rf build\nRun this command?\n> 1. Yes, run command\n  2. Yes, and always allow\n  3. No\n  4. No, cancel",
		"subagent-approval": "helper needs approval for Create (ctrl+k to approve)",
		"review":            "Review plan\n> 1. Approve",
	},
}

// inputBox is the input area each adapter reads as ready: the model's turn
// has ended or is under way, no dialog is on display.
var inputBox = map[providers.ID]string{
	providers.ClaudeCode:  strings.Repeat("─", 40) + "\n❯ \n" + strings.Repeat("─", 40) + "\n  ? for shortcuts",
	providers.Codex:       "› Ask Codex to do anything\n\n  gpt-5.5 · ? for shortcuts",
	providers.Antigravity: strings.Repeat("─", 40) + "\n> \n" + strings.Repeat("─", 40) + "\n? for shortcuts  mestre-a1",
}

// The model (or a tool) quoting the text of a dialog is not the dialog: a
// known screen is matched only at the bottom, where a dialog is drawn, and
// only when the input box is not there (M3 audit). The texts themselves
// are recognized when they are the dialog.
func TestADialogQuotedByTheModelIsNotTheDialog(t *testing.T) {
	t.Parallel()
	reg := providers.NewRegistry(providers.Options{})
	filler := strings.Repeat("● more output of the model\n", 30)
	for id, samples := range dialogSamples {
		a, _ := reg.For(id)
		for screen, text := range samples {
			if r := a.Screens(providers.ScreenInput{Text: "● Working on it\n\n" + text, Stable: true}); r.Known == nil || r.Known.ID != screen {
				t.Errorf("%s %s: the dialog itself was not recognized (%+v)", id, screen, r.Known)
			}
			quoted := "● The fake prints this on start:\n  " + strings.ReplaceAll(text, "\n", "\n  ") + "\n  and then waits.\n\n"
			for name, s := range map[string]string{
				"above the input box":         quoted + inputBox[id],
				"far above the bottom":        quoted + filler,
				"far above, with the box too": quoted + filler + inputBox[id],
			} {
				if r := a.Screens(providers.ScreenInput{Text: s, Stable: true}); r.Known != nil {
					t.Errorf("%s %s quoted %s: recognized as %s", id, screen, name, r.Known.ID)
				}
			}
			if r := a.Screens(providers.ScreenInput{Text: quoted + inputBox[id], Stable: true}); r.State != providers.StateReady {
				t.Errorf("%s %s quoted above the input box: state %s, want ready", id, screen, r.State)
			}
		}
	}
	// Every known screen has a sample.
	for id, samples := range dialogSamples {
		a, _ := reg.For(id)
		known := providers.KnownScreens(a)
		for _, k := range known {
			if _, ok := samples[k.ID]; !ok {
				t.Errorf("%s: screen %s has no sample", id, k.ID)
			}
		}
	}
}

// The rendered screen has the blanks at the end of each row trimmed
// (internal/terminal/ptyd), so the `ready: pattern` of a base that waits for
// the space after the prompt ('\$ $', the example of the document) still
// finds it.
func TestAReadyPatternThatEndsInTheSpaceAfterThePromptMatchesATrimmedRow(t *testing.T) {
	t.Parallel()
	null := providers.NewNull()
	re := regexp.MustCompile(`\$ $`)
	for name, screen := range map[string]string{"trimmed": "user@host:~$", "with the space": "user@host:~$ "} {
		if got := null.Screens(providers.ScreenInput{Text: screen, Stable: true, ReadyPattern: re}); got.State != providers.StateReady {
			t.Errorf("%s: state = %s, want ready", name, got.State)
		}
	}
	if got := null.Screens(providers.ScreenInput{Text: "running…", Stable: true, ReadyPattern: re}); got.State == providers.StateReady {
		t.Error("a screen without the prompt is ready")
	}
}
