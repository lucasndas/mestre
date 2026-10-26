package chain

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/mestre-ia/mestre/internal/classes"
)

// PolicyRules are the rules of the approval policy file the Grão-Mestre is held
// to (docs/aprovacoes/politica.md): the conditions the engine checks, the
// sensitive areas, words and commands, which types he decides alone, the
// quorum and the minimum level of the people. The deadlines, the cadeia section
// and the risk adjustments are Settings and classes.RiskPolicy.
type PolicyRules struct {
	CIGreen, ReviewByOther bool
	// MaxFiles, MaxLines and MaxDeleted are the size limits; 0 is no limit.
	MaxFiles, MaxLines, MaxDeleted int
	// IgnoredLines are the globs of the files that count neither in MaxLines
	// nor in the search for words.
	IgnoredLines []string
	// Areas are the sensitive areas (name → globs); Words the words that send
	// an added line to a person.
	Areas    map[string][]string
	Words    []string
	Commands []string
	// Alone says which types the Grão-Mestre decides alone when the conditions
	// pass (sozinho: se_condicoes).
	Alone map[classes.RequestType]bool
	// AskAlways and AskAll are the types (or all of them) that a decree of policy
	// sends to the people, whatever the risk and any delegation.
	AskAlways map[classes.RequestType]bool
	AskAll    bool
	// MinLevel is the nivel_minimo of each type.
	MinLevel map[classes.RequestType]classes.Level
	// Pin says which types ask the PIN of the user after the button (the
	// pin of the file; the fixed types always do).
	Pin map[classes.RequestType]bool
	// RiskMinLevel is the minimum level of the people for each risk.
	RiskMinLevel map[classes.Risk]classes.Level
	// Quorum is the default number of approvals; Rules the ones that force a
	// question to the people.
	Quorum      int
	QuorumRules []QuorumRule

	wordRes []*regexp.Regexp
}

// QuorumRule is a regra of quorum: it matches a request by type and table, and
// it forces the people to be asked.
type QuorumRule struct {
	// Type and Table are "" or "*" for any.
	Type      classes.RequestType
	Table     string
	Approvals int
	// Require are the users whose approval is required (exigir_usuarios).
	Require []string
}

func (q QuorumRule) matches(r Request) bool {
	return (q.Type == "" || q.Type == "*" || q.Type == r.Type) && (q.Table == "" || q.Table == "*" || q.Table == r.Table)
}

// DefaultRules is the content of the default policy file
// (docs/aprovacoes/arquivo-de-politica.md#conteúdo-padrão).
func DefaultRules() PolicyRules {
	r := PolicyRules{
		CIGreen: true, ReviewByOther: true, MaxFiles: 25, MaxLines: 600, MaxDeleted: 5,
		IgnoredLines: []string{"**/package-lock.json", "**/yarn.lock", "**/pnpm-lock.yaml", "**/go.sum", "**/Cargo.lock", "**/*.snap", "**/gen/**"},
		Areas: map[string][]string{
			"autenticacao":          {"**/auth/**", "**/*login*", "**/*session*"},
			"pagamentos":            {"**/payment*/**", "**/billing/**", "**/checkout/**"},
			"migracoes":             {"**/migrations/**", "**/*.sql"},
			"infraestrutura":        {"infra/**", "terraform/**", "k8s/**", "**/Dockerfile", "**/docker-compose*.yml", ".github/**"},
			"permissoes_e_segredos": slices.Clone(classes.DefaultSecretPaths),
			"mestre":                slices.Clone(classes.DefaultMestrePaths),
		},
		Words:        []string{"DROP", "TRUNCATE", "DELETE FROM"},
		Commands:     []string{"rm -rf *", "git push*", "*DROP TABLE*", "*TRUNCATE*", "*DELETE FROM*"},
		Alone:        map[classes.RequestType]bool{},
		MinLevel:     map[classes.RequestType]classes.Level{},
		Pin:          map[classes.RequestType]bool{},
		RiskMinLevel: map[classes.Risk]classes.Level{classes.RiskHigh: classes.LevelAdmin},
		Quorum:       1,
	}
	for _, t := range []classes.RequestType{classes.RequestPR, classes.RequestMerge, classes.RequestMissionPoint, classes.RequestClassPermission, classes.RequestExtraCost} {
		r.Alone[t], r.MinLevel[t] = true, classes.LevelApprover
	}
	for _, t := range []classes.RequestType{classes.RequestWorkflows, classes.RequestWorkspaceClass, classes.RequestDestructive, classes.RequestAgentsMD, classes.RequestGMAction} {
		r.Alone[t], r.MinLevel[t] = false, classes.LevelAdmin
	}
	r.Alone[classes.RequestRepoSettings], r.MinLevel[classes.RequestRepoSettings] = false, classes.LevelOwner
	r.compile()
	return r
}

