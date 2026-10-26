package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/mestre-ia/mestre/internal/machinesheet"
	"github.com/mestre-ia/mestre/internal/providers"
)

// CA-CONF-06
//
// Step 5 fails when `mestre debug` from the shell tool did not reach the
// engine identified with the terminal, and the result says so in the
// cli_unreachable flag; the provider stays conformant (the hooks work), and a
// step that passed switches nothing on.
func TestStepFiveFailsAndFlagsACLIThatDoesNotReachTheEngine(t *testing.T) {
	t.Parallel()
	ok := commandCommitFacts{committed: true, pre: true, post: true, identified: true}
	out, why, unreachable := commandCommitVerdict(ok)
	if out != Passed || why != "" || unreachable {
		t.Fatalf("reached: %v %q %v", out, why, unreachable)
	}
	lost := ok
	lost.identified = false
	out, why, unreachable = commandCommitVerdict(lost)
	if out != Failed || !unreachable || why == "" {
		t.Fatalf("unreachable: %v %q %v", out, why, unreachable)
	}
	// Another failure keeps its reason, and the flag still says what was seen.
	lost.committed = false
	if out, why, unreachable = commandCommitVerdict(lost); out != Failed || !unreachable || why != "o commit não foi criado no worktree" {
		t.Fatalf("no commit and unreachable: %v %q %v", out, why, unreachable)
	}

	res := Result{Provider: providers.ClaudeCode, Steps: []StepResult{
		{Step: StepReady, Outcome: Passed}, {Step: StepInstructions, Outcome: Passed}, {Step: StepDeniedWrite, Outcome: Passed},
		{Step: StepDegraded, Outcome: Passed}, {Step: StepCommandCommit, Outcome: Failed, CLIUnreachable: true},
	}}
	res.decide()
	if !res.Flags.CLIUnreachable || !res.Conformant {
		t.Fatalf("flags %+v, conformant %v: the flag is on and the provider stays conformant", res.Flags, res.Conformant)
	}
	res.Steps[4] = StepResult{Step: StepCommandCommit, Outcome: Passed}
	res.decide()
	if res.Flags.CLIUnreachable {
		t.Fatal("the flag stays on after a step that passed")
	}
}

type installedFake struct{}

func (installedFake) Providers(context.Context) ([]machinesheet.Provider, error) {
	return []machinesheet.Provider{{Provider: "claude", Profiles: []machinesheet.Profile{{Name: "p", Version: "2.1.0", LoggedIn: true}}}}, nil
}

type resultFake struct{ r Result }

func (f resultFake) Save(context.Context, Result) error { return errors.New("unused") }
func (f resultFake) Latest(context.Context, string, string, providers.ID) (Result, bool, error) {
	return f.r, true, nil
}

// CA-CONF-06 (the other half: the profile of the sheet of the machine carries
// the flag of the result of its version, and only of it).
func TestTheSheetOfTheMachineCarriesTheFlagOfTheResultOfTheVersion(t *testing.T) {
	t.Parallel()
	probe := func(version string) machinesheet.Profile {
		p := Probe{Machine: "m", Installed: installedFake{}, Results: resultFake{Result{
			Version: version, Conformant: true, Flags: Flags{CLIUnreachable: true}}}}
		ps, err := p.Providers(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return ps[0].Profiles[0]
	}
	if p := probe("2.1.0"); !p.CLIUnreachable || !p.Conformant {
		t.Errorf("same version: %+v", p)
	}
	if p := probe("2.0.0"); p.CLIUnreachable || p.Conformant {
		t.Errorf("another version: %+v", p)
	}
}
