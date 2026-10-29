package decrees

import (
	"net/url"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/i18n"
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
		return "", refuse(CodeLevel, "decree.author_inactive")
	}
	if strings.TrimSpace(p.Text) == "" {
		return "", refuse(CodeInvalid, "decree.text_required")
	}
	if p.Workspace == "" {
		return "", refuse(CodeInvalid, "decree.workspace_required")
	}
	switch p.Kind {
	case KindRule:
		if err := users.Require(u.Level, users.PermanentDecree); err != nil {
			return "", refuseMsg(CodeLevel, levelMsg(err))
		}
	case KindException:
		if err := users.Require(u.Level, users.OneOffException); err != nil {
			return "", refuseMsg(CodeLevel, levelMsg(err))
		}
	default:
		return "", refuse(CodeInvalid, "decree.kind_invalid")
	}
	if !u.Acts(p.Workspace) {
		return "", refuse(CodeWorkspace, "decree.workspace_not_yours", "workspace", p.Workspace)
	}
	if err := checkTarget(p); err != nil {
		return "", err
	}
	if p.Kind == KindRule && !p.Until.IsZero() && !p.Until.After(now) {
		return "", refuse(CodeInvalid, "decree.validity_passed")
	}
	if p.Kind == KindException {
		if p.Action == nil || p.Effect.Kind != EffectPermission {
			return "", refuse(CodeInvalid, "decree.exception_exact_action")
		}
	}
	if len(p.TargetRef) > 0 {
		if _, err := chain.Impress(p.TargetRef); err != nil {
			return "", refuse(CodeInvalid, "decree.exception_state_unreadable", "reason", err.Error())
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
			return "", refuse(CodeInvalid, "decree.behavior_too_long")
		}
		return "", nil
	case EffectPolicy:
		return "", checkPolicy(p)
	case EffectCeiling:
		if err := users.Require(u.Level, users.RaiseCeiling); err != nil {
			return "", refuseMsg(CodeLevel, levelMsg(err))
		}
		if p.Effect.Allow == nil || emptyGrant(p.Effect.Allow) {
			return "", refuse(CodeInvalid, "decree.ceiling_needs_allow")
		}
		if ref := invariant(p.Effect.Allow); ref != nil {
			return "", ref
		}
		return "amplia o teto da campanha", nil
	case EffectDelegation:
		return checkDelegation(p, u, now)
	}
	return "", refuse(CodeInvalid, "decree.effect_unknown", "effect", p.Effect.Kind)
}

func checkTarget(p Proposal) *Refusal {
	switch p.Target.Kind {
	case TargetClass, TargetCharacter, TargetWorkspace, TargetTable:
	default:
		return refuse(CodeInvalid, "decree.target_kind")
	}
	if strings.TrimSpace(p.Target.Name) == "" {
		return refuse(CodeInvalid, "decree.target_name_required")
	}
	if p.Target.Kind == TargetWorkspace && p.Target.Name != p.Workspace {
		return refuse(CodeInvalid, "decree.target_workspace_mismatch", "target", p.Target.Name, "workspace", p.Workspace)
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
		return refuse(CodeBuiltIn, "decree.class_built_in")
	}
	return refuse(CodeInvalid, "decree.class_not_edited")
}

func emptyGrant(g *classes.Grant) bool {
	return g == nil || len(g.Files)+len(g.Commands)+len(g.Tools)+len(g.Domains) == 0
}

func checkPermission(p Proposal) (string, *Refusal) {
	e := p.Effect
	if emptyGrant(e.Allow) && emptyGrant(e.Deny) && (p.Kind == KindRule || p.Action == nil) {
		return "", refuse(CodeInvalid, "decree.permission_kinds")
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
				return refuse(CodeInvariant, "decree.allow_all", "pattern", pat)
			}
		}
	}
	for _, f := range g.Files {
		l := strings.ToLower(strings.ReplaceAll(f, "\\", "/"))
		bare := strings.Trim(strings.ReplaceAll(l, "*", ""), "/ ")
		for _, bad := range protected {
			if strings.Contains(l, bad) || (len(bare) >= 3 && strings.HasPrefix(bad, bare)) {
				return refuse(CodeInvariant, "decree.file_never", "file", f)
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
					return refuse(CodeInvariant, "decree.agent_cli_never", "command", c)
				}
			}
		}
		if strings.Contains(l, "mestre vault") || strings.Contains(l, "mestre secret") {
			return refuse(CodeInvariant, "decree.vault_never", "command", c)
		}
	}
	return nil
}

func checkPolicy(p Proposal) *Refusal {
	e := p.Effect
	if strings.TrimSpace(e.Setting) == "" {
		return refuse(CodeInvalid, "decree.policy_needs_type")
	}
	if p.Target.Kind == TargetCharacter {
		return refuse(CodeInvalid, "decree.policy_target")
	}
	if !e.Loosens {
		// a decree of policy tightens any type, the high ones too ("*" is all of them)
		if e.Setting != "*" && e.Setting != "tudo" && !chain.KnownPolicyType(e.Setting) {
			return refuse(CodeInvalid, "decree.policy_tighten_type", "setting", e.Setting)
		}
		return nil
	}
	if protections[e.Setting] {
		return refuse(CodeInvariant, "decree.policy_github_protection", "setting", e.Setting)
	}
	if highTypes[e.Setting] || fixedTypes[e.Setting] {
		return refuse(CodePolicy, "decree.policy_not_loosened", "setting", e.Setting)
	}
	if !chain.LoosenablePolicySetting(e.Setting) {
		return refuse(CodeInvalid, "decree.policy_loosen_area", "setting", e.Setting)
	}
	return nil
}

