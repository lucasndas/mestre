package chain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/safefile"
)

// MaxPolicyBytes bounds the policy file: it lives in the main clone, a folder
// the account of the agents can reach.
const MaxPolicyBytes = 256 << 10

// PolicyFile is the policy of the workspaces read from their file,
// .mestre/regras/politica-de-aprovacao.yaml of the main clone
// (docs/aprovacoes/arquivo-de-politica.md). It is the Policy of the chain
// (Chain) and the owner's part of the risk classification (Risk).
//
// The file is read at each use, through safefile (a link or a file that is not
// regular is refused: the folder is reachable by the agents) and validated
// whole. An invalid or unreadable file never loosens anything: the last valid
// version of the workspace holds, or the default content before any, and the
// Owner is told once per version of the file that did not hold (Alert).
type PolicyFile struct {
	// Path is the policy file of a workspace in its main clone; "" means the
	// workspace has no folder (the default content holds).
	Path func(workspace string) string
	// Read reads a file; nil is safefile.Read bounded by MaxPolicyBytes.
	Read func(path string) ([]byte, error)
	// Alert is told that the file of the workspace does not hold, with why; once
	// for each content that failed, and not again until it changes. nil drops it.
	Alert  func(workspace string, err error)
	Logger *slog.Logger

	mu   sync.Mutex
	last map[string]*policyState
}

type policyState struct {
	valid   *parsedPolicy // the last version that held
	alerted string        // hash of the last content that did not hold and was told
}

type parsedPolicy struct {
	version  string
	settings Settings
	risk     *classes.RiskPolicy
	rules    *PolicyRules
}

var (
	_ Policy = (*PolicyFile)(nil)

	// ErrInvalidPolicy wraps every error of an invalid policy file.
	ErrInvalidPolicy = errors.New("invalid approval policy file")

	// fixedTypes are the types of a fixed rule: the file cannot give them a time
	// or a way to decide.
	fixedTypes = []classes.RequestType{classes.RequestOperator, classes.RequestGuildEntry, classes.RequestCitadelPromotion, classes.RequestMestreUpdate}

	// highTypes are the types of risk alto: the Grand Master opens them only by
	// delegation, so the file cannot let them be decided alone.
	highTypes = []classes.RequestType{classes.RequestWorkflows, classes.RequestRepoSettings, classes.RequestWorkspaceClass, classes.RequestDestructive, classes.RequestAgentsMD, classes.RequestGMAction}

	// knownTypes are the types the file may configure.
	knownTypes = []classes.RequestType{
		classes.RequestPR, classes.RequestMerge, classes.RequestMissionPoint, classes.RequestClassPermission, classes.RequestExtraCost,
		classes.RequestWorkflows, classes.RequestRepoSettings, classes.RequestWorkspaceClass, classes.RequestDestructive, classes.RequestAgentsMD, classes.RequestGMAction,
	}
)

// policyYAML is the file as the chain reads it: the sections it does not own
// are kept as nodes, so the top-level keys are still checked (a misspelled
// section is an error) and the owners of the others read them.
type policyYAML struct {
	Versao            int                  `yaml:"versao"`
	Expiracao         string               `yaml:"expiracao"`
	Condicoes         yaml.Node            `yaml:"condicoes"`
	AreasSensiveis    map[string]yaml.Node `yaml:"areas_sensiveis"`
	ComandosSensiveis yaml.Node            `yaml:"comandos_sensiveis"`
	Tipos             map[string]struct {
		Sozinho     string `yaml:"sozinho"`
		NivelMinimo string `yaml:"nivel_minimo"`
		Expiracao   string `yaml:"expiracao"`
		Pin         bool   `yaml:"pin"`
	} `yaml:"tipos"`
	Quorum    yaml.Node `yaml:"quorum"`
	Cadeia    yaml.Node `yaml:"cadeia"`
	Risco     yaml.Node `yaml:"risco"`
	Delegacao yaml.Node `yaml:"delegacao"`
	Resumo    yaml.Node `yaml:"resumo"`
}

// ParsePolicyFile reads and checks the policy file: the deadlines of the
// people (expiracao and tipos.<tipo>.expiracao), the cadeia section and the
// risco section, and the sensitive areas the classification reads. Absent
// keys are the default. Any error makes the whole file invalid.
func ParsePolicyFile(data []byte) (Settings, *classes.RiskPolicy, error) {
	st, risk, _, err := parsePolicyFull(data)
	return st, risk, err
}

