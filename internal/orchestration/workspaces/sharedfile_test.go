package workspaces_test

import (
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
)

// Part of CA-REPL-17: only the .mestre/ of the main clone of each workspace
// travels from the folder of the workspaces.
func TestOnlyTheMestreOfTheMainCloneOfEachWorkspaceIsShared(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		rel  string
		dir  bool
		want bool
	}{
		{"meu-app", true, true}, {"meu-app/principal", true, true}, {"meu-app/principal/.mestre", true, true},
		{"meu-app/principal/.mestre/regras", true, true}, {"meu-app/principal/.mestre/regras/politica-de-aprovacao.yaml", false, true},
		{"meu-app/principal/.mestre/mesas/42-login/pergaminhos/n1.md", false, true},
		{"meu-app/principal/src", true, false}, {"meu-app/principal/src/main.go", false, false}, {"meu-app/principal/.mestre", false, false},
		{"meu-app/principal/.mestreX/a", false, false}, {"meu-app/principal/.git", true, false},
		{"meu-app/mesas", true, false}, {"meu-app/mesas/42-login/leak.txt", false, false}, {"meu-app/espelho.git", true, false}, {"meu-app/espelho.git/HEAD", false, false},
		{"meu-app/outro/.mestre/a", false, false}, {".mestre/a", false, false}, {"", true, false}, {"..", true, false}, {"../x/principal/.mestre/a", false, false},
		{"meu-app", false, false}, {"meu-app/principal", false, false},
	} {
		if got := workspaces.SharedFile(c.rel, c.dir); got != c.want {
			t.Errorf("SharedFile(%q, dir %v) = %v; want %v", c.rel, c.dir, got, c.want)
		}
	}
}
