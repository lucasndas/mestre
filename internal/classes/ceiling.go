package classes

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// CeilingFile is where the ceiling lives in the main clone of the workspace
// (docs/classes/teto-do-workspace.md#arquivo).
const CeilingFile = ".mestre/regras/teto.yaml"

// CeilingVersion is the version of the teto.yaml format.
const CeilingVersion = 1

// DefaultCeilingYAML is the default ceiling the session zero of the
// workspace writes: no restriction beyond the fixed rules, but no class asks
// for a push, sends CI changes or uses the Operador do PC, no class reads the
// files of secrets (the globs of permissoes_e_segredos of the approval policy,
// but **/secrets/** and **/*permission*, which there only ask), and no cost
// block (only the usage limits hold). **/.env* has no exception: it takes
// .env.example too (docs/classes/teto-do-workspace.md).
const DefaultCeilingYAML = `version: 1
permissions:
  files:
    deny: ["**/.env*", "**/*.pem", "**/*.key", "**/id_rsa*", "**/id_ed25519*", "**/id_ecdsa*", "**/.npmrc", "**/credentials*.json", "**/service-account*.json"]
  git:
    request_push: false
    workflows: never
  operator: false
`

// Ceiling is a teto.yaml: the permissions block, in the format of
// permissions.yaml read as a maximum, and the cost ceiling.
type Ceiling struct {
	Version     int          `yaml:"version"`
	Permissions *Permissions `yaml:"permissions"`
	Cost        *CostCeiling `yaml:"cost"`
	// Hash is the sha256 of the file the ceiling was read from: the version
	// the database records as approved.
	Hash string `yaml:"-"`
	raw  []byte
}

// CostCeiling is the cost block: the maximum, per unit of the usage limits
// (docs/plataforma/limites-de-uso.md), that the configured limits may have.
// An absent unit has no ceiling.
type CostCeiling struct {
	USD    *CostLimits `yaml:"usd"`
	Tokens *CostLimits `yaml:"tokens"`
	Hours  *CostLimits `yaml:"hours"`
}

// CostLimits are the maximums of one unit: per table and per day of the
// workspace. An absent field has no ceiling.
type CostLimits struct {
	PerTable *float64 `yaml:"per_table"`
	PerDay   *float64 `yaml:"per_day"`
}

// ErrInvalidCeiling wraps every error of an invalid teto.yaml: the last
// approved version holds.
var ErrInvalidCeiling = errors.New("invalid teto.yaml")

// ParseCeiling reads and checks a teto.yaml, as strictly as a
// permissions.yaml: version 1 at the top, no unknown field, one document,
// the permissions block valid in that format (without a version of its
// own) and finite, non-negative cost values (whole ones for tokens).
func ParseCeiling(data []byte) (*Ceiling, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var c Ceiling
	if err := dec.Decode(&c); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%w: empty", ErrInvalidCeiling)
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidCeiling, err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: more than one YAML document", ErrInvalidCeiling)
	}
	if c.Version != CeilingVersion {
		return nil, fmt.Errorf("%w: version %d: this binary reads version %d", ErrInvalidCeiling, c.Version, CeilingVersion)
	}
	if c.Permissions != nil {
		if c.Permissions.Version != 0 {
			return nil, fmt.Errorf("%w: permissions.version: the version goes at the top of teto.yaml", ErrInvalidCeiling)
		}
		c.Permissions.Version = PermissionsVersion
		if err := c.Permissions.validate(); err != nil {
			return nil, fmt.Errorf("%w: permissions: %v", ErrInvalidCeiling, err)
		}
	}
	if err := c.Cost.validate(); err != nil {
		return nil, fmt.Errorf("%w: cost: %v", ErrInvalidCeiling, err)
	}
	c.Hash, c.raw = hashOf(data), slices.Clone(data)
	return &c, nil
}

// DefaultCeiling is the default ceiling, parsed.
func DefaultCeiling() *Ceiling {
	c, err := ParseCeiling([]byte(DefaultCeilingYAML))
	if err != nil {
		panic("classes: the default ceiling does not parse: " + err.Error()) // an impossible invariant
	}
	return c
}

// Raw is the file the ceiling was read from.
func (c *Ceiling) Raw() []byte { return slices.Clone(c.raw) }

// Maximum is the permissions block as Layers.Ceiling takes it. operator,
// when absent, is false: a class is used on the Operador do PC base only if
// the ceiling also says operator: true
// (docs/classes/formato-das-permissoes.md#git-rede-e-ferramentas).
func (c *Ceiling) Maximum() *Permissions {
	var p Permissions
	if c != nil && c.Permissions != nil {
		p = *c.Permissions
	}
	p.Version = PermissionsVersion
	if p.Operator == nil {
		off := false
		p.Operator = &off
	}
	return &p
}

