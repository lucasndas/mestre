package decrees

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/users"
)

// MaxOperatorDelegation is the longest validity of the delegation of the
// activation of the Operator (docs/telegram/decretos.md#delegação-da-ativação-do-operador).
const MaxOperatorDelegation = 7 * 24 * time.Hour

// DefaultOperatorMinutes is the longest activation under that delegation.
const DefaultOperatorMinutes = 30

// highTypes are the types of request of risk alto: a decree of policy never
// loosens them; the Grand Master decides them only under a decree of
// delegation (docs/aprovacoes/politica.md#decretos-ajustam-a-política).
var highTypes = map[string]bool{
	string(classes.RequestWorkflows):      true,
	string(classes.RequestRepoSettings):   true,
	string(classes.RequestWorkspaceClass): true,
	string(classes.RequestDestructive):    true,
	string(classes.RequestAgentsMD):       true,
}

// fixedTypes are the types that are outside the chain: no decree reaches them
// but the delegation of the Operator.
var fixedTypes = map[string]bool{
	string(classes.RequestOperator):         true,
	string(classes.RequestMestreUpdate):     true,
	string(classes.RequestGuildEntry):       true,
	string(classes.RequestCitadelPromotion): true,
}

// protections are the settings of GitHub that the engine can not get around
// for anybody (docs/arquitetura/precedencia.md#invariantes-arquiteturais).
var protections = map[string]bool{
	"revisao_obrigatoria": true, "protecao_do_github": true, "protecao_de_branch": true, "checks_obrigatorios": true,
}

// check is every refusal that comes before the confirmation. u is the author
// as stored now. It returns why the PIN is needed ("" when it is not).
func check(p Proposal, u users.User, now time.Time) (string, *Refusal) {
	if u.State != users.Active {
		return "", refuse(CodeLevel, "o autor não está ativo")
	}
	if strings.TrimSpace(p.Text) == "" {
		return "", refuse(CodeInvalid, "a proposta precisa do texto original da ordem")
	}
	if p.Workspace == "" {
		return "", refuse(CodeInvalid, "a proposta precisa da campanha")
	}
	switch p.Kind {
	case KindRule:
		if err := users.Require(u.Level, users.PermanentDecree); err != nil {
			return "", refuse(CodeLevel, "%s", err.Error())
		}
	case KindException:
		if err := users.Require(u.Level, users.OneOffException); err != nil {
			return "", refuse(CodeLevel, "%s", err.Error())
		}
	default:
		return "", refuse(CodeInvalid, "o tipo do decreto é regra permanente ou exceção pontual")
	}
	if !u.Acts(p.Workspace) {
		return "", refuse(CodeWorkspace, "a campanha %q não está entre os seus", p.Workspace)
	}
	if err := checkTarget(p); err != nil {
		return "", err
	}
	if p.Kind == KindRule && !p.Until.IsZero() && !p.Until.After(now) {
		return "", refuse(CodeInvalid, "a validade já passou")
	}
	if p.Kind == KindException {
		if p.Action == nil || p.Effect.Kind != EffectPermission {
			return "", refuse(CodeInvalid, "uma exceção pontual libera uma ação exata")
		}
	}
	if len(p.TargetRef) > 0 {
		if _, err := chain.Impress(p.TargetRef); err != nil {
			return "", refuse(CodeInvalid, "o estado a que a exceção se prende não pode ser lido: %v", err)
		}
	}
	if ref := checkBuiltIn(p); ref != nil {
		return "", ref
	}
	switch p.Effect.Kind {
	case EffectPermission:
		return checkPermission(p)
	case EffectBehavior:
		if strings.TrimSpace(p.Effect.Instruction) == "" || len([]rune(p.Effect.Instruction)) > 500 {
			return "", refuse(CodeInvalid, "o comportamento é uma instrução curta, de até 500 caracteres")
		}
		return "", nil
	case EffectPolicy:
		return "", checkPolicy(p)
	case EffectCeiling:
		if err := users.Require(u.Level, users.RaiseCeiling); err != nil {
			return "", refuse(CodeLevel, "%s", err.Error())
		}
		if p.Effect.Allow == nil || emptyGrant(p.Effect.Allow) {
			return "", refuse(CodeInvalid, "ampliar o teto precisa dizer o que o teto passa a permitir")
		}
		if ref := invariant(p.Effect.Allow); ref != nil {
			return "", ref
		}
		return "amplia o teto da campanha", nil
	case EffectDelegation:
		return checkDelegation(p, u, now)
	}
	return "", refuse(CodeInvalid, "efeito desconhecido: %q (os efeitos são permission, behavior, policy, ceiling e delegation)", p.Effect.Kind)
}