func checkDelegation(p Proposal, u users.User, now time.Time) (string, *Refusal) {
	e := p.Effect
	if err := users.Require(u.Level, users.DelegateHigh); err != nil {
		return "", refuseMsg(CodeLevel, levelMsg(err))
	}
	t := e.Setting
	switch {
	case t == string(classes.RequestOperator):
		if err := users.Require(u.Level, users.OperatorActivation); err != nil {
			return "", refuse(CodeLevel, "decree.operator_delegation_owner_only", "reason", levelMsg(err))
		}
		if !strings.HasPrefix(p.Scope, "missao:") && !strings.HasPrefix(p.Scope, "classe:") || strings.HasSuffix(p.Scope, ":") {
			return "", refuse(CodeDelegation, "decree.operator_scope_required")
		}
		if p.Until.IsZero() || !p.Until.After(now) || p.Until.Sub(now) > MaxOperatorDelegation {
			return "", refuse(CodeDelegation, "decree.operator_validity_required")
		}
		if e.MaxMinutes < 0 || e.MaxMinutes > DefaultOperatorMinutes {
			return "", refuse(CodeDelegation, "decree.operator_minutes", "minutes", DefaultOperatorMinutes)
		}
	case highTypes[t]:
		if p.Mesa == "" && p.Scope == "" {
			return "", refuse(CodeDelegation, "decree.scope_required")
		}
		if p.Scope != "" && !validDecisionScope(p.Scope) {
			return "", refuse(CodeDelegation, "decree.delegation_scope_unknown", "scope", p.Scope)
		}
		if !p.Until.IsZero() && p.Until.Sub(now) > MaxDelegation {
			return "", refuse(CodeDelegation, "decree.delegation_validity_max", "until", p.Until.UTC())
		}
	default:
		return "", refuse(CodeDelegation, "decree.delegation_type_not_allowed", "type", t)
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

// describeEffect is the sentence of the confirmation and of /decretos, in pt-BR (what
// is kept in the audit). describeMsg is the same as a message.
func describeEffect(e Effect, t Target, kind Kind, a *classes.Action) string {
	return i18n.Render(i18n.PtBR, describeMsg(e, t, kind, a))
}

// describeMsg is the sentence of the confirmation and of /decretos as a message.
func describeMsg(e Effect, t Target, kind Kind, a *classes.Action) i18n.Msg {
	var for_ i18n.Msg
	switch t.Kind {
	case TargetClass:
		for_ = i18n.M("decree.for_class", "name", t.Name)
	case TargetCharacter:
		for_ = i18n.M("decree.for_character", "name", t.Name)
	case TargetWorkspace:
		for_ = i18n.M("decree.for_workspace", "name", t.Name)
	case TargetTable:
		for_ = i18n.M("decree.for_table", "name", t.Name)
	}
	switch e.Kind {
	case EffectPermission:
		var parts []i18n.Msg
		if a != nil {
			parts = append(parts, i18n.M("decree.allow_once", "action", a.String()))
		}
		if g := grantMsg(e.Allow); !g.IsZero() {
			parts = append(parts, i18n.M("decree.allow", "what", g))
		}
		if g := grantMsg(e.Deny); !g.IsZero() {
			parts = append(parts, i18n.M("decree.deny", "what", g))
		}
		return i18n.Concat(i18n.Join(i18n.Text("; "), parts...), for_)
	case EffectBehavior:
		return i18n.M("decree.instruct", "for", for_, "text", strings.TrimSpace(e.Instruction))
	case EffectPolicy:
		verb := i18n.M("decree.policy_tighten", "setting", e.Setting)
		if e.Loosens {
			verb = i18n.M("decree.policy_loosen", "setting", e.Setting)
		}
		return i18n.Concat(verb, for_)
	case EffectCeiling:
		return i18n.Concat(i18n.M("decree.ceiling_raise", "what", grantMsg(e.Allow)), for_)
	case EffectDelegation:
		return i18n.Concat(i18n.M("decree.delegation_effect", "setting", e.Setting), for_)
	}
	_ = kind
	return i18n.Concat(i18n.Text(e.Kind), for_)
}

func grantMsg(g *classes.Grant) i18n.Msg {
	if g == nil {
		return i18n.Msg{}
	}
	var parts []i18n.Msg
	add := func(code i18n.Msg, l []string) {
		if len(l) > 0 {
			parts = append(parts, i18n.M(code.Code, "list", strings.Join(l, ", ")))
		}
	}
	add(i18n.M("decree.grant_files"), g.Files)
	add(i18n.M("decree.grant_commands"), g.Commands)
	add(i18n.M("decree.grant_tools"), g.Tools)
	add(i18n.M("decree.grant_domains"), g.Domains)
	return i18n.Join(i18n.Text("; "), parts...)
}

// hostOf is the host of a URL ("" when it has none).
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
