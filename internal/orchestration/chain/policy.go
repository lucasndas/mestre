package chain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
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
	// PeopleDeadlines are the times of the people of the types that have one of
	// their own (`tipos.<tipo>.expiracao` of the policy file, which the loader of
	// the policy of milestone 5 fills in); the other types have PeopleDeadline.
	PeopleDeadlines map[classes.RequestType]time.Duration
	// TempRuleMax is the longest validity of a temporary table rule.
	TempRuleMax time.Duration
	// MesaDecides are the groups of medio the Mestre da mesa decides.
	MesaDecides []string
	// DenialsToPause is the number of denials in a row that pause a
	// character.
	DenialsToPause int
	// SummaryEvery is how often the Grão-Mestre gets the summary of the
	// decisions of the Mestres de mesa (cadeia.resumo_mesas).
	SummaryEvery time.Duration
	// QueueAlertOpen and QueueAlertFactor are the limits of the alert of a
	// queue: more open requests than the first, or the oldest past the
	// second times the deadline of the level (cadeia.alerta_fila).
	QueueAlertOpen, QueueAlertFactor int
	// Summary is the periodic summary of what the agents decided (the file's
	// `resumo`, docs/aprovacoes/resumo-e-sugestoes.md).
	Summary SummarySettings
}

// SummarySettings are the `resumo` of the policy file.
type SummarySettings struct {
	// Frequency is diario, semanal or desligado; At the "HH:MM" of the
	// workspace's time zone.
	Frequency string
	At        string
	// For are the people the summary goes to: `dono`, `administradores` or the
	// name of a user.
	For []string
	// Sample is how many approved actions of each level are drawn for review,
	// SuggestAfter how many approvals of one pattern in 7 days make a suggestion.
	Sample, SuggestAfter int
}

// The frequencies of the summary.
const (
	SummaryDaily  = "diario"
	SummaryWeekly = "semanal"
	SummaryOff    = "desligado"
)

// DefaultSummary is the content of the file when it says nothing of `resumo`.
func DefaultSummary() SummarySettings {
	return SummarySettings{Frequency: SummaryDaily, At: "18:00", For: []string{"dono"}, Sample: 5, SuggestAfter: 3}
}

type summaryYAML struct {
	Frequencia  string   `yaml:"frequencia"`
	Horario     string   `yaml:"horario"`
	Para        []string `yaml:"para"`
	Amostra     *int     `yaml:"amostra"`
	SugerirApos *int     `yaml:"sugerir_apos"`
}

// ParseSummary reads the `resumo` section: absent keys are the default; an
// unknown frequency, a time that is not HH:MM, an empty list of recipients or a
// number out of range (amostra 0 to 50, sugerir_apos 2 to 20) make the whole file invalid.
func ParseSummary(data []byte) (SummarySettings, error) {
	out := DefaultSummary()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var y summaryYAML
	if err := dec.Decode(&y); err != nil && !errors.Is(err, io.EOF) {
		return SummarySettings{}, &SectionError{Item: "resumo", Reason: err.Error()}
	}
	if y.Frequencia != "" {
		if !slices.Contains([]string{SummaryDaily, SummaryWeekly, SummaryOff}, y.Frequencia) {
			return SummarySettings{}, &SectionError{Item: "resumo.frequencia", Reason: "precisa de diario, semanal ou desligado"}
		}
		out.Frequency = y.Frequencia
	}
	if y.Horario != "" {
		if _, err := time.Parse("15:04", y.Horario); err != nil {
			return SummarySettings{}, &SectionError{Item: "resumo.horario", Reason: "precisa de HH:MM, como 18:00"}
		}
		out.At = y.Horario
	}
	if y.Para != nil {
		if len(y.Para) == 0 {
			return SummarySettings{}, &SectionError{Item: "resumo.para", Reason: "a lista vazia não manda o resumo a ninguém; use frequencia: desligado"}
		}
		for _, p := range y.Para {
			if strings.TrimSpace(p) == "" {
				return SummarySettings{}, &SectionError{Item: "resumo.para", Reason: "nome vazio"}
			}
		}
		out.For = slices.Clone(y.Para)
	}
	if a := y.Amostra; a != nil {
		if *a < 0 || *a > 50 {
			return SummarySettings{}, &SectionError{Item: "resumo.amostra", Reason: "vai de 0 a 50"}
		}
		out.Sample = *a
	}
	if n := y.SugerirApos; n != nil {
		if *n < 2 || *n > 20 {
			return SummarySettings{}, &SectionError{Item: "resumo.sugerir_apos", Reason: "vai de 2 a 20"}
		}
		out.SuggestAfter = *n
	}
	return out, nil
}