func checkTarget(p Proposal) *Refusal {
	switch p.Target.Kind {
	case TargetClass, TargetCharacter, TargetWorkspace, TargetTable:
	default:
		return refuse(CodeInvalid, "o alvo é uma classe, um personagem, uma campanha ou uma mesa")
	}
	if strings.TrimSpace(p.Target.Name) == "" {
		return refuse(CodeInvalid, "o alvo precisa de um nome")
	}
	if p.Target.Kind == TargetWorkspace && p.Target.Name != p.Workspace {
		return refuse(CodeInvalid, "o alvo campanha %q não é o escopo %q", p.Target.Name, p.Workspace)
	}
	return nil
}

// checkBuiltIn refuses what changes the class `orquestrador` itself
// (docs/classes/classe-orquestrador.md#regras-e-erros; CA-DEC-12). What a
// decree can do to an orchestrator is what it does to any character: free or
// forbid an action in its effective rules.
func checkBuiltIn(p Proposal) *Refusal {
	if p.Effect.Kind != EffectClassEdit {
		return nil
	}
	if p.Target.Kind == TargetClass && p.Target.Name == classes.ReservedOrchestrator {
		return refuse(CodeBuiltIn, "a classe `orquestrador` é embutida no binário: nenhum decreto muda os campos, as regras fixas ou o files.deny dela; um decreto pode liberar ou proibir uma ação aos orquestradores, como a qualquer personagem")
	}
	return refuse(CodeInvalid, "um decreto não edita uma classe: a classe muda por `mestre class`, e o decreto muda a regra efetiva")
}

func emptyGrant(g *classes.Grant) bool {
	return g == nil || len(g.Files)+len(g.Commands)+len(g.Tools)+len(g.Domains) == 0
}

func checkPermission(p Proposal) (string, *Refusal) {
	e := p.Effect
	if emptyGrant(e.Allow) && emptyGrant(e.Deny) && (p.Kind == KindRule || p.Action == nil) {
		return "", refuse(CodeInvalid, "uma permissão libera ou proíbe arquivos, comandos, ferramentas ou domínios")
	}
	if ref := invariant(e.Allow); ref != nil {
		return "", ref
	}
	if p.Action != nil {
		if ref := invariant(&classes.Grant{Commands: commandOf(p.Action), Files: p.Action.Paths}); ref != nil {
			return "", ref
		}
	}
	why := ""
	if p.Kind == KindRule && !emptyGrant(e.Allow) {
		why = "regra permanente que libera"
	}
	if g := loosening(e.Allow); g != "" {
		why = g
	}
	if p.Action != nil {
		if g := loosening(&classes.Grant{Commands: commandOf(p.Action)}); g != "" {
			why = g
		}
	}
	return why, nil
}

func commandOf(a *classes.Action) []string {
	if a.Kind == providers.KindShell && a.Command != "" {
		return []string{a.Command}
	}
	return nil
}

