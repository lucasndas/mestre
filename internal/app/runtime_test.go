package app

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
)

// Part of CA-CIDADELA-16, the half without `terminais`: a machine whose
// configuration does not wire the terminals starts no character and says why;
// another machine is still the one that cannot be reached.
func TestAMachineWithoutTerminalsRefusesTheStartWithTheReason(t *testing.T) {
	t.Parallel()
	e := &Engine{id: guild.Identity{MachineID: "m1"}}
	m := e.machines(nil)
	_, err := m.Start(t.Context(), "m1", characters.StartCommand{Character: "c1"})
	if !errors.Is(err, errTerminalsNotWired) || !strings.Contains(err.Error(), "terminais") {
		t.Fatalf("start on the machine without terminals: %v", err)
	}
	var unreachable *characters.MachineUnreachableError
	if _, err := m.Start(t.Context(), "m2", characters.StartCommand{Character: "c1"}); !errors.As(err, &unreachable) {
		t.Fatalf("start on another machine: %v", err)
	}
}

// Part of CA-CIDADELA-16: the folder and the version of the mestre-pty that
// the installation brings are checked when the configuration is read, and the
// path of a version cannot leave the folder.
func TestTheTerminalsOfTheConfigurationAreChecked(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	good := Config{Instance: "principal", Name: "m", EnginePort: 7443, Terminais: &Terminals{Dir: dir, Version: "1.2.3", Socket: filepath.Join(dir, "l.sock")}}
	if err := good.check(); err != nil {
		t.Fatalf("a good configuration: %v", err)
	}
	b, err := json.Marshal(good)
	if err != nil {
		t.Fatal(err)
	}
	var back Config
	if err := json.Unmarshal(b, &back); err != nil || back.Terminais == nil || *back.Terminais != *good.Terminais {
		t.Fatalf("the terminals do not survive the file: %+v, %v", back.Terminais, err)
	}
	for name, term := range map[string]*Terminals{
		"relative folder": {Dir: "pty", Version: "1.2.3"},
		"no version":      {Dir: dir},
		"no folder":       {Version: "1.2.3"},
	} {
		c := good
		c.Terminais = term
		if err := c.check(); err == nil {
			t.Errorf("%s: the configuration was accepted", name)
		}
	}
	for _, v := range []string{"../x", "..", "."} {
		if _, err := good.Terminais.ptyBinary(v); err == nil {
			t.Errorf("the version %q was accepted", v)
		}
	}
	if p, err := good.Terminais.ptyBinary("1.2.3"); err != nil || filepath.Dir(filepath.Dir(p)) != dir {
		t.Errorf("the mestre-pty of a version: %q, %v", p, err)
	}
}

// Part of CA-CIDADELA-16: the Tower writes the files of a character only in the
// workspaces of its own machine.
func TestATowerRefusesAWorktreeOutsideItsWorkspaces(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	e := &Engine{paths: Paths{Workspaces: filepath.Join(root, "workspaces")}}
	for dir, ok := range map[string]bool{
		filepath.Join(root, "workspaces", "meu-app", "t1"): true,
		filepath.Join(root, "workspaces"):                  false,
		filepath.Join(root, "workspaces", "..", "x"):       false,
		filepath.Join(root, "outro"):                       false,
		"relativo":                                         false,
	} {
		if err := e.towerWorktree(dir); (err == nil) != ok {
			t.Errorf("%s: %v", dir, err)
		}
	}
	if err := (&Engine{}).towerWorktree(filepath.Join(root, "x")); err == nil {
		t.Error("a machine with no folder of workspaces took a worktree")
	}
}
