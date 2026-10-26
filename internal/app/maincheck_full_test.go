//go:build full

package app

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/presets"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/sessionzero"
	"github.com/mestre-ia/mestre/internal/terminal"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The terminals half of CA-GM-17 over the real composition of `mestre run`: the Runtime the engine
// builds from `terminais` runs the CheckStart of the session zero for the Grand
// Master in the main clone (the table ""), and a provider whose files were not
// generated there does not start it.
func TestMestreRunRefusesTheGrandMasterWhenTheFilesOfItsProviderWereNotGenerated(t *testing.T) {
	t.Parallel()
	buildRuntimeBinaries(t)
	r := newRig(t)
	root, err := os.MkdirTemp("", "mrg")
	if err != nil {
		t.Fatal(err)
	}
	if rr, err := filepath.EvalSymlinks(root); err == nil {
		root = rr
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	r.root = root
	a := r.node("a")
	a.paths.CLISocket = filepath.Join(root, "a", "cli.sock")
	a.cfg.Terminais = &Terminals{Dir: runtimeBuilds.ptyFolde, Version: runtimePTYVersion}
	a.setup = func(e *Engine) {
		e.deps.HostOrchestration = true
		e.deps.Spawner = &terminal.DirectSpawner{Suite: "full", System: terminal.NewSystem()}
		e.deps.CharacterPrep = runtimePrep{cfg: filepath.Join(root, "home", "cfg"), home: filepath.Join(root, "home"), sock: a.paths.CLISocket}
	}
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act and host the orchestration", func() bool { return a.acting() && a.e.orc.Load() != nil })

	main := filepath.Join(root, "principal")
	if err := os.MkdirAll(filepath.Join(main, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", main, "init", "-q", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	// the file of Claude Code is the person's, and broken: the Mestre does not rewrite it
	if err := os.WriteFile(filepath.Join(main, ".claude", "settings.local.json"), []byte("{isto não é json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = a.e.rt.rt.Start(t.Context(), characters.StartCommand{
		Character: "gm1", Terminal: "t-gm1", Kind: characters.KindCharacter,
		Request: sessionzero.CharacterRequest{
			ID: "gm1", Name: characters.NameGrandMaster, Role: characters.RoleGrandMaster, Workspace: "meu-app", Machine: a.id, Dir: main,
			Snapshot: presets.Snapshot{Sheet: presets.Sheet{Name: "Grão-Mestre", Agent: "claude-code", Class: "Grão-Mestre"}, Base: presets.Base{Name: "claude-code", Ready: presets.Ready{Mode: presets.ReadyHook}}},
		},
	})
	var blocked *sessionzero.BlockedError
	if !errors.As(err, &blocked) || blocked.Provider != providers.ClaudeCode || !strings.Contains(err.Error(), ".claude/settings.local.json") {
		t.Fatalf("the start of the Grand Master = %v; want the refusal of claude-code with the problem", err)
	}
}
