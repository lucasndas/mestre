package classes_test

import (
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
)

// cli.allow lists commands as typed; * matches any text, and "issue *" also
// matches "issue" (docs/classes/formato-das-permissoes.md).
func TestCLIAllowsMatchesTheCommandsTheClassLists(t *testing.T) {
	t.Parallel()
	allow := []string{"issue comment", "pr *", "sheet list"}
	for cmd, want := range map[string]bool{
		"issue comment": true, "issue close": false, "issue": false,
		"pr open": true, "pr update": true, "pr": true,
		"sheet list": true, "sheet show": false, "table": false,
	} {
		if got := classes.CLIAllows(allow, cmd); got != want {
			t.Errorf("CLIAllows(%q) = %v, want %v", cmd, got, want)
		}
	}
	if classes.CLIAllows(nil, "issue comment") {
		t.Error("a class with no cli.allow lists a command")
	}
}