// peopleDeadline is the time of the people for a request of a type.
func (s Settings) peopleDeadline(t classes.RequestType) time.Duration {
	if t == "" {
		t = classes.RequestClassPermission // the requests of an action have no type in old records
	}
	if t == classes.RequestGuildEntry {
		return GuildEntryValidity // fixed: the file cannot change it
	}
	if d, ok := s.PeopleDeadlines[t]; ok && d > 0 {
		return d
	}
	return s.PeopleDeadline
}

// ParseDuration is time.ParseDuration that also reads whole days (`7d`, `30d`),
// the unit the policy file uses for the long times.
func ParseDuration(v string) (time.Duration, error) {
	if n, ok := strings.CutSuffix(v, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil || days < 0 || days > 36500 {
			return 0, fmt.Errorf("invalid duration %q", v)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(v)
}

// DefaultSettings is the default content of the section.
func DefaultSettings() Settings {
	return Settings{AgentDeadline: 2 * time.Minute, GrandMasterDeadline: 10 * time.Minute, PeopleDeadline: 24 * time.Hour,
		PeopleDeadlines: map[classes.RequestType]time.Duration{classes.RequestClassPermission: 2 * time.Hour},
		TempRuleMax:     24 * time.Hour, MesaDecides: slices.Clone(classes.DefaultMesaGroups), DenialsToPause: 3,
		SummaryEvery: 30 * time.Minute, QueueAlertOpen: 10, QueueAlertFactor: 2, Summary: DefaultSummary()}
}

// WithDefaults fills what the policy file leaves out with the defaults.
func (s Settings) WithDefaults() Settings {
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
	if s.PeopleDeadlines == nil {
		s.PeopleDeadlines = d.PeopleDeadlines
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
	if s.SummaryEvery <= 0 {
		s.SummaryEvery = d.SummaryEvery
	}
	if s.Summary.Frequency == "" {
		s.Summary = d.Summary
	}
	if s.QueueAlertOpen <= 0 {
		s.QueueAlertOpen = d.QueueAlertOpen
	}
	if s.QueueAlertFactor <= 0 {
		s.QueueAlertFactor = d.QueueAlertFactor
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
	AlertaFila         struct {
		Abertos *int `yaml:"abertos"`
		Fator   *int `yaml:"fator"`
	} `yaml:"alerta_fila"`
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
	dec.KnownFields(true)
	var y sectionYAML
	if err := dec.Decode(&y); err != nil && !errors.Is(err, io.EOF) {
		return Settings{}, &SectionError{Item: "cadeia", Reason: err.Error()}
	}
	dur := func(item, v string, max time.Duration, dst *time.Duration) error {
		if v == "" {
			return nil
		}
		d, err := ParseDuration(v)
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
	if err := dur("cadeia.resumo_mesas", y.ResumoMesas, 0, &s.SummaryEvery); err != nil {
		return Settings{}, err
	}
	if a := y.AlertaFila.Abertos; a != nil {
		if *a < 1 || *a > 1000 {
			return Settings{}, &SectionError{Item: "cadeia.alerta_fila.abertos", Reason: "goes from 1 to 1000"}
		}
		s.QueueAlertOpen = *a
	}
	if f := y.AlertaFila.Fator; f != nil {
		if *f < 1 || *f > 10 {
			return Settings{}, &SectionError{Item: "cadeia.alerta_fila.fator", Reason: "goes from 1 to 10"}
		}
		s.QueueAlertFactor = *f
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

// policyReader reads the section of the policy and, when the file cannot be
// read, keeps the last valid version it read for the workspace: an unreadable
// file never loosens the chain back to the defaults.
type policyReader struct {
	mu   sync.Mutex
	last map[string]readSettings
}

type readSettings struct {
	st      Settings
	version string
}

func (p *policyReader) read(ctx context.Context, pol Policy, log *slog.Logger, workspace string) (Settings, string) {
	st, version, err := pol.Chain(ctx, workspace)
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		if prev, ok := p.last[workspace]; ok {
			log.Warn("chain: the policy was not read, the last valid one holds", "workspace", workspace, "err", err)
			return prev.st, prev.version
		}
		log.Warn("chain: the policy was not read, the default holds", "workspace", workspace, "err", err)
		return DefaultSettings(), "default"
	}
	st = st.WithDefaults()
	if p.last == nil {
		p.last = map[string]readSettings{}
	}
	p.last[workspace] = readSettings{st, version}
	return st, version
}
