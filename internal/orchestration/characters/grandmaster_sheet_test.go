package characters_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mestre-ia/mestre/internal/machinesheet"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/presets"
)

// sheetsOnly is the part of the presets the sheet of the Grand Master reads.
type sheetsOnly struct {
	characters.Presets
	snaps    map[string]presets.Snapshot
	machines []machinesheet.Sheet
}

func (s sheetsOnly) Snapshot(_ context.Context, sheet string) (presets.Snapshot, error) {
	snap, ok := s.snaps[sheet]
	if !ok {
		return presets.Snapshot{}, errors.New("no sheet")
	}
	return snap, nil
}

func (s sheetsOnly) Machines(context.Context) ([]machinesheet.Sheet, error) { return s.machines, nil }

// The sheet of the Grand Master runs an agent, never on the base of the Operator of
// the PC, and its base runs on every machine the Owner did not exclude, because any
// of them may become the Citadel (docs/orquestracao/grao-mestre.md#onde-roda).
func TestTheSheetOfTheGrandMasterIsAnAgentThatRunsOnEveryMachineNotExcluded(t *testing.T) {
	t.Parallel()
	both := []presets.Platform{{OS: "linux"}, {OS: "windows"}}
	snap := func(agent, base string, platforms []presets.Platform) presets.Snapshot {
		return presets.Snapshot{Sheet: presets.Sheet{Name: "s", Agent: agent}, Base: presets.Base{Name: base, Platforms: platforms}}
	}
	p := sheetsOnly{
		snaps: map[string]presets.Snapshot{
			"ok":       snap(presets.AgentClaudeCode, "claude-code", both),
			"shell":    snap(presets.AgentShell, "bash", both),
			"operator": snap(presets.AgentClaudeCode, presets.OperatorBase, both),
			"linux":    snap(presets.AgentClaudeCode, "so-linux", []presets.Platform{{OS: "linux"}}),
		},
		machines: []machinesheet.Sheet{
			{MachineID: "m1", Name: "Cidadela", OS: "linux"},
			{MachineID: "m2", Name: "windows-pc", OS: "windows"},
			{MachineID: "m3", Name: "mac", OS: "darwin", Excluded: true},
		},
	}
	ctx := t.Context()
	if err := characters.GrandMasterSheetProblem(ctx, p, "ok"); err != nil {
		t.Errorf("a good sheet: %v", err)
	}
	for _, sheet := range []string{"", "shell", "operator", "linux"} {
		if err := characters.GrandMasterSheetProblem(ctx, p, sheet); !errors.Is(err, characters.ErrBadGrandMasterSheet) {
			t.Errorf("sheet %q = %v, want ErrBadGrandMasterSheet", sheet, err)
		}
	}
	if err := characters.GrandMasterSheetProblem(ctx, p, "nao-existe"); !errors.Is(err, characters.ErrBadSheet) {
		t.Errorf("an unknown sheet = %v, want ErrBadSheet", err)
	}
	// the machine that is excluded does not count: without the Windows machine, a base for Linux serves
	p.machines = p.machines[:1]
	if err := characters.GrandMasterSheetProblem(ctx, p, "linux"); err != nil {
		t.Errorf("a base for Linux in a Guild of Linux: %v", err)
	}
}
