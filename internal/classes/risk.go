package classes

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/lucasndas/mestre/internal/providers"
)

// Risk is the risk of an approval request, which says how far up the
// approval chain it can be decided (docs/classes/classificacao-de-risco.md).
type Risk string

// The four risks, lowest first. fixo stays out of the chain, with the
// request type's own rule.
const (
	RiskLow    Risk = "baixo"
	RiskMedium Risk = "medio"
	RiskHigh   Risk = "alto"
	RiskFixed  Risk = "fixo"
)

func (r Risk) rank() int {
	switch r {
	case RiskLow:
		return 0
	case RiskMedium:
		return 1
	case RiskHigh:
		return 2
	case RiskFixed:
		return 3
	}
	return -1
}

func (r Risk) valid() bool { return r.rank() >= 0 }

// RiskTableVersion is the version of the default risk table embedded in the
// binary; it goes with every classification to the request and the audit.
const RiskTableVersion = 5

// RequestType is a type of approval request
// (docs/aprovacoes/politica.md#tipos-de-ação).
type RequestType string

// The request types of the approval policy.
const (
	RequestClassPermission  RequestType = "permissao_de_classe"
	RequestMissionPoint     RequestType = "ponto_de_missao"
	RequestPR               RequestType = "pr"
	RequestMerge            RequestType = "merge"
	RequestExtraCost        RequestType = "custo_extra"
	RequestWorkflows        RequestType = "envio_workflows"
	RequestRepoSettings     RequestType = "configuracao_repositorio"
	RequestWorkspaceClass   RequestType = "classe_workspace"
	RequestDestructive      RequestType = "acao_destrutiva"
	RequestAgentsMD         RequestType = "proposta_agents_md"
	RequestOperator         RequestType = "ativacao_operador"
	RequestMestreUpdate     RequestType = "atualizacao_mestre"
	RequestGuildEntry       RequestType = "entrada_na_guilda"
	RequestCitadelPromotion RequestType = "promocao_cidadela"
)

// typeRisk is the risk table of the request types other than
// permissao_de_classe (the action's) and custo_extra (by the cost ceiling).
var typeRisk = map[RequestType]Risk{
	RequestMissionPoint: RiskMedium, RequestPR: RiskMedium, RequestMerge: RiskMedium,
	RequestWorkflows: RiskHigh, RequestRepoSettings: RiskHigh, RequestWorkspaceClass: RiskHigh,
	RequestDestructive: RiskHigh, RequestAgentsMD: RiskHigh,
	RequestOperator: RiskFixed, RequestMestreUpdate: RiskFixed, RequestGuildEntry: RiskFixed, RequestCitadelPromotion: RiskFixed,
}

// RiskRequest is what is classified: the type and, for permissao_de_classe,
// the normalized action. Nothing the agent wrote about the action (its
// justification) is an input.
type RiskRequest struct {
	// Type is the request type; "" is permissao_de_classe.
	Type RequestType
	// Tool is the normalized action of a permissao_de_classe; only its kind,
	// native name, command, paths and URL are read, never RawInput.
	Tool *providers.Tool
	// Cwd is the folder a command starts in; "" is the worktree.
	Cwd string
	// AboveCostCeiling is a custo_extra that takes the limit above the
	// cost ceiling (Ceiling.PassesCeiling).
	AboveCostCeiling bool
}

// RiskResult is the outcome of the classification: the risk, the version
// of the default table and the row or the owner's adjustment that decided.
// The three go to the request and to the audit.
type RiskResult struct {
	Risk         Risk   `json:"risk"`
	TableVersion int    `json:"risk_table_version"`
	Rule         string `json:"rule"`
	// Adjustment is the number (from 1, in the order of the file) of the
	// owner's adjustment that decided; 0 is the default table.
	Adjustment int `json:"adjustment,omitempty"`
	// NoDelegation is an alto that no delegation opens: only an
	// Administrator or above decides it (custo_extra above the ceiling).
	NoDelegation bool `json:"no_delegation,omitempty"`
}

