//go:build full

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mestre-ia/mestre/pkg/clock"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// CA-REPL-17: the engine ties the folders of the Citadel to the replication.
// A Tower that connects gets the global classes and the .mestre/ of the main
// clone of a workspace, and nothing else of the folder of the workspaces; what
// the Citadel changes afterwards reaches it too.
func TestTheEngineSendsTheFoldersOfTheCitadelAndOnlyTheMestreOfEachWorkspace(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	b := r.node("b", a)
	ws := a.paths.Workspaces
	writeFile(t, filepath.Join(a.paths.ClassesGlobais, "qa", "CLASS.md"), "a classe")
	writeFile(t, filepath.Join(ws, "meu-app", "principal", ".mestre", "regras", "politica-de-aprovacao.yaml"), "versao: 1\n")
	writeFile(t, filepath.Join(ws, "meu-app", "principal", "src", "main.go"), "package main") // the project's
	writeFile(t, filepath.Join(ws, "meu-app", "mesas", "42-login", "leak.txt"), "worktree")
	writeFile(t, filepath.Join(ws, "meu-app", "espelho.git", "HEAD"), "ref: refs/heads/main")

	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	r.join(b, a, clock.Real{})
	r.start(b, clock.Real{})
	r.waitFor("b to connect", func() bool { return b.connected(1) })

	has := func(p string) bool { _, err := os.Stat(p); return err == nil }
	policy := filepath.Join(b.paths.Workspaces, "meu-app", "principal", ".mestre", "regras", "politica-de-aprovacao.yaml")
	class := filepath.Join(b.paths.ClassesGlobais, "qa", "CLASS.md")
	r.waitFor("the files to reach the Tower", func() bool { return has(policy) && has(class) })
	for _, p := range []string{
		filepath.Join(b.paths.Workspaces, "meu-app", "principal", "src", "main.go"),
		filepath.Join(b.paths.Workspaces, "meu-app", "mesas", "42-login", "leak.txt"),
		filepath.Join(b.paths.Workspaces, "meu-app", "espelho.git", "HEAD"),
	} {
		if has(p) {
			t.Errorf("%s reached the Tower: the folder of the workspaces carries only the .mestre/ of the main clone", p)
		}
	}
	if got, _ := os.ReadFile(class); string(got) != "a classe" {
		t.Errorf("class = %q", got)
	}
}