func (cc *CostCeiling) validate() error {
	if cc == nil {
		return nil
	}
	for unit, l := range cc.units() {
		if l == nil {
			continue
		}
		for field, v := range map[string]*float64{"per_table": l.PerTable, "per_day": l.PerDay} {
			switch {
			case v == nil:
			case math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0:
				return fmt.Errorf("%s.%s: %v is not a finite non-negative number", unit, field, *v)
			case unit == UnitTokens && *v != math.Trunc(*v):
				return fmt.Errorf("%s.%s: %v is not a whole number of tokens", unit, field, *v)
			}
		}
	}
	return nil
}

func (cc *CostCeiling) units() map[Unit]*CostLimits {
	if cc == nil {
		return nil
	}
	return map[Unit]*CostLimits{UnitUSD: cc.USD, UnitTokens: cc.Tokens, UnitHours: cc.Hours}
}

// Excess is one item of a class that passes the ceiling.
type Excess struct {
	Field string // "network.domains"
	Item  string // "example.com"
	Why   string // what the ceiling says, in the words of the user
}

func (e Excess) String() string {
	if e.Item == "" {
		return fmt.Sprintf("%s (%s)", e.Field, e.Why)
	}
	return fmt.Sprintf("%s %s (%s)", e.Field, e.Item, e.Why)
}

// ErrCeilingDenied is a class that does not fit in the ceiling: it is not
// created or saved (ceiling_denied of the CLI,
// docs/cli/coordenacao-classes-e-rituais.md).
var ErrCeilingDenied = errors.New("acima do teto da campanha")

// Fits returns nil when the class fits in the ceiling, or ErrCeilingDenied
// citing every item that passes it. Creating or editing a class (of the
// workspace or of a table) calls it, whoever asks.
func (c *Ceiling) Fits(class *Permissions) error {
	ex := c.Excess(class)
	if len(ex) == 0 {
		return nil
	}
	parts := make([]string, len(ex))
	for i, e := range ex {
		parts[i] = e.String()
	}
	return fmt.Errorf("%w: %s", ErrCeilingDenied, strings.Join(parts, "; "))
}

// ValidateClass reads a permissions.yaml of a class being created or edited
// and checks it against the ceiling: ErrInvalid or ErrCeilingDenied, and the
// class is not saved.
func ValidateClass(data []byte, ceiling *Ceiling) (*Permissions, error) {
	p, err := ParsePermissions(data)
	if err != nil {
		return nil, err
	}
	if err := ceiling.Fits(p); err != nil {
		return nil, err
	}
	return p, nil
}

