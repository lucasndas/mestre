package providers_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/testkit"
)

// The native permission screen of Antigravity 1.3.5, as the real round of
// the conformance captured it (recording screen-permission-command): the
// title "Requesting permission for:", the command, the question, the menu
// and two lines below it. The call the dialog shows is found exactly, and
// nothing else answers it (docs/provedores/adaptador.md#prontidão-e-telas-interativas).
func TestAntigravityRealPermissionScreenTakesItsCall(t *testing.T) {
	t.Parallel()
	rec, err := testkit.LoadRecording(testkit.Recordings(), "antigravity", "1.3.5", "screen-permission-command")
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Screen string `json:"screen"`
	}
	if err := json.Unmarshal(rec.Body, &body); err != nil {
		t.Fatal(err)
	}
	const command = "echo ok > permitido/y.txt"
	call := func(c, choice string) providers.CallDecision {
		return providers.CallDecision{Tool: &providers.Tool{NativeName: "run_command", Kind: providers.KindShell, Command: c}, Choice: choice, Cwd: "/mconf/mesa"}
	}
	// The whole screen: the turn above, the rule the dialog draws under,
	// the dialog in place of the input box.
	full := "> Rode estes comandos no shell.\n\n● Thought for 4.4s\n" + strings.Repeat("─", 60) + "\n" + body.Screen
	a := providers.NewAntigravity(providers.Options{})
	res := a.Screens(providers.ScreenInput{Text: full, Stable: true, Begun: true})
	if res.Known == nil || res.Known.ID != "permission" {
		t.Fatalf("known screen = %+v; want permission", res.Known)
	}
	cases := []struct {
		name  string
		calls []providers.CallDecision
		want  int
	}{
		{"the call of the dialog", []providers.CallDecision{call("ls", "allow"), call(command, "allow")}, 1},
		{"a prefix of the command", []providers.CallDecision{call("echo ok", "allow")}, -1},
		{"a longer command", []providers.CallDecision{call(command+" && git push", "allow")}, -1},
		{"another command", []providers.CallDecision{call("git status", "allow")}, -1},
		{"the call denied", []providers.CallDecision{call(command, "allow"), call(command, "deny")}, 1},
	}
	for _, c := range cases {
		if got := providers.ScreenCall(res.Dialog, c.calls); got != c.want {
			t.Errorf("%s: ScreenCall = %d; want %d\n%s", c.name, got, c.want, res.Dialog)
		}
	}
	// The title is the dialog's own only at its top: a command line equal
	// to it is part of the command.
	forged := strings.Replace(res.Dialog, "   "+command, "   Requesting permission for:\n   "+command, 1)
	if got := providers.ScreenCall(forged, []providers.CallDecision{call(command, "allow")}); got != -1 {
		t.Errorf("a second title line taken as the dialog's: %d", got)
	}
}
