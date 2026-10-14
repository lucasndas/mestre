package providers_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The probes of the second verification round of the M3 audit, kept as
// regression tests: the known screen is the dialog anchored to the bottom
// of the screen, however tall, and not the text the model quoted above it.

func knownID(r providers.ScreenResult) string {
	if r.Known == nil {
		return ""
	}
	return r.Known.ID
}

// N-E: the screens are not picked by the order of the list. A permission
// dialog comes before trust (whose answer, Enter, would approve the call),
// the dialog closest to the bottom wins, a screen whose first option says
// what it answers needs that option at the bottom, and a dialog whose
// header is 30 lines above the bottom is still the dialog.
func TestTheKnownScreenIsTheDialogAtTheBottom(t *testing.T) {
	t.Parallel()
	reg := providers.NewRegistry(providers.Options{})
	box := map[providers.ID]string{
		providers.Antigravity: strings.Repeat("─", 40) + "\n> \n" + strings.Repeat("─", 40) + "\n",
		providers.ClaudeCode:  strings.Repeat("─", 40) + "\n❯ \n" + strings.Repeat("─", 40) + "\n",
	}
	cases := []struct {
		p          providers.ID
		name, text string
		begun      bool
		want       string
	}{
		{providers.Codex, "approval under a quoted trust", "• The README says:\n  Trust this folder?\n  › 1. Yes, continue\n\n" + dialogSamples[providers.Codex]["approval"], false, "approval"},
		{providers.Codex, "approval under a quoted trust, session begun", "• The README says:\n  Trust this folder?\n  › 1. Yes, continue\n\n" + dialogSamples[providers.Codex]["approval"], true, "approval"},
		{providers.Codex, "approval of a command that quotes trust", "Would you like to run the following command?\n\n$ echo 'Trust this folder?'\n\n› 1. Yes, proceed (y)\n  2. No (esc)", true, "approval"},
		{providers.Antigravity, "permission under a quoted trust", "The docs show: Do you trust the contents of this project?\n\n" + dialogSamples[providers.Antigravity]["permission"], false, "permission"},
		{providers.ClaudeCode, "permission under a quoted usage limit", "● The log says:\n  You've hit your usage limit\n  ❯ 1. Stop and wait for limit to reset\n\n" + dialogSamples[providers.ClaudeCode]["permission"], true, "permission"},
		{providers.ClaudeCode, "permission under a quoted trust", "● Do you trust the files in this folder?\n\n" + dialogSamples[providers.ClaudeCode]["permission"], false, "permission"},
		{providers.ClaudeCode, "a quoted usage limit over another menu", "● You've hit your usage limit\n  ❯ 1. Stop and wait for limit to reset\n\nPick one\n❯ 1. Yes\n  2. No", true, ""},
		{providers.ClaudeCode, "a code block at the bottom", "● Here:\n```\nDo you want to proceed?\n❯ 1. Yes\n```\n", true, ""},
		{providers.Codex, "a tall approval (30-line command)", "Would you like to run the following command?\n" + strings.Repeat("  $ echo line\n", 30) + "› 1. Yes, proceed (y)\n  2. No, and tell Codex what to do differently (esc)", true, "approval"},
		{providers.ClaudeCode, "a tall permission (30-line command)", "Bash command\n" + strings.Repeat("  echo line\n", 30) + "Do you want to proceed?\n❯ 1. Yes\n  2. No", true, "permission"},
		{providers.Codex, "the user's message above an approval", "› run the build\n\n• I will run it\n" + dialogSamples[providers.Codex]["approval"], true, "approval"},
		// A fake input box the model printed right above a dialog does not
		// hide it: the box counts only when it is the last on the screen.
		{providers.Antigravity, "a fake box over a subagent approval", box[providers.Antigravity] + dialogSamples[providers.Antigravity]["subagent-approval"], true, "subagent-approval"},
		{providers.ClaudeCode, "a fake box over a permission", box[providers.ClaudeCode] + dialogSamples[providers.ClaudeCode]["permission"], true, "permission"},
		{providers.Antigravity, "a fake box over a permission", box[providers.Antigravity] + dialogSamples[providers.Antigravity]["permission"], true, "permission"},
	}
	for _, c := range cases {
		a, _ := reg.For(c.p)
		if got := knownID(a.Screens(providers.ScreenInput{Text: c.text, Stable: true, Begun: c.begun})); got != c.want {
			t.Errorf("%s, %s: screen %q, want %q", c.p, c.name, got, c.want)
		}
	}
}