// compile prepares the search for words: whole words, any case, the spaces of a
// phrase matching any run of blanks.
func (r *PolicyRules) compile() {
	r.wordRes = r.wordRes[:0]
	for _, w := range r.Words {
		parts := strings.Fields(w)
		for i := range parts {
			parts[i] = regexp.QuoteMeta(parts[i])
		}
		r.wordRes = append(r.wordRes, regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}_])`+strings.Join(parts, `\s+`)+`(?:$|[^\p{L}\p{N}_])`))
	}
}

// ForcesPeople says whether the policy sends the request to the people no
// matter what the conditions find, and why: a type that is not decided alone, a
// rule of quorum that matches. It looks at no PR: it is what the request is.
func (r PolicyRules) ForcesPeople(req Request) string {
	t := req.Type
	if t == "" {
		t = classes.RequestClassPermission
	}
	if why := r.AsksAlways(req); why != "" {
		return why
	}
	if req.Risk.Risk != classes.RiskHigh && !r.Alone[t] {
		return fmt.Sprintf("a política manda %s para as pessoas (sozinho: nunca)", t)
	}
	for _, q := range r.QuorumRules {
		if q.matches(req) {
			if len(q.Require) > 0 {
				return fmt.Sprintf("regra de quórum: exige a aprovação de %s", strings.Join(q.Require, ", "))
			}
			return fmt.Sprintf("regra de quórum: %d aprovações", q.Approvals)
		}
	}
	return ""
}

// AsksAlways says why a decree of policy sends the request to the people, at any
// level and whatever the risk ("me pergunte tudo", or a type), and "" when none does.
func (r PolicyRules) AsksAlways(req Request) string {
	t := req.Type
	if t == "" {
		t = classes.RequestClassPermission
	}
	if r.AskAll || r.AskAlways[t] {
		return fmt.Sprintf("um decreto de política manda perguntar sobre %s", t)
	}
	return ""
}

// NeedsPIN says whether the approval of a request asks the PIN of the user
// after the button: the types of the policy with pin, and always the fixed
// ones (the Operator, the entry in the Guild, the promotion, the settings of the
// repository).
func (r PolicyRules) NeedsPIN(req Request) bool {
	switch req.Type {
	case classes.RequestOperator, classes.RequestGuildEntry, classes.RequestCitadelPromotion, classes.RequestRepoSettings:
		return true
	}
	return r.Pin[req.Type]
}

// Need is what the people must give a request: the approvals (the strictest
// quorum rule that matches, or the default), the users who must approve and the
// minimum level (of the type, or of the risk if higher).
func (r PolicyRules) Need(req Request) (approvals int, require []string, min classes.Level) {
	approvals = max(r.Quorum, 1)
	for _, q := range r.QuorumRules {
		if q.matches(req) {
			approvals = max(approvals, q.Approvals)
			for _, u := range q.Require {
				if !slices.Contains(require, u) {
					require = append(require, u)
				}
			}
		}
	}
	for _, u := range req.Require { // the people this very request needs
		if !slices.Contains(require, u) {
			require = append(require, u)
		}
	}
	min = classes.LevelApprover
	if l, ok := r.MinLevel[req.Type]; ok {
		min = l
	}
	// The fixed rules are not in the file: only the Owner answers these, and the
	// Administrator the update of the Mestre.
	switch req.Type {
	case classes.RequestOperator, classes.RequestGuildEntry, classes.RequestCitadelPromotion:
		min = classes.LevelOwner
	case classes.RequestMestreUpdate:
		min = classes.LevelAdmin
	}
	if l, ok := r.RiskMinLevel[req.Risk.Risk]; ok && l.AtLeast(min) {
		min = l
	}
	return approvals, require, min
}

type rulesYAML struct {
	Condicoes struct {
		CIVerde             *bool    `yaml:"ci_verde"`
		RevisaoPorOutro     *bool    `yaml:"revisao_por_outro_personagem"`
		MaxArquivos         *int     `yaml:"max_arquivos"`
		MaxLinhas           *int     `yaml:"max_linhas"`
		MaxArquivosApagados *int     `yaml:"max_arquivos_apagados"`
		DentroDoTeto        *bool    `yaml:"dentro_do_teto_de_custo"`
		LinhasIgnoradas     []string `yaml:"linhas_ignoradas"`
	} `yaml:"condicoes"`
	Quorum struct {
		Padrao struct {
			Aprovacoes int `yaml:"aprovacoes"`
		} `yaml:"padrao"`
		Regras []struct {
			Tipo           string   `yaml:"tipo"`
			Mesa           string   `yaml:"mesa"`
			Aprovacoes     int      `yaml:"aprovacoes"`
			ExigirUsuarios []string `yaml:"exigir_usuarios"`
		} `yaml:"regras"`
	} `yaml:"quorum"`
	Risco struct {
		NivelMinimo map[string]string `yaml:"nivel_minimo"`
	} `yaml:"risco"`
}

// wordsKey is the key of areas_sensiveis that is a list of words, not an area.
const wordsKey = "palavras_em_linhas_adicionadas"

// parseRules reads the rules the chain owns out of the file already decoded
// (policyYAML): absent keys are the default content. An invalid item is a
// SectionError, which makes the whole file invalid.
func parseRules(y *policyYAML) (PolicyRules, error) {
	r := DefaultRules()
	bad := func(item, reason string) (PolicyRules, error) {
		return PolicyRules{}, &SectionError{Item: item, Reason: reason}
	}
	var ry rulesYAML
	for _, n := range []struct {
		node *yaml.Node
		dst  any
		item string
	}{{&y.Condicoes, &ry.Condicoes, "condicoes"}, {&y.Quorum, &ry.Quorum, "quorum"}} {
		data, err := nodeYAML(n.node)
		if err != nil {
			return bad(n.item, err.Error())
		}
		if len(data) == 0 {
			continue
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(n.dst); err != nil && !errors.Is(err, io.EOF) {
			return bad(n.item, err.Error())
		}
	}
	if data, err := nodeYAML(&y.Risco); err == nil && len(data) > 0 {
		_ = yaml.Unmarshal(data, &ry.Risco) // the owner of the section (classes) already checked it
	}
	c := ry.Condicoes
	if c.CIVerde != nil {
		r.CIGreen = *c.CIVerde
	}
	if c.RevisaoPorOutro != nil {
		r.ReviewByOther = *c.RevisaoPorOutro
	}
	for _, l := range []struct {
		item string
		v    *int
		dst  *int
	}{{"condicoes.max_arquivos", c.MaxArquivos, &r.MaxFiles}, {"condicoes.max_linhas", c.MaxLinhas, &r.MaxLines}, {"condicoes.max_arquivos_apagados", c.MaxArquivosApagados, &r.MaxDeleted}} {
		if l.v == nil {
			continue
		}
		if *l.v < 0 || *l.v > 1_000_000 {
			return bad(l.item, "precisa de um número de 0 a 1000000")
		}
		*l.dst = *l.v
	}
	if c.LinhasIgnoradas != nil {
		for _, g := range c.LinhasIgnoradas {
			if !classes.ValidGlob(g) {
				return bad("condicoes.linhas_ignoradas", fmt.Sprintf("glob inválido %q", g))
			}
		}
		r.IgnoredLines = c.LinhasIgnoradas
	}
	for name, node := range y.AreasSensiveis {
		var list []string
		if err := node.Decode(&list); err != nil {
			return bad("areas_sensiveis."+name, "precisa de uma lista")
		}
		if name == wordsKey {
			for _, w := range list {
				if strings.TrimSpace(w) == "" {
					return bad("areas_sensiveis."+name, "palavra vazia")
				}
			}
			r.Words = list
			continue
		}
		for _, g := range list {
			if !classes.ValidGlob(g) {
				return bad("areas_sensiveis."+name, fmt.Sprintf("glob inválido %q", g))
			}
		}
		r.Areas[name] = list
	}
	if cmds, err := listOf(&y.ComandosSensiveis); err != nil {
		return bad("comandos_sensiveis", "precisa de uma lista de padrões")
	} else if cmds != nil {
		for _, p := range cmds {
			if strings.TrimSpace(p) == "" {
				return bad("comandos_sensiveis", "padrão vazio")
			}
		}
		r.Commands = cmds
	}
	for name, t := range y.Tipos {
		rt := classes.RequestType(name)
		switch t.Sozinho {
		case "nunca":
			r.Alone[rt] = false
		case "se_condicoes":
			r.Alone[rt] = true
		}
		if t.Pin {
			r.Pin[rt] = true
		}
		switch t.NivelMinimo {
		case "aprovador":
			r.MinLevel[rt] = classes.LevelApprover
		case "administrador":
			r.MinLevel[rt] = classes.LevelAdmin
		case "dono":
			r.MinLevel[rt] = classes.LevelOwner
		}
	}
	for name, v := range ry.Risco.NivelMinimo {
		lvl := map[string]classes.Level{"aprovador": classes.LevelApprover, "administrador": classes.LevelAdmin, "dono": classes.LevelOwner}[v]
		if lvl == "" {
			continue // the owner of the section rejects it
		}
		r.RiskMinLevel[classes.Risk(name)] = lvl
	}
	if q := ry.Quorum.Padrao.Aprovacoes; q != 0 {
		if q < 1 || q > 10 {
			return bad("quorum.padrao.aprovacoes", "vai de 1 a 10")
		}
		r.Quorum = q
	}
	for i, q := range ry.Quorum.Regras {
		item := fmt.Sprintf("quorum.regras[%d]", i+1)
		switch {
		case q.Tipo == "" && q.Mesa == "":
			return bad(item, "precisa de tipo e/ou mesa")
		case q.Tipo != "" && q.Tipo != "*" && !slices.Contains(append(slices.Clone(knownTypes), fixedTypes...), classes.RequestType(q.Tipo)):
			return bad(item, "tipo desconhecido")
		case q.Aprovacoes < 0 || q.Aprovacoes > 10:
			return bad(item, "aprovacoes vai de 1 a 10")
		case q.Aprovacoes == 0 && len(q.ExigirUsuarios) == 0:
			return bad(item, "precisa de aprovacoes ou exigir_usuarios")
		}
		r.QuorumRules = append(r.QuorumRules, QuorumRule{Type: classes.RequestType(q.Tipo), Table: q.Mesa, Approvals: max(q.Aprovacoes, 1), Require: q.ExigirUsuarios})
	}
	r.compile()
	return r, nil
}

// listOf is the list of strings of a node, nil when the file does not have it.
func listOf(n *yaml.Node) ([]string, error) {
	if n.Kind == 0 {
		return nil, nil
	}
	var out []string
	if err := n.Decode(&out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

// areaNames are the names of the sensitive areas, in a fixed order.
func (r PolicyRules) areaNames() []string {
	names := make([]string, 0, len(r.Areas))
	for n := range r.Areas {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// wildMatch matches a command against a pattern whose only wildcard is `*`.
func wildMatch(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(s, p)
		if i < 0 {
			return false
		}
		s = s[i+len(p):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}

// commandSegments are the simple commands of a command line, lower case, with
// the blanks collapsed and without the assignments and `sudo` that lead them:
// `cd x && FOO=1 sudo git push` has the segments `cd x` and `git push`.
func commandSegments(cmd string) []string {
	seps := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n", "&", "\n", "(", "\n", ")", "\n", "`", "\n", "$", "\n")
	var out []string
	for _, seg := range strings.Split(seps.Replace(strings.ToLower(cmd)), "\n") {
		words := strings.Fields(seg)
		for len(words) > 0 && (strings.Contains(words[0], "=") || words[0] == "sudo" || words[0] == "env" || words[0] == "command") {
			words = words[1:]
		}
		if len(words) > 0 {
			out = append(out, strings.Join(words, " "))
		}
	}
	return out
}
