//go:build full

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The wiring of `mestre run`: a Grand Master the registry has interrupted is
// started again by the Keeper the period runs (the Runtime of the machine
// answers the start; the start fails here for lack of the people of the chain,
// which is not what is looked at: the attempt, recorded in the history of the
// states, is). Without the loop in host.go nobody tries.
func TestMestreRunKeepsAnInterruptedGrandMasterUpWhileTheCitadelActs(t *testing.T) {
	t.Parallel()
	buildRuntimeBinaries(t)
	r := newRig(t)
	root, err := os.MkdirTemp("", "mrk")
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
	}
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act and host the orchestration", func() bool { return a.acting() && a.e.orc.Load() != nil })
	orc := a.e.orc.Load()
	ctx := t.Context()
	if err := a.e.withGuildDB(func(db *store.DB) error {
		_, err := db.Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
			VALUES ('meu-app', 'lucas/meu-app', 'lucas/meu-app', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	gm := characters.Character{
		ID: "gm1", Name: characters.NameGrandMaster, Kind: characters.KindCharacter, Role: characters.RoleGrandMaster, Workspace: "meu-app", Machine: a.id,
		Sheet: "Grão-Mestre", Base: "claude-code", Class: "Grão-Mestre", Dir: filepath.Join(root, "principal"), State: characters.Interrupted, Reason: "the process died",
		CreatedBy: characters.Creator{Kind: characters.CreatedByEngine}, Lifecycle: characters.Persistent, Pinned: true,
	}
	if err := orc.Registry.Insert(ctx, gm); err != nil {
		t.Fatal(err)
	}
	r.waitFor("the Keeper of the period to try to start the Grand Master again", func() bool {
		log, err := orc.Registry.Log(ctx, gm.ID)
		if err != nil {
			return false
		}
		for _, l := range log {
			if l.To == characters.Starting && l.Reason == "resume" {
				return true
			}
		}
		return false
	})
}