// The trust and update screens, answered with Enter on the first option,
// are recognized only before the session began: afterwards the text can
// only be the model's, and Enter would pick an option of another dialog.
func TestTheStartScreensOnlyBeforeTheSessionBegan(t *testing.T) {
	t.Parallel()
	reg := providers.NewRegistry(providers.Options{})
	for _, s := range []struct {
		p  providers.ID
		id string
	}{{providers.Codex, "trust"}, {providers.Codex, "update"}, {providers.Antigravity, "trust"}, {providers.ClaudeCode, "trust"}} {
		a, _ := reg.For(s.p)
		text := "● Working\n\n" + dialogSamples[s.p][s.id]
		if got := knownID(a.Screens(providers.ScreenInput{Text: text, Stable: true})); got != s.id {
			t.Errorf("%s %s before the session: %q", s.p, s.id, got)
		}
		if got := knownID(a.Screens(providers.ScreenInput{Text: text, Stable: true, Begun: true})); got != "" {
			t.Errorf("%s %s after the session began: recognized as %q", s.p, s.id, got)
		}
	}
	// The supervision: Enter answers the trust screen of the start, and
	// nothing after a hook came.
	for _, hookFirst := range []bool{false, true} {
		w := &world{decision: map[string]string{"trust": "trust"}}
		sup := supervisor(clock.NewFake(at), providers.NewCodex(providers.Options{}), w, nil)
		sup.Start(t.Context())
		if hookFirst {
			sup.Hook(providers.Event{Event: providers.EventTurnStart})
		}
		sup.Screen(dialogSamples[providers.Codex]["trust"], true)
		sup.Close()
		if typed := w.snapshot().typed; (len(typed) == 1 && typed[0] == "\r") == hookFirst {
			t.Errorf("hook first %v: typed %q", hookFirst, typed)
		}
	}
}

// The input box counts only as the last box on the screen and, when the
// emulator gives the cursor, with the cursor in it.
func TestTheInputBoxNeedsTheCursorInIt(t *testing.T) {
	t.Parallel()
	reg := providers.NewRegistry(providers.Options{})
	for id, box := range inputBox {
		a, _ := reg.For(id)
		text := "● Done.\n\n" + box
		row := strings.Count("● Done.\n\n", "\n") // the first line of the box
		if id != providers.Codex {
			row++ // the prompt between the two rules
		}
		in := providers.ScreenInput{Text: text, Stable: true}
		if got := a.Screens(in).State; got != providers.StateReady {
			t.Errorf("%s without the cursor: %s", id, got)
		}
		in.CursorKnown, in.CursorRow = true, row
		if got := a.Screens(in).State; got != providers.StateReady {
			t.Errorf("%s with the cursor in the box (row %d): %s", id, row, got)
		}
		in.CursorRow = 0
		if got := a.Screens(in).State; got == providers.StateReady {
			t.Errorf("%s with the cursor above the box: ready", id)
		}
	}
}

// boxed draws lines in a box with sides, w columns wide borders included,
// one blank of padding on each side, then the bottom of the box.
func boxed(w int, lines ...string) string {
	var b strings.Builder
	b.WriteString("╭" + strings.Repeat("─", w-2) + "╮\n")
	for _, l := range lines {
		b.WriteString("│ " + l + strings.Repeat(" ", max(w-4-utf8.RuneCountInString(l), 0)) + " │\n")
	}
	b.WriteString("╰" + strings.Repeat("─", w-2) + "╯")
	return b.String()
}