// ParsePolicyRules is the rules of the file (conditions, areas, quorum…) that
// the Grão-Mestre is held to; the same checks of ParsePolicyFile apply.
func ParsePolicyRules(data []byte) (PolicyRules, error) {
	_, _, rules, err := parsePolicyFull(data)
	return rules, err
}

func parsePolicyFull(data []byte) (Settings, *classes.RiskPolicy, PolicyRules, error) {
	bad := func(item, reason string) (Settings, *classes.RiskPolicy, PolicyRules, error) {
		return Settings{}, nil, PolicyRules{}, &SectionError{Item: item, Reason: reason}
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var y policyYAML
	if err := dec.Decode(&y); err != nil {
		if errors.Is(err, io.EOF) {
			return bad("arquivo", "vazio")
		}
		return bad("arquivo", err.Error())
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return bad("arquivo", "mais de um documento YAML")
	}
	if y.Versao != 1 {
		return bad("versao", "o arquivo precisa começar com `versao: 1`")
	}
	var expiry time.Duration
	if y.Expiracao != "" {
		d, err := ParseDuration(y.Expiracao)
		if err != nil || d <= 0 {
			return bad("expiracao", "precisa de uma duração positiva como 24h")
		}
		expiry = d
	}
	section, err := nodeYAML(&y.Cadeia)
	if err != nil {
		return bad("cadeia", err.Error())
	}
	st, err := ParseSection(section, expiry)
	if err != nil {
		return Settings{}, nil, PolicyRules{}, err
	}
	for name, t := range y.Tipos {
		rt := classes.RequestType(name)
		if slices.Contains(fixedTypes, rt) {
			return bad("tipos."+name, "é um tipo de regra fixa: o arquivo não o configura")
		}
		if !slices.Contains(knownTypes, rt) {
			return bad("tipos."+name, "tipo desconhecido")
		}
		switch t.Sozinho {
		case "", "nunca":
		case "se_condicoes":
			if slices.Contains(highTypes, rt) {
				return bad("tipos."+name+".sozinho", "um tipo de risco alto não se decide sozinho; só por delegação")
			}
		default:
			return bad("tipos."+name+".sozinho", "precisa de se_condicoes ou nunca")
		}
		if !slices.Contains([]string{"", "aprovador", "administrador", "dono"}, t.NivelMinimo) {
			return bad("tipos."+name+".nivel_minimo", "precisa de aprovador, administrador ou dono")
		}
		if t.Expiracao == "" {
			continue
		}
		d, err := ParseDuration(t.Expiracao)
		if err != nil || d <= 0 {
			return bad("tipos."+name+".expiracao", "precisa de uma duração positiva como 2h")
		}
		if st.PeopleDeadlines == nil {
			st.PeopleDeadlines = map[classes.RequestType]time.Duration{}
		}
		st.PeopleDeadlines[rt] = d
	}
	if section, err = nodeYAML(&y.Resumo); err != nil {
		return bad("resumo", err.Error())
	} else if len(section) > 0 {
		if st.Summary, err = ParseSummary(section); err != nil {
			return Settings{}, nil, PolicyRules{}, err
		}
	}
	if section, err = nodeYAML(&y.Delegacao); err != nil {
		return bad("delegacao", err.Error())
	} else if len(section) > 0 {
		if st.DelegationValidity, st.DelegationReminder, err = ParseDelegation(section); err != nil {
			return Settings{}, nil, PolicyRules{}, err
		}
	}
	risk := &classes.RiskPolicy{}
	if section, err = nodeYAML(&y.Risco); err != nil {
		return bad("risco", err.Error())
	} else if len(section) > 0 {
		adj, err := classes.ParseRiskAdjustments(section)
		if err != nil {
			return Settings{}, nil, PolicyRules{}, err
		}
		risk.Adjustments = adj
	}
	for _, area := range []struct {
		key string
		dst *[]string
	}{{"permissoes_e_segredos", &risk.Secrets}, {"mestre", &risk.Mestre}} {
		node, ok := y.AreasSensiveis[area.key]
		if !ok {
			continue
		}
		var globs []string
		if err := node.Decode(&globs); err != nil {
			return bad("areas_sensiveis."+area.key, "precisa de uma lista de globs")
		}
		if len(globs) == 0 {
			return bad("areas_sensiveis."+area.key, "a lista vazia tiraria a proteção; apague a chave para valer o padrão")
		}
		for _, g := range globs {
			if !classes.ValidGlob(g) {
				return bad("areas_sensiveis."+area.key, fmt.Sprintf("glob inválido %q", g))
			}
		}
		*area.dst = globs
	}
	rules, err := parseRules(&y)
	if err != nil {
		return Settings{}, nil, PolicyRules{}, err
	}
	return st.WithDefaults(), risk, rules, nil
}

// nodeYAML is the YAML of a section's value, nil when the file does not have it.
func nodeYAML(n *yaml.Node) ([]byte, error) {
	if n.Kind == 0 {
		return nil, nil
	}
	b, err := yaml.Marshal(n)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (f *PolicyFile) log() *slog.Logger {
	if f.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return f.Logger
}

// load reads the file of the workspace and returns what holds now; the Owner
// is told after the lock is let go (an Alert that comes back here must not wait
// for itself).
func (f *PolicyFile) load(workspace string) *parsedPolicy {
	var told error
	f.mu.Lock()
	p := f.loadLocked(workspace, &told)
	f.mu.Unlock()
	if told != nil && f.Alert != nil {
		f.Alert(workspace, told)
	}
	return p
}

func (f *PolicyFile) loadLocked(workspace string, told *error) *parsedPolicy {
	if f.last == nil {
		f.last = map[string]*policyState{}
	}
	st := f.last[workspace]
	if st == nil {
		st = &policyState{}
		f.last[workspace] = st
	}
	holds := func() *parsedPolicy {
		if st.valid != nil {
			return st.valid
		}
		return &parsedPolicy{version: "default", settings: DefaultSettings()}
	}
	path := ""
	if f.Path != nil {
		path = f.Path(workspace)
	}
	if path == "" {
		return holds()
	}
	read := f.Read
	if read == nil {
		read = func(p string) ([]byte, error) {
			b, _, err := safefile.ReadTrusted(p, MaxPolicyBytes)
			return b, err
		}
	}
	data, err := read(path)
	if errors.Is(err, fs.ErrNotExist) {
		return holds() // the session zero makes it; until then the default content holds
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	if err == nil {
		var settings Settings
		var risk *classes.RiskPolicy
		var rules PolicyRules
		if settings, risk, rules, err = parsePolicyFull(data); err == nil {
			st.valid = &parsedPolicy{version: "sha256:" + hash[:16], settings: settings, risk: risk, rules: &rules}
			st.alerted = ""
			return st.valid
		}
		err = fmt.Errorf("%w: %w", ErrInvalidPolicy, err)
	} else {
		hash = "unreadable"
		err = fmt.Errorf("%w: %w", ErrInvalidPolicy, err)
	}
	f.log().Warn("chain: the policy file does not hold, the last valid version does", "workspace", workspace, "err", err)
	if st.alerted != hash {
		st.alerted = hash
		*told = err
	}
	return holds()
}

// PolicySnapshot is what holds of a workspace at one reading of the file: the
// settings, the version (hash) and the owner's part of the risk. A decision that
// audits the version takes all three from one snapshot.
type PolicySnapshot struct {
	Settings Settings
	Version  string
	Risk     *classes.RiskPolicy
}

// Snapshot reads the file once. The result is the caller's own: changing it
// does not change what holds.
func (f *PolicyFile) Snapshot(workspace string) PolicySnapshot {
	p := f.load(workspace)
	s := p.settings
	s.PeopleDeadlines = maps.Clone(s.PeopleDeadlines)
	s.MesaDecides = slices.Clone(s.MesaDecides)
	var risk *classes.RiskPolicy
	if p.risk != nil {
		r := *p.risk
		r.Secrets, r.Mestre = slices.Clone(r.Secrets), slices.Clone(r.Mestre)
		risk = &r
	}
	return PolicySnapshot{Settings: s, Version: p.version, Risk: risk}
}

// Chain implements Policy: the settings that hold and the version (hash) of
// the file they come from ("default" when none has been read).
func (f *PolicyFile) Chain(_ context.Context, workspace string) (Settings, string, error) {
	s := f.Snapshot(workspace)
	return s.Settings, s.Version, nil
}

// Risk is the owner's part of the risk classification of the workspace
// (env.Risk of the decisions); nil is the default table.
func (f *PolicyFile) Risk(workspace string) *classes.RiskPolicy { return f.Snapshot(workspace).Risk }
