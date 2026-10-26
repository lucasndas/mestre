package conformance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/machinesheet"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/store"
)

// Outcome is how a step ended.
type Outcome string

// Outcomes. NotApplicable is a step the provider has nothing for, or one a
// later milestone brings (the AI diagnosis of step 11), with the reason.
const (
	Passed        Outcome = "passed"
	Failed        Outcome = "failed"
	NotApplicable Outcome = "not_applicable"
)

// Steps are the twelve steps of docs/provedores/conformidade.md#etapas.
const (
	StepReady         = 1
	StepInstructions  = 2
	StepSkill         = 3
	StepDeniedWrite   = 4
	StepCommandCommit = 5
	StepDegraded      = 6
	StepUsage         = 7
	StepCompactEnd    = 8
	StepProbeSkill    = 9
	StepResume        = 10
	StepDiagnosis     = 11
	StepSubagent      = 12
)

// notRun starts the reason of a step that did not run (after a failed
// step 1): it switches nothing on.
const notRun = "não rodou"

// blocking are the steps whose failure makes the provider not conformant on
// the machine and profile (docs/provedores/conformidade.md#resultado).
var blocking = []int{StepReady, StepInstructions, StepDeniedWrite, StepDegraded}

// StepResult is one step of a run.
type StepResult struct {
	Step     int           `json:"step"`
	Outcome  Outcome       `json:"outcome"`
	Reason   string        `json:"reason,omitempty"`
	Duration time.Duration `json:"duration"`
	// Transcript is the masked screen of the terminal at the end of the
	// step.
	Transcript string `json:"transcript,omitempty"`
	// Answer is the answer of the model the verdict read (the last message
	// of the turn, or the screen after the question), masked.
	Answer string `json:"answer,omitempty"`
	// Notes are what the step observed for the items of "Verificar na
	// implementação" (a native screen, a tool name, a path field).
	Notes []string `json:"notes,omitempty"`
	// CLIUnreachable (step 5): `mestre debug` did not reach the engine
	// identified with the terminal.
	CLIUnreachable bool `json:"cli_unreachable,omitempty"`
}

// Flags are what a result switches on beyond the conformance itself.
type Flags struct {
	// AgentsSkillsRead: Claude Code already reads .agents/skills/ (step 9);
	// the managed copy in .claude/skills/ may be turned off.
	AgentsSkillsRead bool `json:"agents_skills_read,omitempty"`
	// ResumeReborn: the resume failed (step 10); the characters of the
	// provider are reborn instead.
	ResumeReborn bool `json:"resume_reborn,omitempty"`
	// DenySubagent: the hooks did not hold inside a native subagent (step
	// 12); on this machine the classes deny the subagent tool of the
	// provider to the character itself.
	DenySubagent bool `json:"deny_subagent,omitempty"`
	// NoDiagnosis: the AI diagnosis does not use the provider here (step 11).
	NoDiagnosis bool `json:"no_diagnosis,omitempty"`
	// CLIUnreachable: the `mestre` CLI, called by the shell tool of the
	// provider, did not reach the engine identified as the terminal (step 5).
	// The provider stays conformant (the hooks work), but a class that calls
	// the CLI is not recruited on this profile and the checklist shows it.
	CLIUnreachable bool `json:"cli_unreachable,omitempty"`
}

// Origin is who asked for the run.
type Origin string

// Origins (docs/provedores/conformidade.md#quando-roda).
const (
	OriginProfileSetup Origin = "profile_setup" // right after the login of a profile
	OriginCLIUpdate    Origin = "cli_update"    // the version of the CLI changed in a profile
	OriginHeavy        Origin = "heavy"         // the heavy suite, nightly or on demand
	OriginManual       Origin = "manual"        // a person on the machine
)