// RiskPolicy is the owner's part of the classification, from the approval
// policy file (M5): the adjustments of the risco section and the sensitive
// areas whose paths make a part alto. The zero value is the default table
// and the default content of the file.
type RiskPolicy struct {
	Adjustments *RiskAdjustments
	// Secrets and Mestre are areas_sensiveis.permissoes_e_segredos and
	// areas_sensiveis.mestre; nil is the default content.
	Secrets, Mestre []string
}

// DefaultSecretPaths and DefaultMestrePaths are the globs of
// areas_sensiveis.permissoes_e_segredos and areas_sensiveis.mestre in the
// default content of the policy file
// (docs/aprovacoes/arquivo-de-politica.md#conteúdo-padrão).
var (
	DefaultSecretPaths = []string{"**/.env*", "**/secrets/**", "**/*permission*", "**/*.pem", "**/*.key"}
	DefaultMestrePaths = []string{".mestre/**", "AGENTS.md", "CLAUDE.md"}
)

// ClassifyRisk gives the risk of a request, deterministically, by the type
// of the action and its target (docs/classes/classificacao-de-risco.md):
// the same request, table version and adjustments always give the same
// risk. env is the character's (worktree, home, system, the folders the
// fixed rules close, env.Git, which tells the scripts of the repository
// from the ones of the table, and env.Risk, the owner's part); rules are
// its effective rules, for the domains (nil: none). What it does not
// recognize or cannot analyse goes up, never down.
func ClassifyRisk(env Env, rules *Rules, req RiskRequest) RiskResult {
	res := func(r Risk, rule string) RiskResult {
		return RiskResult{Risk: r, TableVersion: RiskTableVersion, Rule: rule}
	}
	switch t := req.Type; t {
	case "", RequestClassPermission:
	case RequestExtraCost:
		if req.AboveCostCeiling {
			r := res(RiskHigh, "tipo custo_extra acima do teto de custo")
			r.NoDelegation = true
			return r
		}
		return res(RiskMedium, "tipo custo_extra dentro do teto de custo")
	default:
		if r, ok := typeRisk[t]; ok {
			return res(r, "tipo "+string(t))
		}
		return res(RiskHigh, fmt.Sprintf("tipo de pedido desconhecido %q", t))
	}
	if req.Tool == nil {
		return res(RiskHigh, "pedido de permissão sem a ação")
	}
	if rules == nil {
		rules = &Rules{}
	}
	ev := providers.Event{Event: providers.EventPreTool, Cwd: req.Cwd, Tool: req.Tool}
	env.Exceptions, env.Edits = nil, nil
	e := newEvaluator(context.Background(), env, rules, ev, false)
	return e.classify(req.Tool, true)
}

// classify is the risk of an action. The pipeline classifies only what it
// left in ask, after the fixed rules (checkFixed false); on its own, an
// action a fixed rule covers is fixo.
func (e *evaluator) classify(t *providers.Tool, checkFixed bool) RiskResult {
	var out riskOut
	if checkFixed {
		if res := e.analyze(t); res.fixed.v == Deny {
			return RiskResult{Risk: RiskFixed, TableVersion: RiskTableVersion, Rule: res.fixed.reason}
		}
	}
	w := e.riskWalker()
	switch t.Kind {
	case providers.KindShell:
		out = w.line(t.Command, e.cwd(), dialectPOSIX, 0)
		if e.windows {
			out.raise(w.line(t.Command, e.cwd(), dialectPwsh, 0))
		}
	case providers.KindWeb:
		out = w.web(t.URL)
	case providers.KindMCP, providers.KindOther, providers.KindSubagent:
		out = w.tool(t.NativeName, t.Kind)
	default:
		out = riskOut{risk: RiskMedium, rule: fmt.Sprintf("ação %s sem linha na tabela", t.Kind)}
		for _, p := range t.Paths {
			out.raise(w.pathRisk(e.key(p)))
		}
	}
	return RiskResult{Risk: out.risk, TableVersion: RiskTableVersion, Rule: out.rule, Adjustment: out.adj}
}

// riskOut is the risk of a part with the rule that gave it.
type riskOut struct {
	risk Risk
	rule string
	adj  int
}

// raise keeps the higher risk; on a tie, the first rule.
func (o *riskOut) raise(n riskOut) {
	if o.risk == "" || n.risk.rank() > o.risk.rank() {
		*o = n
	}
}

func (o riskOut) set() bool { return o.risk != "" }