// Excess lists what the class gives beyond the ceiling. The check is
// conservative, to warn early: a pattern the maximum does not surely contain
// passes it, and so does a grant that a deny or ask of the ceiling takes
// whole. A grant that only overlaps a deny of the ceiling fits: the pipeline
// cuts the overlap at each decision. Only what the class writes counts; a
// default of the format it leaves implicit is cut by Build.
func (c *Ceiling) Excess(class *Permissions) []Excess {
	m := c.Maximum()
	var out []Excess
	add := func(field, item, why string) { out = append(out, Excess{field, item, why}) }

	allowed := func(field string, items []string, max *[]string, covers func(t, c string) bool) {
		if max == nil {
			return
		}
		for _, it := range items {
			if !slices.ContainsFunc(*max, func(t string) bool { return covers(t, it) }) {
				add(field, it, fmt.Sprintf("o teto só dá %s", list(*max)))
			}
		}
	}
	takenWhole := func(field string, items []string, deny *[]string, why string, covers func(t, c string) bool) {
		for _, it := range items {
			if i := slices.IndexFunc(deref(deny), func(t string) bool { return covers(t, it) }); i >= 0 {
				add(field, it, fmt.Sprintf("o teto %s %s", why, (*deny)[i]))
			}
		}
	}
	verdict := func(field string, class, max *Verdict) {
		if class != nil && max != nil && class.rank() < max.rank() {
			add(field, string(*class), "o teto põe "+string(*max))
		}
	}
	flag := func(field string, class, max *bool) {
		if class != nil && *class && max != nil && !*max {
			add(field, "true", "o teto põe false")
		}
	}
	// A ceiling with an allow list gives nothing outside it: a default that
	// the class writes as allow or ask passes it.
	listed := func(field string, class *Verdict, max *[]string) {
		if class != nil && *class != Deny && max != nil {
			add(field, string(*class), "o teto só dá "+list(*max))
		}
	}

	if f := class.Files; f != nil {
		allowed("files.read", deref(f.Read), filesPtr(m, "read"), coversGlob)
		allowed("files.write", deref(f.Write), filesPtr(m, "write"), coversGlob)
		takenWhole("files.read", deref(f.Read), filesPtr(m, "deny"), "nega", coversGlob)
		takenWhole("files.write", deref(f.Write), filesPtr(m, "deny"), "nega", coversGlob)
	}
	if cm := class.Commands; cm != nil {
		allowed("commands.allow", deref(cm.Allow), ptrOf(m.Commands, "allow"), coversText)
		takenWhole("commands.allow", deref(cm.Allow), ptrOf(m.Commands, "deny"), "nega", coversText)
		takenWhole("commands.allow", deref(cm.Allow), ptrOf(m.Commands, "ask"), "pede aprovação para", coversText)
		if m.Commands != nil {
			verdict("commands.default", cm.Default, m.Commands.Default)
			listed("commands.default", cm.Default, m.Commands.Allow)
		}
	}
	if t := class.Tools; t != nil {
		var mt ToolsSpec
		if m.Tools != nil {
			mt = *m.Tools
		}
		allowed("tools.allow", deref(t.Allow), mt.Allow, coversText)
		takenWhole("tools.allow", deref(t.Allow), mt.Deny, "nega", coversText)
		takenWhole("tools.allow", deref(t.Allow), mt.Ask, "pede aprovação para", coversText)
		verdict("tools.default", t.Default, mt.Default)
		listed("tools.default", t.Default, mt.Allow)
	}
	if g := class.Git; g != nil {
		var mg GitSpec
		if m.Git != nil {
			mg = *m.Git
		}
		flag("git.commit", g.Commit, mg.Commit)
		flag("git.request_push", g.RequestPush, mg.RequestPush)
		allowed("git.branches", deref(g.Branches), mg.Branches, coversGlob)
		if g.Workflows != nil && mg.Workflows != nil && g.Workflows.rank() > mg.Workflows.rank() {
			add("git.workflows", string(*g.Workflows), "o teto põe "+string(*mg.Workflows))
		}
	}
	if n := class.Network; n != nil {
		var mn NetworkSpec
		if m.Network != nil {
			mn = *m.Network
		}
		// Absent internet is on, but the default of the system is not a
		// widening: only the domains the class writes are checked.
		internet := n.Internet == nil || *n.Internet
		flag("network.internet", n.Internet, mn.Internet)
		maxDomains := deref(mn.Domains)
		if internet && len(maxDomains) > 0 {
			if len(deref(n.Domains)) == 0 && n.Internet != nil {
				add("network.domains", "", "qualquer domínio; o teto só dá "+list(maxDomains))
			}
			allowed("network.domains", deref(n.Domains), mn.Domains, coversDomain)
		}
		allowed("network.credentials", deref(n.Credentials), mn.Credentials, func(t, c string) bool { return t == c })
	}
	if cl := class.CLI; cl != nil && m.CLI != nil {
		allowed("cli.allow", deref(cl.Allow), m.CLI.Allow, coversText)
	}
	flag("operator", class.Operator, m.Operator)
	return out
}

func list(l []string) string {
	if len(l) == 0 {
		return "nada"
	}
	return strings.Join(l, ", ")
}

// CeilingCheck is the ceiling part of `mestre class check` and of the panel:
// a class that the current ceiling cuts is "acima do teto". Nothing is
// deleted; the pipeline cuts what passes, with the reason.
type CeilingCheck struct {
	Class  string
	Excess []Excess
}

// CheckClass checks a saved class against the current ceiling.
func CheckClass(class string, p *Permissions, ceiling *Ceiling) CeilingCheck {
	return CeilingCheck{Class: class, Excess: ceiling.Excess(p)}
}

// Above reports whether the class passes the ceiling.
func (c CeilingCheck) Above() bool { return len(c.Excess) > 0 }

// Status is the label of the check.
func (c CeilingCheck) Status() string {
	if c.Above() {
		return "acima do teto"
	}
	return "dentro do teto"
}

// String is the ceiling line of the check, with one line per item.
func (c CeilingCheck) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "classe %s: %s", c.Class, c.Status())
	for _, e := range c.Excess {
		b.WriteString("\n  - " + e.String())
	}
	return b.String()
}