// normCommand is a command as the checks read it: lower case, one space between
// words, without the assignments of the environment, `env`, `command`, `exec`,
// `nohup` in front and without the folder of the program.
func normCommand(c string) string {
	words := strings.Fields(strings.ToLower(strings.NewReplacer(`"`, " ", `'`, " ").Replace(c)))
	for len(words) > 0 {
		w := words[0]
		if strings.Contains(w, "=") && !strings.HasPrefix(w, "-") || w == "env" || w == "command" || w == "exec" || w == "nohup" || w == "time" {
			words = words[1:]
			continue
		}
		break
	}
	if len(words) > 0 {
		if i := strings.LastIndexAny(words[0], `/\`); i >= 0 {
			words[0] = words[0][i+1:]
		}
	}
	return strings.Join(words, " ")
}

// loosening says why the patterns loosen the security enough to ask the PIN
// even where the rest would not: a push or a merge, a destructive command.
func loosening(g *classes.Grant) string {
	if g == nil {
		return ""
	}
	for _, raw := range g.Commands {
		c := normCommand(raw)
		switch {
		case strings.HasPrefix(c, "git push"), strings.HasPrefix(c, "git merge"), strings.HasPrefix(c, "gh pr merge"):
			return "libera push ou merge"
		case strings.HasPrefix(c, "rm "), strings.HasPrefix(c, "git reset --hard"), strings.HasPrefix(c, "git clean"), strings.HasPrefix(c, "sudo"),
			strings.Contains(c, "--force"), strings.Contains(c, "-delete"), strings.Contains(c, "drop table"), strings.Contains(c, "truncate"),
			c == "*" || strings.HasPrefix(c, "* "):
			return "libera um comando destrutivo"
		}
	}
	return ""
}

// invariant refuses what the agent never has by a decree: the vault, the files
// of hooks and of configuration of the Mestre, the CLIs of agents started by the
// shell (docs/arquitetura/precedencia.md#invariantes-arquiteturais). What the
// engine can do for the agent (the push of the branch of the table, by `mestre
// pr update`) is not refused here: the decree is kept, and the engine fulfils it.
func invariant(g *classes.Grant) *Refusal {
	if g == nil {
		return nil
	}
	protected := []string{".mestre", ".claude/settings", ".codex/", ".gemini", "mestre.db", "cofre", "vault"}
	for _, list := range [][]string{g.Files, g.Tools, g.Domains, g.Commands} {
		for _, pat := range list {
			if strings.Trim(pat, "*/ ") == "" {
				return refuse(CodeInvariant, "%q libera tudo, o que inclui o cofre, os arquivos do Mestre e os CLIs de agentes, que o agente nunca recebe; o decreto é recusado", pat)
			}
		}
	}
	for _, f := range g.Files {
		l := strings.ToLower(strings.ReplaceAll(f, "\\", "/"))
		bare := strings.Trim(strings.ReplaceAll(l, "*", ""), "/ ")
		for _, bad := range protected {
			if strings.Contains(l, bad) || (len(bare) >= 3 && strings.HasPrefix(bad, bare)) {
				return refuse(CodeInvariant, "o agente nunca recebe %q: são arquivos de hooks e de configuração do Mestre ou o cofre, que só o motor toca; o decreto é recusado", f)
			}
		}
	}
	for _, c := range g.Commands {
		l := normCommand(c)
		for _, w := range strings.Fields(strings.NewReplacer(";", " ", "&", " ", "|", " ", "(", " ", ")", " ").Replace(l)) {
			w = strings.TrimLeft(w, "./")
			if i := strings.LastIndexAny(w, `/\`); i >= 0 {
				w = w[i+1:]
			}
			for _, cli := range []string{"claude", "codex", "agy", "gemini"} {
				if w == cli || strings.HasPrefix(w, cli+"*") || (strings.HasSuffix(w, "*") && w != "*" && strings.HasPrefix(cli, strings.TrimRight(w, "*"))) {
					return refuse(CodeInvariant, "o agente não inicia CLIs de agente pelo shell (%q): personagens nascem só pelo motor, de fichas e bases", c)
				}
			}
		}
		if strings.Contains(l, "mestre vault") || strings.Contains(l, "mestre secret") {
			return refuse(CodeInvariant, "o agente nunca tem acesso ao cofre (%q)", c)
		}
	}
	return nil
}

func checkPolicy(p Proposal) *Refusal {
	e := p.Effect
	if strings.TrimSpace(e.Setting) == "" {
		return refuse(CodeInvalid, "um decreto de política diz o tipo de pedido ou a condição que ajusta")
	}
	if p.Target.Kind == TargetCharacter {
		return refuse(CodeInvalid, "um decreto de política vale para a campanha, a mesa ou a classe, não para um personagem")
	}
	if !e.Loosens {
		// a decree of policy tightens any type, the high ones too ("*" is all of them)
		if e.Setting != "*" && e.Setting != "tudo" && !chain.KnownPolicyType(e.Setting) {
			return refuse(CodeInvalid, "apertar a política precisa de um tipo de pedido (como acao_destrutiva) ou de * para todos; %q não é um", e.Setting)
		}
		return nil
	}
	if protections[e.Setting] {
		return refuse(CodeInvariant, "contornar a proteção do GitHub (%s) nem o motor pode: o decreto é recusado", e.Setting)
	}
	if highTypes[e.Setting] || fixedTypes[e.Setting] {
		return refuse(CodePolicy, "um decreto de política não afrouxa o tipo %q (risco alto ou fixo); para o Grão-Mestre decidir um tipo alto, só um decreto `delegation` do Senhor da Guilda (docs/aprovacoes/delegacao.md)", e.Setting)
	}
	if !chain.LoosenablePolicySetting(e.Setting) {
		return refuse(CodeInvalid, "afrouxar a política precisa de uma área sensível (como migracoes) ou de um tipo de risco medio; %q não é um (as áreas do motor e dos segredos nunca são abertas)", e.Setting)
	}
	return nil
}

func checkDelegation(p Proposal, u users.User, now time.Time) (string, *Refusal) {
	e := p.Effect
	if err := users.Require(u.Level, users.DelegateHigh); err != nil {
		return "", refuse(CodeLevel, "%s", err.Error())
	}
	t := e.Setting
	switch {
	case t == string(classes.RequestOperator):
		if err := users.Require(u.Level, users.OperatorActivation); err != nil {
			return "", refuse(CodeLevel, "a delegação da ativação do Operador é só do Dono: %s", err.Error())
		}
		if !strings.HasPrefix(p.Scope, "missao:") && !strings.HasPrefix(p.Scope, "classe:") || strings.HasSuffix(p.Scope, ":") {
			return "", refuse(CodeDelegation, "a delegação da ativação do Operador precisa de escopo: `missao:<nome>` ou `classe:<nome>`")
		}
		if p.Until.IsZero() || !p.Until.After(now) || p.Until.Sub(now) > MaxOperatorDelegation {
			return "", refuse(CodeDelegation, "a delegação da ativação do Operador tem validade obrigatória, de no máximo 7 dias")
		}
		if e.MaxMinutes < 0 || e.MaxMinutes > DefaultOperatorMinutes {
			return "", refuse(CodeDelegation, "cada ativação delegada vai até %d minutos", DefaultOperatorMinutes)
		}
	case highTypes[t]:
		if p.Mesa == "" && p.Scope == "" {
			return "", refuse(CodeDelegation, "a delegação precisa de escopo: a mesa, `classe:<nome>` ou `repo:<dono/nome>`")
		}
		if p.Scope != "" && !validDecisionScope(p.Scope) {
			return "", refuse(CodeDelegation, "o escopo de uma delegação é a mesa, `classe:<nome>` ou `repo:<dono/nome>`; %q não se entende", p.Scope)
		}
		if !p.Until.IsZero() && p.Until.Sub(now) > MaxDelegation {
			return "", refuse(CodeDelegation, "a delegação vale por no máximo 30 dias (pedida: até %s)", momentIn(p.Until, nil))
		}
	default:
		return "", refuse(CodeDelegation, "tipo não delegável: %q; o Grão-Mestre decide só os tipos de risco alto sob um decreto `delegation` (a entrada na Guilda, a promoção da Cidadela e o custo acima do teto nunca)", t)
	}
	return "delegação", nil
}

// validDecisionScope says the scope of a delegation of decision is one the
// engine understands: a repository or a class.
func validDecisionScope(scope string) bool {
	switch {
	case strings.HasPrefix(scope, "repo:"):
		return strings.Count(scope, "/") == 1 && !strings.HasSuffix(scope, "/") && !strings.HasPrefix(scope, "repo:/")
	case strings.HasPrefix(scope, "classe:"):
		return len(scope) > len("classe:")
	}
	return false
}

// describeEffect is the sentence of the confirmation and of /decretos.
func describeEffect(e Effect, t Target, kind Kind, a *classes.Action) string {
	for_ := ""
	switch t.Kind {
	case TargetClass:
		for_ = " para a classe " + t.Name
	case TargetCharacter:
		for_ = " para " + t.Name
	case TargetWorkspace:
		for_ = " na campanha " + t.Name
	case TargetTable:
		for_ = " na mesa " + t.Name
	}
	switch e.Kind {
	case EffectPermission:
		var parts []string
		if a != nil {
			parts = append(parts, "permitir uma vez "+a.String())
		}
		if g := grantText(e.Allow); g != "" {
			parts = append(parts, "permitir "+g)
		}
		if g := grantText(e.Deny); g != "" {
			parts = append(parts, "proibir "+g)
		}
		return strings.Join(parts, "; ") + for_
	case EffectBehavior:
		return fmt.Sprintf("instruir%s: %s", for_, strings.TrimSpace(e.Instruction))
	case EffectPolicy:
		verb := "apertar"
		if e.Loosens {
			verb = "afrouxar"
		}
		return fmt.Sprintf("%s a política de %s%s", verb, e.Setting, for_)
	case EffectCeiling:
		return "ampliar o teto com " + grantText(e.Allow) + for_
	case EffectDelegation:
		return fmt.Sprintf("deixar o Grão-Mestre decidir pedidos %q%s", e.Setting, for_)
	}
	_ = kind
	return e.Kind + for_
}

func grantText(g *classes.Grant) string {
	if g == nil {
		return ""
	}
	var parts []string
	add := func(label string, l []string) {
		if len(l) > 0 {
			parts = append(parts, label+" "+strings.Join(l, ", "))
		}
	}
	add("arquivos", g.Files)
	add("comandos", g.Commands)
	add("ferramentas", g.Tools)
	add("domínios", g.Domains)
	return strings.Join(parts, "; ")
}

// hostOf is the host of a URL ("" when it has none).
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
