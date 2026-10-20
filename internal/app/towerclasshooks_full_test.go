//go:build full

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/presets"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/scrolls"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Part of CA-CLASSES-04 on a Tower (the Citadel half is in classhooks_full_test.go): the hooks of the
// class run for a character of a Tower as they do for one of the Citadel. The hooks come
// with the effective rules the Citadel sends (the Tower has no library of classes). A
// synchronous blocking hook of pre_tool denies, in the `mestre hook` of the Tower, the write
// it is about, and not the other; an asynchronous hook of turn_end runs in the terminal of
// the character on the Tower, as the character, and its output reaches the scroll of the
// mission, which lives in the Citadel.
func TestTheHooksOfTheClassRunForACharacterOfATowerAndTheOutputReachesTheCitadel(t *testing.T) {
	t.Parallel()
	x := newChannelRigWith(t, channelOpts{chain: true, towerHosts: true})
	ctx := t.Context()
	const table, branch = "4-busca", "mestre/4-busca"
	x.seedTable(t, table, branch, 4)
	home := filepath.Join(x.root, "home")
	script := "prompts:\n" +
		"  - match: \"(?s).*PASSO-UM.*\"\n    steps:\n" +
		"      - edit: {path: proibido.txt, content: \"não\\n\"}\n" +
		"      - edit: {path: permitido.txt, content: \"sim\\n\"}\n" +
		"      - say: \"turno feito\"\n"
	if err := os.WriteFile(filepath.Join(home, "quick.yaml"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	hooks := "pre_tool:\n" +
		"  - {name: guarda, run: 'git no-such-subcommand', mode: sync, timeout: 30, blocking: true, when: {kind: [write], paths: [\"proibido.txt\"]}}\n" +
		"turn_end:\n" +
		"  - {name: onde, run: 'git rev-parse --show-toplevel', mode: async, timeout: 60, output: scroll}\n"
	approveClassOn(t, x.a, "meu-app", "dev", devClass("dev", hooks)...)

	now := clock.Real{}.Now()
	master := characters.Character{ID: "m4", Name: "Mestre", Kind: characters.KindCharacter, Role: characters.RoleTableMaster, Workspace: "meu-app", Table: table, Machine: x.a.id,
		Sheet: "Dev Claude", Base: "claude-code", Class: "dev", State: characters.Idle, CreatedBy: characters.Creator{Kind: characters.CreatedByEngine}, Lifecycle: characters.Persistent, CreatedAt: now}
	if err := x.orc.Registry.Insert(ctx, master); err != nil {
		t.Fatal(err)
	}
	def := "name: Busca\nsteps:\n  - {id: um, sheet: Dev Claude, machine: b, instructions: \"PASSO-UM: escreva os arquivos\"}\n"
	v, err := x.orc.Quests.Create(ctx, quests.CreateRequest{Workspace: "meu-app", Table: table, Data: []byte(def), Designer: master.ID})
	if err != nil {
		t.Fatal(err)
	}
	q, err := x.orc.Quests.Run(ctx, v.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	var dev characters.Character
	x.r.waitFor("the character of the step, on the Tower", func() bool {
		cs, err := x.orc.Registry.AliveOf(ctx, "meu-app")
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cs {
			if c.Quest == q.ID && c.Machine == x.b.id {
				dev = c
			}
		}
		return dev.ID != ""
	})
	towerTree := filepath.Join(x.b.paths.Workspaces, "meu-app", "mesas", table)

	// the allowed write is made and the forbidden one is denied by the hook of the class
	x.r.waitFor("the allowed write of the character on the Tower", func() bool {
		_, err := os.Stat(filepath.Join(towerTree, "permitido.txt"))
		return err == nil
	})
	x.r.waitFor("the turn to end", func() bool { return x.state(t, dev.ID) == characters.Idle })
	if _, err := os.Stat(filepath.Join(towerTree, "proibido.txt")); err == nil {
		t.Error("the blocking hook of the class did not deny the write on the Tower")
	}

	// the asynchronous hook ran in the worktree of the Tower, and the scroll of the Citadel has its output
	x.r.waitFor("the output of the hook, from the Tower, in the scroll of the mission", func() bool {
		_, text, err := x.orc.Scrolls.Read(ctx, scrolls.ReadRequest{Workspace: "meu-app", Table: table, Actor: scrolls.Engine, Name: scrolls.QuestScrollName(q.Name)})
		if err != nil {
			t.Fatal(err)
		}
		return strings.Contains(text, "## Hook onde — "+dev.Name) && strings.Contains(text, filepath.ToSlash(towerTree))
	})
}

// A character whose class has hooks is not started on a Tower that did not receive them with
// the effective rules: it fails closed (missing_prerequisite), never runs without them.
func TestACharacterWhoseClassHooksTheTowerDidNotReceiveIsNotStartedThere(t *testing.T) {
	t.Parallel()
	x := newChannelRig(t)
	c := x.character(t, "c1", "Dev-1", characters.Starting)
	class := &classes.Class{Name: "dev", Hash: "h1", Hooks: classes.Hooks{"turn_end": {{Name: "onde", Run: "git status", Mode: classes.ModeAsync, Timeout: 30, Output: classes.OutputLog}}}}
	req := sessionzero.CharacterRequest{
		ID: c.ID, Name: c.Name, Role: c.Role, Workspace: "meu-app", Table: sessionzero.TableContext{ID: "3-login"}, Machine: x.b.id, Dir: x.work, Class: class,
		Snapshot: presets.Snapshot{Sheet: presets.Sheet{Name: c.Sheet, Agent: "claude-code", Class: "dev"}, Base: presets.Base{Name: "claude-code", Ready: presets.Ready{Mode: presets.ReadyHook}}},
	}
	_, err := x.a.e.mach.Load().Start(t.Context(), x.b.id, characters.StartCommand{Character: c.ID, Terminal: "t-" + c.ID, Kind: characters.KindCharacter, Request: req})
	if err == nil || !strings.Contains(err.Error(), "did not receive them") {
		t.Fatalf("the start without the hooks on the Tower = %v, want the refusal", err)
	}
	if x.aliveOnTower(c.ID) {
		t.Error("a terminal of the character was made on the Tower without its hooks")
	}
}