// Result is the result of one run, for one machine, profile, provider and
// version.
type Result struct {
	Machine  string       `json:"machine"`
	Profile  string       `json:"profile"`
	Provider providers.ID `json:"provider"`
	Version  string       `json:"version"`
	Origin   Origin       `json:"origin"`
	// Conformant is false when step 1, 2, 4 or 6 failed or an event the map
	// requires never arrived; Reason says why.
	Conformant bool         `json:"conformant"`
	Reason     string       `json:"reason,omitempty"`
	Steps      []StepResult `json:"steps"`
	// MissingEvents are the events of the map that never arrived, or
	// arrived without the fields of the internal format.
	MissingEvents []string `json:"missing_events,omitempty"`
	Flags         Flags    `json:"flags"`
	// Notes are what the run observed outside the steps (a trust screen
	// that came back, temporary files outside the TMPDIR).
	Notes      []string  `json:"notes,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// Step is the result of step n (the zero StepResult when it did not run).
func (r Result) Step(n int) StepResult {
	for _, s := range r.Steps {
		if s.Step == n {
			return s
		}
	}
	return StepResult{Step: n}
}

// DenyTools are the native tools the effective rules of the characters of
// this provider deny on this machine, because of the result: the subagent
// tools when step 12 failed (Flags.DenySubagent), and nothing otherwise. The
// plan B holds for the machine, profile and version of the result, until the
// run of a new version replaces it (the CLI update asks for it); it goes to
// classes.Layers.DenyTools.
func (r Result) DenyTools() []string {
	if !r.Flags.DenySubagent {
		return nil
	}
	return providers.SubagentTools(r.Provider)
}

// decide fills Conformant, Reason and Flags from the steps and the events.
func (r *Result) decide() {
	var why []string
	for _, n := range blocking {
		if s := r.Step(n); s.Outcome != Passed && s.Outcome != NotApplicable {
			why = append(why, fmt.Sprintf("etapa %d: %s", n, orNotRun(s)))
		}
	}
	if len(r.MissingEvents) > 0 {
		why = append(why, "eventos ausentes: "+strings.Join(r.MissingEvents, ", "))
	}
	r.Conformant, r.Reason = len(why) == 0, strings.Join(why, "; ")
	ran := func(n int) bool { s := r.Step(n); return s.Outcome == Failed && !strings.HasPrefix(s.Reason, notRun) }
	r.Flags.ResumeReborn = ran(StepResume)
	r.Flags.DenySubagent = ran(StepSubagent)
	r.Flags.NoDiagnosis = ran(StepDiagnosis)
	r.Flags.CLIUnreachable = r.Step(StepCommandCommit).CLIUnreachable
}

func orNotRun(s StepResult) string {
	if s.Outcome == "" {
		return "não rodou"
	}
	if s.Reason == "" {
		return string(s.Outcome)
	}
	return s.Reason
}

// Results keeps the results (docs/arquitetura/persistencia.md:
// conformance_results).
type Results interface {
	// Save keeps r in place of the previous result of its machine, profile
	// and provider.
	Save(ctx context.Context, r Result) error
	// Latest is the result of a machine, profile and provider.
	Latest(ctx context.Context, machine, profile string, p providers.ID) (Result, bool, error)
}

// ResultsDB keeps the results in the Guild database.
type ResultsDB struct{ DB *store.DB }

var _ Results = ResultsDB{}

// Save implements Results.
func (d ResultsDB) Save(ctx context.Context, r Result) error {
	steps, err := json.Marshal(r.Steps)
	if err != nil {
		return err
	}
	flags, err := json.Marshal(r.Flags)
	if err != nil {
		return err
	}
	return d.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO conformance_results
			(machine, profile, provider, version, conformant, reason, origin, steps, flags, started_at, finished_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (machine, profile, provider) DO UPDATE SET version = excluded.version,
			conformant = excluded.conformant, reason = excluded.reason, origin = excluded.origin, steps = excluded.steps,
			flags = excluded.flags, started_at = excluded.started_at, finished_at = excluded.finished_at`,
			r.Machine, r.Profile, string(r.Provider), r.Version, r.Conformant, r.Reason, string(r.Origin),
			string(steps), string(flags), r.StartedAt.UTC().UnixMilli(), r.FinishedAt.UTC().UnixMilli())
		if err != nil {
			return fmt.Errorf("save the conformance of %s/%s/%s: %w", r.Machine, r.Profile, r.Provider, err)
		}
		return nil
	})
}

// Latest implements Results.
func (d ResultsDB) Latest(ctx context.Context, machine, profile string, p providers.ID) (Result, bool, error) {
	var (
		r                    = Result{Machine: machine, Profile: profile, Provider: p}
		origin, steps, flags string
		started, finished    int64
	)
	err := d.DB.Reader().QueryRowContext(ctx, `SELECT version, conformant, reason, origin, steps, flags, started_at, finished_at
		FROM conformance_results WHERE machine = ? AND profile = ? AND provider = ?`, machine, profile, string(p)).
		Scan(&r.Version, &r.Conformant, &r.Reason, &origin, &steps, &flags, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, fmt.Errorf("conformance of %s/%s/%s: %w", machine, profile, p, err)
	}
	if err := json.Unmarshal([]byte(steps), &r.Steps); err != nil {
		return Result{}, false, fmt.Errorf("conformance of %s/%s/%s: steps: %w", machine, profile, p, err)
	}
	if err := json.Unmarshal([]byte(flags), &r.Flags); err != nil {
		return Result{}, false, fmt.Errorf("conformance of %s/%s/%s: flags: %w", machine, profile, p, err)
	}
	r.Origin = Origin(origin)
	r.StartedAt, r.FinishedAt = time.UnixMilli(started).UTC(), time.UnixMilli(finished).UTC()
	return r, true, nil
}

// Probe is the provider part of the machine sheet with the conformance read
// from the results: a profile is conformant only when its latest result is
// conformant and is of the version installed now (version and conformance
// are always per machine and profile). The `provider` requirement of the
// machine choice reads it (docs/guilda/ficha-da-maquina.md#requisitos).
type Probe struct {
	Machine string
	// Installed reads the CLIs and the profiles of the machine (the
	// versions and the logins); its Conformant is ignored.
	Installed machinesheet.ProviderProbe
	Results   Results
}

var _ machinesheet.ProviderProbe = Probe{}

// Providers implements machinesheet.ProviderProbe.
func (p Probe) Providers(ctx context.Context) ([]machinesheet.Provider, error) {
	ps, err := p.Installed.Providers(ctx)
	if err != nil {
		return nil, err
	}
	out := slices.Clone(ps)
	for i := range out {
		out[i].Profiles = slices.Clone(out[i].Profiles)
		for j := range out[i].Profiles {
			pr := &out[i].Profiles[j]
			r, ok, err := p.Results.Latest(ctx, p.Machine, pr.Name, providers.ID(out[i].Provider))
			if err != nil {
				return nil, err
			}
			pr.Conformant = ok && r.Conformant && r.Version == pr.Version
			pr.CLIUnreachable = ok && r.Flags.CLIUnreachable && r.Version == pr.Version
		}
	}
	return out, nil
}