// riskWalker classifies the parts of an action.
type riskWalker struct {
	e               *evaluator
	domains         []string
	adj             *RiskAdjustments
	secrets, mestre []string
}

func (e *evaluator) riskWalker() *riskWalker {
	w := &riskWalker{e: e, domains: e.r.Network.Domains, secrets: DefaultSecretPaths, mestre: DefaultMestrePaths}
	if p := e.env.Risk; p != nil {
		w.adj = p.Adjustments
		if p.Secrets != nil {
			w.secrets = p.Secrets
		}
		if p.Mestre != nil {
			w.mestre = p.Mestre
		}
	}
	return w
}

// web classifies a web tool by the domain of its URL.
func (w *riskWalker) web(raw string) riskOut {
	host := ""
	if u, err := url.Parse(raw); err == nil {
		host = strings.ToLower(u.Hostname())
	}
	switch {
	case host == "":
		return riskOut{risk: RiskMedium, rule: "web sem URL"}
	case githubHost(host):
		return riskOut{risk: RiskHigh, rule: "web: URL do GitHub"}
	case w.inDomains(host):
		return riskOut{risk: RiskLow, rule: "web: domínio das regras efetivas"}
	}
	return riskOut{risk: RiskMedium, rule: "web: domínio fora das regras efetivas"}
}

func (w *riskWalker) inDomains(host string) bool {
	for _, d := range w.domains {
		if matchDomain(d, host) {
			return true
		}
	}
	return false
}

// destructiveWords make a tool alto by its native name.
var destructiveWords = []string{"delete", "drop", "destroy", "purge", "truncate"}

// tool classifies an mcp, other or subagent tool by its native name: the
// owner's adjustments first, then the default table.
func (w *riskWalker) tool(name string, kind providers.ToolKind) riskOut {
	if w.adj != nil {
		for _, a := range w.adj.Items {
			if a.Tool != "" && matchText(a.Tool, name) {
				return riskOut{risk: a.Risk, rule: a.rule(), adj: a.Index}
			}
		}
	}
	return defaultToolRisk(name, kind)
}

func defaultToolRisk(name string, kind providers.ToolKind) riskOut {
	n := strings.ToLower(name)
	switch {
	case matchText("mcp__github__*", n):
		return riskOut{risk: RiskHigh, rule: "ferramenta: mcp__github__*"}
	}
	for _, d := range destructiveWords {
		if strings.Contains(n, d) {
			return riskOut{risk: RiskHigh, rule: "ferramenta: nome com " + d}
		}
	}
	if kind == providers.KindSubagent {
		// the character itself, with its own rules (ADR 0025)
		return riskOut{risk: RiskLow, rule: "subagente: o próprio personagem"}
	}
	return riskOut{risk: RiskMedium, rule: "ferramenta sem linha na tabela"}
}

// githubHost is a host of GitHub: github.com, its subdomains (api.github.com)
// and *.githubusercontent.com.
func githubHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	return host == "github.com" || strings.HasSuffix(host, ".github.com") || host == "githubusercontent.com" || strings.HasSuffix(host, ".githubusercontent.com")
}

// pathRisk is the floor a literal path puts on its part: alto for a path of
// the secrets or of the Mestre, medio outside the worktree.
func (w *riskWalker) pathRisk(k string) riskOut {
	e := w.e
	rel, inside := e.rel(k)
	if !inside {
		rel = strings.TrimLeft(k, "/")
		if len(rel) >= 2 && rel[1] == ':' {
			rel = strings.TrimLeft(rel[2:], "/")
		}
	}
	for _, g := range w.secrets {
		if matchGlob(e.foldPat(g), rel) {
			return riskOut{risk: RiskHigh, rule: fmt.Sprintf("caminho de segredo %s (%s)", e.display(k), g)}
		}
	}
	if inside {
		for _, g := range w.mestre {
			if matchGlob(e.foldPat(g), rel) {
				return riskOut{risk: RiskHigh, rule: fmt.Sprintf("caminho do Mestre %s (%s)", e.display(k), g)}
			}
		}
		return riskOut{}
	}
	return riskOut{risk: RiskMedium, rule: "caminho fora do worktree: " + e.display(k)}
}
