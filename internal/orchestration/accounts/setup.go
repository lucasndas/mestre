package accounts

import (
	"context"
	"fmt"

	"github.com/mestre-ia/mestre/internal/machinesheet"
	"github.com/mestre-ia/mestre/internal/providers"
)

// Terminal is a terminal the panel opens for the Owner, as the agents'
// account (on Windows with the profile of that account loaded): the login
// of the provider, or the Codex's approval of the hooks.
type Terminal struct {
	ID string
	// Hint says what to do there, e.g. `codex login --device-auth`: the
	// Owner finishes a link or code login in their own browser.
	Hint string
}

// Steps are what the setup does on the machine; the engine implements them
// with the installer of the provider, the terminals and the conformance
// test, and the tests with a fake.
type Steps interface {
	// CLIVersion is the version of the CLI installed in the home of the
	// profile, "" when it is not there.
	CLIVersion(ctx context.Context, p Profile) (string, error)
	// InstallCLI runs the provider's official installer in the home of the
	// profile (docs/plataforma/instalacao-e-atualizacao.md#clis-dos-provedores).
	InstallCLI(ctx context.Context, p Profile) error
	LoggedIn(ctx context.Context, p Profile) (bool, error)
	// OpenTerminal opens the login terminal of the profile, or, with
	// hooks, the one where the Owner approves the Mestre's hooks (Codex,
	// /hooks).
	OpenTerminal(ctx context.Context, p Profile, hooks bool) (Terminal, error)
	// HooksTrusted says whether the Mestre's hooks are approved in the
	// profile (Codex).
	HooksTrusted(ctx context.Context, p Profile) (bool, error)
	// Conformance runs the conformance test of the provider in this profile
	// and says whether it passed, and if not, why.
	Conformance(ctx context.Context, p Profile, version string) (ok bool, reason string, err error)
}

// Setup is the guided setup of a profile, once per machine
// (docs/orquestracao/bases-e-fichas.md#perfis-de-conta): the CLI installed,
// the login, for the Codex the approval of the hooks, and the conformance.
type Setup struct {
	Registry *Registry
	Steps    Steps
}

// Status is where the setup stands after Advance.
type Status struct {
	Step Step
	Done bool
	// Waiting is what the setup needs from the Owner; Terminal is the
	// terminal opened for it.
	Waiting  string
	Terminal *Terminal
	// Conformant and Reason are the result of the last conformance run.
	Conformant bool
	Reason     string
}

// Advance goes as far as the profile can: it installs the CLI when it is
// missing, then stops at the login (opening the terminal) until the Owner
// has logged in, then at the Codex's approval of the hooks, and ends with the
// conformance. It can be called again at any time, from where it stopped; each
// step is looked at, not assumed. A profile whose conformance failed stays at
// that step with the reason, and the next call runs it again.
func (s *Setup) Advance(ctx context.Context, name string) (Status, error) {
	p, ok, err := s.Registry.Profile(ctx, name)
	switch {
	case err != nil:
		return Status{}, err
	case !ok:
		return Status{}, fmt.Errorf("%w: `%s`", ErrUnknown, name)
	}
	if p.Step == StepDone {
		return Status{Step: StepDone, Done: true, Conformant: true}, nil
	}
	for {
		st, next, err := s.step(ctx, p)
		if err != nil {
			return Status{Step: p.Step}, err
		}
		if next == p.Step {
			if err := s.Registry.setStep(ctx, name, p.Step, st.Waiting+st.Reason); err != nil {
				return Status{}, err
			}
			st.Step = p.Step
			return st, nil
		}
		p.Step = next
		if err := s.Registry.setStep(ctx, name, next, ""); err != nil {
			return Status{}, err
		}
		if next == StepDone {
			return Status{Step: StepDone, Done: true, Conformant: true}, nil
		}
	}
}

