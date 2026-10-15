package chain

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/lucasndas/mestre/internal/classes"
)

// Settings is the section `cadeia` of the approval policy file
// (docs/aprovacoes/arquivo-de-politica.md): what the Owner may change of the
// chain. None of it moves the ceiling of a level (baixo, medio) nor the
// conditions of a group, which are the engine's.
type Settings struct {
	// AgentDeadline is the time of the agent superior and of the Mestre da
	// mesa; GrandMasterDeadline the Grão-Mestre's.
	AgentDeadline, GrandMasterDeadline time.Duration
	// PeopleDeadline is the time of the people (the file's `expiracao`).
	PeopleDeadline time.Duration
	// TempRuleMax is the longest validity of a temporary table rule.
	TempRuleMax time.Duration
	// MesaDecides are the groups of medio the Mestre da mesa decides.
	MesaDecides []string
	// DenialsToPause is the number of denials in a row that pause a
	// character.
	DenialsToPause int
}

// DefaultSettings is the default content of the section.
func DefaultSettings() Settings {
	return Settings{AgentDeadline: 2 * time.Minute, GrandMasterDeadline: 10 * time.Minute, PeopleDeadline: 24 * time.Hour,
		TempRuleMax: 24 * time.Hour, MesaDecides: slices.Clone(classes.DefaultMesaGroups), DenialsToPause: 3}
}

func (s Settings) withDefaults() Settings {
	d := DefaultSettings()
	if s.AgentDeadline <= 0 {
		s.AgentDeadline = d.AgentDeadline
	}
	if s.GrandMasterDeadline <= 0 {
		s.GrandMasterDeadline = d.GrandMasterDeadline
	}
	if s.PeopleDeadline <= 0 {
		s.PeopleDeadline = d.PeopleDeadline
	}
	if s.TempRuleMax <= 0 {
		s.TempRuleMax = d.TempRuleMax
	}
	if s.MesaDecides == nil {
		s.MesaDecides = d.MesaDecides
	}
	if s.DenialsToPause <= 0 {
		s.DenialsToPause = d.DenialsToPause
	}
	return s
}

// MesaDecide says whether the Mestre da mesa decides the group.
func (s Settings) MesaDecide(group string) bool { return slices.Contains(s.MesaDecides, group) }

// ErrInvalidSection wraps every error of an invalid cadeia section: the whole
// file is refused, the last valid version holds and the Owner is told.
var ErrInvalidSection = errors.New("invalid cadeia section")

// SectionError is an invalid item of the section, with the reason.
type SectionError struct{ Item, Reason string }

func (e *SectionError) Error() string {
	return fmt.Sprintf("%v: %s: %s", ErrInvalidSection, e.Item, e.Reason)
}

// Unwrap lets errors.Is find ErrInvalidSection.
func (e *SectionError) Unwrap() error { return ErrInvalidSection }

type sectionYAML struct {
	PrazoAgente        string   `yaml:"prazo_agente"`
	PrazoGraoMestre    string   `yaml:"prazo_grao_mestre"`
	RegraTemporariaMax string   `yaml:"regra_temporaria_max"`
	MesaDecide         []string `yaml:"mesa_decide"`
	NegacoesParaPausar int      `yaml:"negacoes_para_pausar"`
	ResumoMesas        string   `yaml:"resumo_mesas"`
	AlertaFila         any      `yaml:"alerta_fila"`
}

// ParseSection reads and checks the cadeia section (the YAML of the section's
// value). Absent keys are the default; `mesa_decide` names only the groups of
// medio the Mestre da mesa may decide, so `alto`, `fixo`, `git_push` or an
// unknown name make the whole section invalid with the item named; the
// durations are positive and the temporary rule goes up to 7 days;
// negacoes_para_pausar goes from 1 to 10.
func ParseSection(data []byte, expiry time.Duration) (Settings, error) {
	s := DefaultSettings()
	if expiry > 0 {
		s.PeopleDeadline = expiry
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var y sectionYAML
	if err := dec.Decode(&y); err != nil && !errors.Is(err, io.EOF) {
		return Settings{}, &SectionError{Item: "cadeia", Reason: err.Error()}
	}
	dur := func(item, v string, max time.Duration, dst *time.Duration) error {
		if v == "" {
			return nil
		}
		d, err := time.ParseDuration(v)
		switch {
		case err != nil || d <= 0:
			return &SectionError{Item: item, Reason: "needs a positive duration like 2m or 10m"}
		case max > 0 && d > max:
			return &SectionError{Item: item, Reason: fmt.Sprintf("goes up to %s", max)}
		}
		*dst = d
		return nil
	}
	if err := dur("cadeia.prazo_agente", y.PrazoAgente, 0, &s.AgentDeadline); err != nil {
		return Settings{}, err
	}
	if err := dur("cadeia.prazo_grao_mestre", y.PrazoGraoMestre, 0, &s.GrandMasterDeadline); err != nil {
		return Settings{}, err
	}
	if err := dur("cadeia.regra_temporaria_max", y.RegraTemporariaMax, 7*24*time.Hour, &s.TempRuleMax); err != nil {
		return Settings{}, err
	}
	if y.NegacoesParaPausar != 0 {
		if y.NegacoesParaPausar < 1 || y.NegacoesParaPausar > 10 {
			return Settings{}, &SectionError{Item: "cadeia.negacoes_para_pausar", Reason: "goes from 1 to 10"}
		}
		s.DenialsToPause = y.NegacoesParaPausar
	}
	if y.MesaDecide != nil {
		var groups []string
		for _, g := range y.MesaDecide {
			if !slices.Contains(classes.MesaGroups, g) {
				return Settings{}, &SectionError{Item: "cadeia.mesa_decide: " + g, Reason: "is not a group of medio the Mestre da mesa can decide"}
			}
			if !slices.Contains(groups, g) {
				groups = append(groups, g)
			}
		}
		if groups == nil {
			groups = []string{}
		}
		s.MesaDecides = groups
	}
	return s, nil
}
