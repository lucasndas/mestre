package classes_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
)

func sheetPerms(t *testing.T, yaml string) *classes.Permissions {
	t.Helper()
	p, err := classes.ParsePermissions([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReviewSheetRefusesWhatTheClassForbidsAndIgnoresWhatItDoesNotGive(t *testing.T) {
	t.Parallel()
	class := sheetPerms(t, `
version: 1
commands:
  allow: ["git status", "npm *"]
  deny: ["npm publish*"]
  default: ask
files:
  deny: ["/etc/**"]
git:
  commit: false
network:
  internet: false
`)
	tests := []struct {
		name             string
		sheet            string
		refused, ignored string
	}{
		{"an allow the class covers", "version: 1\ncommands:\n  allow: [\"npm test\"]\n", "", ""},
		{"an allow a deny covers", "version: 1\ncommands:\n  allow: [\"npm publish --tag x\"]\n", "commands.allow `npm publish --tag x`: a classe proíbe `npm publish*`", ""},
		{"an allow the class does not give", "version: 1\ncommands:\n  allow: [\"make\"]\n", "", "commands.allow `make`"},
		{"a file a deny covers", "version: 1\nfiles:\n  read: [\"/etc/passwd\"]\n", "files.read `/etc/passwd`", ""},
		{"a switch the class turned off", "version: 1\ngit:\n  commit: true\n", "git.commit", ""},
		{"internet the class turned off", "version: 1\nnetwork:\n  internet: true\n", "network.internet", ""},
		{"a switch false is a restriction", "version: 1\ngit:\n  commit: false\nnetwork:\n  internet: false\n", "", ""},
		{"operator the class does not have", "version: 1\noperator: true\n", "", "operator"},
		{"a default looser than the class's", "version: 1\ncommands:\n  default: allow\n", "", "commands.default"},
		{"a default stricter than the class's", "version: 1\ncommands:\n  default: deny\n", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := classes.ReviewSheet(class, sheetPerms(t, tt.sheet))
			has := func(list []string, want string) bool {
				if want == "" {
					return len(list) == 0
				}
				return slices.ContainsFunc(list, func(s string) bool { return strings.Contains(s, want) })
			}
			if !has(r.Refused, tt.refused) || !has(r.Ignored, tt.ignored) {
				t.Errorf("refused %q, ignored %q; want %q and %q", r.Refused, r.Ignored, tt.refused, tt.ignored)
			}
			if r.OK() != (tt.refused == "") {
				t.Errorf("OK = %v", r.OK())
			}
		})
	}
	if r := classes.ReviewSheet(nil, sheetPerms(t, "version: 1\ngit:\n  commit: true\n")); !r.OK() || len(r.Ignored) != 0 {
		t.Errorf("without a class: %+v", r)
	}
}