func (s *Setup) step(ctx context.Context, p Profile) (Status, Step, error) {
	switch p.Step {
	case StepInstall:
		v, err := s.Steps.CLIVersion(ctx, p)
		if err != nil {
			return Status{}, p.Step, err
		}
		if v == "" {
			if err := s.Steps.InstallCLI(ctx, p); err != nil {
				return Status{}, p.Step, fmt.Errorf("install the CLI of %s: %w", p.Provider, err)
			}
			if v, err = s.Steps.CLIVersion(ctx, p); err != nil || v == "" {
				return Status{}, p.Step, fmt.Errorf("the installer of %s left no CLI in %s: %v", p.Provider, p.Dir, err)
			}
		}
		return Status{}, StepLogin, nil
	case StepLogin:
		in, err := s.Steps.LoggedIn(ctx, p)
		if err != nil {
			return Status{}, p.Step, err
		}
		if !in {
			t, err := s.Steps.OpenTerminal(ctx, p, false)
			if err != nil {
				return Status{}, p.Step, err
			}
			return Status{Waiting: "login na conta do provedor, no terminal aberto", Terminal: &t}, p.Step, nil
		}
		if p.Provider == providers.Codex {
			return Status{}, StepHooks, nil
		}
		return Status{}, StepConformance, nil
	case StepHooks:
		ok, err := s.Steps.HooksTrusted(ctx, p)
		if err != nil {
			return Status{}, p.Step, err
		}
		if !ok {
			t, err := s.Steps.OpenTerminal(ctx, p, true)
			if err != nil {
				return Status{}, p.Step, err
			}
			return Status{Waiting: "aprovar os hooks do Mestre em /hooks, no terminal aberto", Terminal: &t}, p.Step, nil
		}
		return Status{}, StepConformance, nil
	case StepConformance:
		v, err := s.Steps.CLIVersion(ctx, p)
		if err != nil {
			return Status{}, p.Step, err
		}
		ok, reason, err := s.Steps.Conformance(ctx, p, v)
		switch {
		case err != nil:
			return Status{}, p.Step, err
		case !ok:
			return Status{Reason: reason}, p.Step, nil
		}
		return Status{Conformant: true}, StepDone, nil
	}
	return Status{Done: true, Conformant: true}, StepDone, nil
}

// Redo takes the profile back to the first step, e.g. after the login was
// lost.
func (s *Setup) Redo(ctx context.Context, name string) error {
	if _, ok, err := s.Registry.Profile(ctx, name); err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("%w: `%s`", ErrUnknown, name)
		}
		return err
	}
	return s.Registry.setStep(ctx, name, StepInstall, "")
}

// CLI is what the machine knows about the CLI of a profile, to tell the
// machine sheet.
type CLI interface {
	CLIVersion(ctx context.Context, p Profile) (string, error)
	LoggedIn(ctx context.Context, p Profile) (bool, error)
}

// Probe is the machinesheet.ProviderProbe over the profiles of the machine:
// the providers with a CLI installed, and for each profile its version and
// login. Conformant is not set here: conformance.Probe wraps this one and
// reads it from the results of the test.
type Probe struct {
	Registry *Registry
	CLI      CLI
}

var _ machinesheet.ProviderProbe = Probe{}

// Providers implements machinesheet.ProviderProbe.
func (pr Probe) Providers(ctx context.Context) ([]machinesheet.Provider, error) {
	all, err := pr.Registry.Profiles(ctx)
	if err != nil {
		return nil, err
	}
	byProvider := map[providers.ID][]machinesheet.Profile{}
	installed := map[providers.ID]bool{}
	var order []providers.ID
	for _, p := range all {
		v, err := pr.CLI.CLIVersion(ctx, p)
		if err != nil {
			return nil, fmt.Errorf("version of %s: %w", p.Name, err)
		}
		in := false
		if v != "" {
			if in, err = pr.CLI.LoggedIn(ctx, p); err != nil {
				return nil, fmt.Errorf("login of %s: %w", p.Name, err)
			}
			installed[p.Provider] = true
		}
		if _, seen := byProvider[p.Provider]; !seen {
			order = append(order, p.Provider)
		}
		byProvider[p.Provider] = append(byProvider[p.Provider], machinesheet.Profile{Name: p.Name, Version: v, LoggedIn: in, Default: p.Default})
	}
	var out []machinesheet.Provider
	for _, id := range order {
		if installed[id] {
			out = append(out, machinesheet.Provider{Provider: string(id), Profiles: byProvider[id]})
		}
	}
	return out, nil
}