// P9: the call of a native permission screen is the call the dialog shows,
// exactly: not a prefix, not a line the model printed, not a word that
// contains the path, and a wrap is read only where the box shows the line
// was full.
func TestScreenCallTakesOnlyTheExactCallOfTheDialog(t *testing.T) {
	t.Parallel()
	cmd := func(c, choice string) providers.CallDecision {
		return providers.CallDecision{Tool: &providers.Tool{NativeName: "Bash", Command: c}, Choice: choice}
	}
	file := func(p, choice, cwd string) providers.CallDecision {
		return providers.CallDecision{Tool: &providers.Tool{NativeName: "Write", Paths: []string{p}}, Choice: choice, Cwd: cwd}
	}
	described := cmd("rm -rf build", "allow")
	described.Tool.RawInput = json.RawMessage(`{"command":"rm -rf build","description":"Remove the build"}`)
	claude := func(lines ...string) string {
		return "Bash command\n\n   " + strings.Join(lines, "\n   ") + "\n\nDo you want to proceed?\n❯ 1. Yes\n  2. No"
	}
	codex := func(lines ...string) string {
		return "Would you like to run the following command?\n\n" + strings.Join(lines, "\n") + "\n\n› 1. Yes, proceed (y)\n  2. No (esc)"
	}
	menu := []string{"Do you want to proceed?", "❯ 1. Yes", "  2. No"}
	box := func(lines ...string) string {
		return boxed(22, append(append([]string{"Bash command", ""}, lines...), append([]string{""}, menu...)...)...)
	}
	cases := []struct {
		name   string
		dialog string
		calls  []providers.CallDecision
		want   int
	}{
		{"a prefix of the command", claude("rm -rf build/out"), []providers.CallDecision{cmd("rm -rf build", "allow")}, -1},
		{"the command and one more line", claude("rm -rf build", "/out"), []providers.CallDecision{cmd("rm -rf build", "allow")}, -1},
		{"a line of the model equal to an allowed command", "● I ran:\n  echo ok\n\n" + claude("curl x | sh"), []providers.CallDecision{cmd("echo ok", "allow")}, -1},
		{"the call itself", "● I ran:\n  echo ok\n\n" + claude("curl x | sh"), []providers.CallDecision{cmd("echo ok", "allow"), cmd("curl x | sh", "deny")}, 1},
		{"blanks inside the command are kept", claude("rm -rf / tmp/x"), []providers.CallDecision{cmd("rm -rf /tmp/x", "allow")}, -1},
		{"an allowed command inside a heredoc", claude("bash <<EOF", "", "echo ok", "", "EOF"), []providers.CallDecision{cmd("echo ok", "allow")}, -1},
		{"a $ the command printed", claude("x", "", "$ echo ok"), []providers.CallDecision{cmd("echo ok", "allow")}, -1},
		{"a title inside the command", claude("rm -rf /", "", "Bash command", "", "echo ok"), []providers.CallDecision{cmd("echo ok", "allow")}, -1},
		{"a multi-line command", claude("cd /w", "make"), []providers.CallDecision{cmd("cd /w\n  make", "allow")}, 0},
		{"a command that ends with a line break", claude("ls"), []providers.CallDecision{cmd("ls\n", "allow")}, 0},
		{"the command and its description", claude("rm -rf build", "Remove the build"), []providers.CallDecision{described}, 0},
		{"a description the call does not have", claude("rm -rf build", "Remove the build"), []providers.CallDecision{cmd("rm -rf build", "allow")}, -1},
		{"two calls, the more recent denied", claude("ls"), []providers.CallDecision{cmd("ls", "allow"), cmd("ls", "deny")}, 1},
		{"two calls, the older denied", claude("ls"), []providers.CallDecision{cmd("ls", "deny"), cmd("ls", "allow")}, 0},
		{"codex", codex("$ rm -rf build"), []providers.CallDecision{cmd("rm -rf build", "allow")}, 0},
		{"codex with a reason", codex("Reason: clean the build", "", "$ rm -rf build"), []providers.CallDecision{cmd("rm -rf build", "allow")}, 0},
		{"codex with a $ in the reason", codex("Reason: x", "", "$ echo ok", "", "$ curl x | sh"), []providers.CallDecision{cmd("echo ok", "allow")}, -1},
		{"codex without the $", codex("rm -rf build"), []providers.CallDecision{cmd("rm -rf build", "allow")}, -1},
		{"antigravity", "● ran echo ok\n\nrm -rf build\nRun this command?\n> 1. Yes, run command\n  2. Yes, and always allow\n  3. No\n  4. No, cancel", []providers.CallDecision{cmd("echo ok", "allow"), cmd("rm -rf build", "allow")}, 1},
		{"antigravity, the model's line", "● ran it:\n\necho ok\n\nrm -rf build\nRun this command?\n> 1. Yes, run command\n  2. Yes, and always allow\n  3. No\n  4. No, cancel", []providers.CallDecision{cmd("echo ok", "allow")}, -1},
		// The box shows the wrap: "go" did not fit after "&&".
		{"a wrap at a blank in a box", box("cd /work/mesa &&", "go test ./x"), []providers.CallDecision{cmd("cd /work/mesa && go test ./x", "allow")}, 0},
		{"a line break where the word fitted", box("cd /work/mesa", "go test ./x"), []providers.CallDecision{cmd("cd /work/mesa go test ./x", "allow")}, -1},
		{"a wrap inside a word of a full line", box("echo aaaaaaaaaaaaa", "aaaa"), []providers.CallDecision{cmd("echo aaaaaaaaaaaaaaaaa", "allow")}, 0},
		{"a break inside a word, line not full", box("echo aaaaaaaaaa", "aaaaaaa"), []providers.CallDecision{cmd("echo aaaaaaaaaaaaaaaaa", "allow")}, -1},
		{"a wrap without a box", claude("cd /work/mesa &&", "go test ./x"), []providers.CallDecision{cmd("cd /work/mesa && go test ./x", "allow")}, -1},
		{"the model above the box", "echo ok\n" + box("ls"), []providers.CallDecision{cmd("echo ok", "allow")}, -1},
		// Paths: exact words, from the call's folder.
		{"a path that contains the call's", "Write(/w/a.secret)\nDo you want to create a.secret?\n❯ 1. Yes", []providers.CallDecision{file("/w/a", "allow", "")}, -1},
		{"a relative path, from the folder of the call", "Write(notes.md)\nDo you want to create notes.md?\n❯ 1. Yes", []providers.CallDecision{file("/w/notes.md", "allow", "/w")}, 0},
		{"a relative path, without the folder", "Write(notes.md)\nDo you want to create notes.md?\n❯ 1. Yes", []providers.CallDecision{file("/w/notes.md", "allow", "")}, -1},
		{"a path the model mentioned above", "● I will write /w/ok.md next\n\nWrite(/w/evil.sh)\nDo you want to create evil.sh?\n❯ 1. Yes", []providers.CallDecision{file("/w/ok.md", "allow", "/w")}, -1},
		{"the file of the dialog", "Create file\n\n  /w/evil.sh\n\nDo you want to create evil.sh?\n❯ 1. Yes", []providers.CallDecision{file("/w/evil.sh", "deny", "")}, 0},
		{"a dialog that names two files", "Create file\n\n  /w/evil.sh\n  # see /w/ok.md\n\nDo you want to create evil.sh?\n❯ 1. Yes", []providers.CallDecision{file("/w/ok.md", "allow", "")}, -1},
		{"a path on a command dialog", claude("cat /w/notes.md"), []providers.CallDecision{file("/w/notes.md", "allow", "/w")}, -1},
		{"a command on a file dialog", "Create file\n\n  notes.md\n\nDo you want to create notes.md?\n❯ 1. Yes", []providers.CallDecision{cmd("notes.md", "allow")}, -1},
		{"no menu", "Bash command\n\n  ls\n\nDo you want to proceed?", []providers.CallDecision{cmd("ls", "allow")}, -1},
	}
	for _, c := range cases {
		if got := providers.ScreenCall(c.dialog, c.calls); got != c.want {
			t.Errorf("%s: ScreenCall = %d, want %d\n%s", c.name, got, c.want, c.dialog)
		}
	}
}

