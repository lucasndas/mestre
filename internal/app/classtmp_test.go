package app

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/sessionzero"
)

// The production composition tells `mestre class show --export` the MESTRE_TMP of
// the character that asks: the one the session zero gives it.
//
// Part of CA-CLI-CLR-10: the export folder is the real one of the character.
func TestTheProductionCompositionGivesClassShowTheTmpOfTheCharacterThatAsks(t *testing.T) {
	t.Parallel()
	i := newInteg(t, false)
	root := t.TempDir()
	e := &Engine{clk: i.clk, log: slog.New(slog.DiscardHandler), id: guild.Identity{MachineID: "m1"},
		paths: Paths{Workspaces: filepath.Join(root, "workspaces"), Maquina: filepath.Join(root, "maquina"),
			Agentes: filepath.Join(root, "agentes"), ClassesGlobais: filepath.Join(root, "classes-globais")},
		deps: Deps{Masker: logging.NewMasker(), Binary: filepath.Join(root, "mestre")}}
	run := i.open()
	t.Cleanup(run.Close)
	orc, err := e.hostOrchestration(i.db, run)
	if err != nil {
		t.Fatal(err)
	}
	if orc.ClassCommands == nil || orc.ClassCommands.Tmp == nil {
		t.Fatalf("class commands: %+v", orc.ClassCommands)
	}
	got, err := orc.ClassCommands.Tmp(t.Context(), classes.Caller{Character: "c-1"})
	if err != nil {
		t.Fatal(err)
	}
	if want := sessionzero.CharacterTmp(filepath.Join(root, "agentes"), "c-1"); got != want {
		t.Errorf("MESTRE_TMP = %q, want %q", got, want)
	}
}