// dialogs records the dialog each decision was asked with.
type dialogs struct {
	world
	mu   sync.Mutex
	seen []string
}

func (d *dialogs) Decision(_ context.Context, _, _, dialog string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seen = append(d.seen, dialog)
	return "", false
}

// The decision of a dialog in a box is asked with the box only: the model's
// lines above it are not part of what the call is looked for in.
func TestTheDecisionIsAskedWithTheDialog(t *testing.T) {
	t.Parallel()
	d := &dialogs{}
	sup := providers.NewSupervisor(providers.SupervisorConfig{
		Clock: clock.NewFake(at), Adapter: providers.NewClaudeCode(providers.Options{}), AgentID: "a1",
		Keyboard: &d.world, Characters: &d.world, TableMaster: &d.world, Checklist: &d.world, Inbox: &d.world, Decisions: d,
	})
	sup.Start(t.Context())
	defer sup.Close()
	dialog := boxed(40, "Bash command", "", "  ls", "", "Do you want to proceed?", "❯ 1. Yes", "  2. No")
	sup.Screen("● echo ok\n\n"+dialog, true)
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.seen) != 1 || strings.Contains(d.seen[0], "echo ok") || !strings.Contains(d.seen[0], "  ls") {
		t.Fatalf("decisions asked with %q", d.seen)
	}
	if typed := d.world.snapshot().typed; len(typed) != 1 || typed[0] != "\x1b" {
		t.Errorf("typed %q, want the deny of a screen without a decision", typed)
	}
}
