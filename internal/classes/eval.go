package classes

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/lucasndas/mestre/internal/providers"
)

// outcome is the verdict of one check, with the reason and the layer.
type outcome struct {
	v      Verdict
	reason string
	layer  Layer
	decree int64
	level  Level // of the decree that decided
	// byDefault is an ask only because no rule covers the call (the
	// default of commands, tools or git.commit): classified baixo, it runs
	// directly (docs/classes/permissoes.md#pipeline-de-decisão).
	byDefault bool
}

var allowed = outcome{v: Allow}

// worse keeps the more restrictive outcome; on a tie, the first that names
// a layer.
// An ask is by default only if every part that asks is.
func (o *outcome) worse(n outcome) {
	switch {
	case n.v.rank() > o.v.rank():
		*o = n
	case n.v == o.v:
		def := o.byDefault && n.byDefault
		if o.layer == "" && n.layer != "" {
			*o = n
		}
		o.byDefault = def
	}
}

// result separates the fixed rules from the rest: the fixed rules decide
// before the exceptions, the rest after.
type result struct {
	fixed outcome // allow or deny
	rules outcome
	// denyRank is the level rank of the strongest deny decree that took a
	// part of the call; -1 for none.
	denyRank int
}

func newResult() result { return result{fixed: allowed, rules: allowed, denyRank: -1} }

func (r *result) merge(n result) {
	r.fixed.worse(n.fixed)
	r.rules.worse(n.rules)
	r.denyRank = max(r.denyRank, n.denyRank)
}

func (r *result) fixedDeny(reason string) {
	r.fixed.worse(outcome{v: Deny, reason: "regra fixa de segurança: " + reason, layer: LayerFixed})
}

func (r *result) rule(o outcome) {
	r.rules.worse(o)
	if o.v == Deny && o.layer == LayerDecree {
		r.denyRank = max(r.denyRank, o.level.rank())
	}
}

// atLeastAsk makes the rules outcome at least ask, for what cannot be
// analysed.
func (r *result) atLeastAsk(why string) {
	r.rules.worse(outcome{v: Ask, reason: "trecho que o despachante não analisa: " + why, layer: LayerFixed})
}

func (e *evaluator) analyze(t *providers.Tool) result {
	res := newResult()
	if e.env.OperatorBase && !e.r.Operator {
		res.rule(e.deny(e.r.originField("operator"), "a classe não pode ser usada no Operador do PC"))
	}
	switch t.Kind {
	case providers.KindRead, providers.KindSearch, providers.KindWrite, providers.KindEdit:
		if len(t.Paths) == 0 {
			// The adapter turns these into other; a decision on paths never
			// goes without them.
			res.fixedDeny(fmt.Sprintf("%s sem caminho", t.NativeName))
			return res
		}
	case providers.KindShell:
		if strings.TrimSpace(t.Command) == "" {
			res.fixedDeny(fmt.Sprintf("%s sem comando", t.NativeName))
			return res
		}
	}
	switch t.Kind {
	case providers.KindRead, providers.KindSearch:
		// A search reads the whole tree under its folder.
		for _, p := range t.Paths {
			res.merge(e.path(e.key(p), use{recursive: t.Kind == providers.KindSearch}))
		}
	case providers.KindWrite, providers.KindEdit:
		for _, p := range t.Paths {
			k := e.key(p)
			res.merge(e.path(k, use{write: true}))
			e.otherEditor(&res, k)
		}
	case providers.KindShell:
		res.merge(e.shell(t.Command, e.cwd(), 0))
	case providers.KindWeb:
		res.rule(e.web(t))
	case providers.KindInternal:
		// the provider's own bookkeeping (plans, tasks): nothing to decide
	default: // mcp, subagent, other and any kind this binary does not know
		e.computerControl(&res, t.NativeName)
		res.rule(e.tool(t.NativeName))
	}
	return res
}

// cwd is the folder a command starts in: the event's, or the worktree.
func (e *evaluator) cwd() string {
	if e.evCwd != "" {
		return e.evCwd
	}
	return e.env.Worktree
}

func (e *evaluator) deny(l Layer, why string) outcome {
	return outcome{v: Deny, reason: fmt.Sprintf("%s (%s)", why, l.label(e.r.Class)), layer: l}
}

// use is how a command touches a path.
type use struct {
	write     bool
	recursive bool // the use takes the whole tree under the path
}

// path checks one path: the fixed rules, then the decrees and the files
// block.
func (e *evaluator) path(k string, u use) result {
	res := newResult()
	if why := e.fixedPath(k, u); why != "" {
		res.fixedDeny(why)
	}
	res.rule(e.filePerm(k, u.write))
	return res
}

// protectedWrite are the files no agent tool writes, at any depth of the
// worktree: git, the Mestre's folder (scrolls and classes only through the
// CLI), the hooks and settings of the providers and the definitions of
// agents and subagents.
var protectedWrite = []string{
	"**/.git", "**/.git/**",
	"**/.mestre", "**/.mestre/**",
	"**/.claude/settings*.json",
	"**/.codex", "**/.codex/**",
	"**/.agents/hooks.json",
	"**/.agents/agents", "**/.agents/agents/**",
	"**/.claude/agents", "**/.claude/agents/**",
	// MCP servers and provider configuration the project owns, and the
	// slash commands the agent must not create or change.
	"**/.mcp.json",
	"**/.gemini", "**/.gemini/**",
	"**/.claude/commands", "**/.claude/commands/**",
	// The skills the Mestre generates (mestre-*): the shared one and the
	// managed copy. Other skills, owned by the project, are not protected.
	"**/.agents/skills/mestre-*", "**/.agents/skills/mestre-*/**",
	"**/.claude/skills/mestre-*", "**/.claude/skills/mestre-*/**",
}

// holdsProtected are the folders whose recursive removal takes protected
// files with it.
var holdsProtected = map[string]bool{
	"": true, ".claude": true, ".agents": true,
	".claude/skills": true, ".claude/commands": true, ".agents/skills": true,
}

// fixedPath is the reason a fixed rule denies the path, or "".
func (e *evaluator) fixedPath(k string, u use) string {
	if k == "" {
		return ""
	}
	if within(k, e.tmp) {
		return "" // the character's own MESTRE_TMP: the only exception
	}
	if e.windows {
		if why := windowsShare(k); why != "" {
			return why
		}
	}
	// A recursive use (a search, a removal) takes the whole tree under k.
	under := func(dir string) bool {
		d := e.key(dir)
		return within(k, d) || (u.recursive && within(d, k))
	}
	if !within(k, e.wt) {
		if l := e.env.Layout; under(l.Agents) {
			if rest, ok := strings.CutPrefix(k, e.key(l.Agents)+"/"); ok {
				if parts := strings.Split(rest, "/"); len(parts) >= 2 && parts[1] == "tmp" {
					return "a pasta temporária de outro personagem"
				}
			}
			return "as pastas dos personagens (retrato das regras, pastas temporárias) ficam fora do alcance dos agentes"
		}
		if under(e.env.Layout.Accounts) {
			return "as pastas dos perfis de conta (contas/), com as credenciais e os CLIs, ficam fora do alcance dos agentes"
		}
		if under(e.env.Layout.Workspaces) {
			return "o espelho e os worktrees de outras mesas"
		}
		for _, d := range e.env.Layout.Data {
			if under(d) {
				return "o cofre e as pastas de dados do Mestre ficam fora do alcance dos agentes"
			}
		}
	}
	if !u.write {
		return ""
	}
	if u.recursive && e.wt != "" && within(e.wt, k) && k != e.wt {
		return "a remoção levaria o worktree inteiro"
	}
	rel, inside := e.rel(k)
	if !inside {
		return ""
	}
	for _, p := range protectedWrite {
		if matchGlob(p, rel) {
			return fmt.Sprintf("%s não pode ser escrito por ferramenta do agente (git, .mestre/ só pela CLI, hooks e definições de agentes)", e.display(k))
		}
	}
	if u.recursive && holdsProtected[rel] {
		return fmt.Sprintf("apagar ou mover %s levaria arquivos protegidos (.git, .mestre, configurações de hooks e agentes)", e.display(k))
	}
	return ""
}

// filePerm is the decision of the decrees and of the files block.
func (e *evaluator) filePerm(k string, write bool) outcome {
	if within(k, e.tmp) {
		return allowed
	}
	rel, inside := e.rel(k)
	return e.decreeFor(func(g *Grant) []string { return g.Files }, func(p string) bool { return e.fileMatch(p, rel, k, inside) },
		func() outcome { return e.fileRules(k, rel, inside, write) })
}

// fileRules is the decision of the files block alone.
func (e *evaluator) fileRules(k, rel string, inside, write bool) outcome {
	shown := e.display(k)
	for _, p := range e.r.Files.Deny {
		if e.fileMatch(p, rel, k, inside) {
			return e.deny(e.r.originOf("files.deny", p), fmt.Sprintf("%s casa com files.deny %q", shown, p))
		}
	}
	if write {
		if !inside {
			// Who writes the whole worktree (**, the default) asks to write
			// outside it; a narrower list denies (ADR 0025).
			if slices.Contains(e.r.Files.Write, "**") {
				return outcome{v: Ask, reason: fmt.Sprintf("escrita fora do worktree: %s", shown), layer: e.r.originField("files.write")}
			}
			return e.deny(LayerClass, fmt.Sprintf("escrita fora do worktree: %s", shown))
		}
		for _, p := range e.r.Files.Write {
			if !isAbsSlash(p) && matchGlob(e.foldPat(p), rel) {
				return allowed
			}
		}
		return e.deny(e.r.originField("files.write"), fmt.Sprintf("%s fora de files.write %v", shown, e.r.Files.Write))
	}
	for _, p := range e.r.Files.Read {
		if e.fileMatch(p, rel, k, inside) {
			return allowed
		}
	}
	if !inside {
		return e.deny(LayerClass, fmt.Sprintf("leitura fora do worktree: %s", shown))
	}
	return e.deny(e.r.originField("files.read"), fmt.Sprintf("%s fora de files.read %v", shown, e.r.Files.Read))
}

func (e *evaluator) foldPat(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	if e.fold {
		p = strings.ToLower(p)
	}
	return p
}

// fileMatch matches a files pattern: an absolute one against the path, a
// relative one against the path inside the worktree.
func (e *evaluator) fileMatch(p, rel, k string, inside bool) bool {
	p = e.foldPat(p)
	if isAbsSlash(p) {
		return matchGlob(p, k)
	}
	return inside && matchGlob(p, rel)
}

func (e *evaluator) otherEditor(res *result, k string) {
	if e.env.Edits == nil || k == "" {
		return
	}
	if who, ok := e.env.Edits.OtherEditor(e.env.TableID, e.env.AgentID, k); ok {
		res.fixedDeny(fmt.Sprintf("%s editou %s no turno em andamento; espere ele terminar", who, e.display(k)))
	}
}

func (e *evaluator) computerControl(res *result, name string) {
	if e.env.OperatorBase {
		return
	}
	pats := e.env.ComputerControl
	if pats == nil {
		pats = DefaultComputerControl
	}
	n := strings.ToLower(name)
	for _, p := range pats {
		if matchText(strings.ToLower(p), n) {
			res.fixedDeny(fmt.Sprintf("%s controla o computador ou o navegador do usuário e só existe na base Operador do PC", name))
			return
		}
	}
}

// tool decides a tool by its native name with the tools block.
func (e *evaluator) tool(name string) outcome {
	return e.decreeFor(func(g *Grant) []string { return g.Tools }, func(p string) bool { return matchText(p, name) },
		func() outcome { return e.patterns("tools", e.r.Tools, []string{name}, name, false) })
}

// patterns applies a block of text patterns: the most restrictive of deny,
// ask and allow, then the default. forms are the texts deny and ask see;
// allowForm is the one allow sees; readOnly is allowed with no pattern.
func (e *evaluator) patterns(block string, pr PatternRules, forms []string, allowForm string, readOnly bool) outcome {
	shown := allowForm
	for _, p := range pr.Deny {
		for _, f := range forms {
			if matchText(p, f) {
				return e.deny(e.r.originOf(block+".deny", p), fmt.Sprintf("%q casa com %s.deny %q", shown, block, p))
			}
		}
	}
	for _, p := range pr.Ask {
		for _, f := range forms {
			if matchText(p, f) {
				return outcome{v: Ask, reason: fmt.Sprintf("%s.ask %q, %s", block, p, e.r.originOf(block+".ask", p).label(e.r.Class)), layer: e.r.originOf(block+".ask", p)}
			}
		}
	}
	if readOnly {
		return allowed
	}
	for _, p := range pr.Allow {
		if matchText(p, allowForm) {
			return allowed
		}
	}
	l := e.r.originField(block + ".default")
	switch pr.Default {
	case Allow:
		return allowed
	case Ask:
		return outcome{v: Ask, reason: fmt.Sprintf("sem regra em %s, padrão ask (%s)", block, l.label(e.r.Class)), layer: l, byDefault: true}
	}
	return e.deny(l, fmt.Sprintf("%q não casa com nenhuma regra de %s e o padrão é negar", shown, block))
}

// web decides a web tool by the domain of its URL.
func (e *evaluator) web(t *providers.Tool) outcome {
	host := ""
	if u, err := url.Parse(t.URL); err == nil {
		host = strings.ToLower(u.Hostname())
	}
	if host == "" {
		return e.network(t, host)
	}
	return e.decreeFor(func(g *Grant) []string { return g.Domains }, func(p string) bool { return matchDomain(p, host) },
		func() outcome { return e.network(t, host) })
}

// network is the decision of the network block alone.
func (e *evaluator) network(t *providers.Tool, host string) outcome {
	n := e.r.Network
	if !n.Internet {
		return e.deny(e.r.originField("network.internet"), "a classe não tem acesso à internet")
	}
	if len(n.Domains) == 0 {
		return allowed
	}
	if host == "" {
		return e.deny(e.r.originField("network.domains"), fmt.Sprintf("%s sem URL: a classe só acessa %v", t.NativeName, n.Domains))
	}
	for _, d := range n.Domains {
		if matchDomain(d, host) {
			return allowed
		}
	}
	return e.deny(e.r.originField("network.domains"), fmt.Sprintf("%s fora de network.domains %v", host, n.Domains))
}

// decreeFor applies the permission decrees (not the exceptions) that take
// the call over base, the decision of the effective rules alone
// (docs/arquitetura/precedencia.md#combinação-na-decisão-de-permissão).
// Among the decrees of persons, the one of the highest level decides; allow
// and deny at the same level suspend each other and the next level down is
// looked at. Without one, base holds, and the decrees of the agents of the
// approval chain (grand_master, table_master) only turn an ask of it into
// allow, never a deny; a deny of theirs only hardens. A decree of an
// unknown level does not count.
func (e *evaluator) decreeFor(list func(*Grant) []string, match func(string) bool, base func() outcome) outcome {
	return e.decreeSplit(list, match, match, base)
}

// decreeSplit is decreeFor with one match for the deny decrees and another
// for the allow ones (a command: deny sees every form, allow the text as
// written).
func (e *evaluator) decreeSplit(list func(*Grant) []string, matchDeny, matchAllow func(string) bool, base func() outcome) outcome {
	takes := func(g *Grant, match func(string) bool) bool {
		if g == nil {
			return false
		}
		for _, p := range list(g) {
			if match(p) {
				return true
			}
		}
		return false
	}
	at := func(rank int) (allow, deny *Decree) {
		for i := range e.r.Decrees {
			d := &e.r.Decrees[i]
			if d.Kind == DecreeException || d.Effect.Kind != EffectPermission || d.Level.rank() != rank || !d.active(e.now) {
				continue
			}
			if takes(d.Effect.Deny, matchDeny) && deny == nil {
				deny = d
			}
			if takes(d.Effect.Allow, matchAllow) && allow == nil {
				allow = d
			}
		}
		return allow, deny
	}
	denied := func(d *Decree) outcome {
		return outcome{v: Deny, reason: fmt.Sprintf("proibido pelo decreto %d", d.ID), layer: LayerDecree, decree: d.ID, level: d.Level}
	}
	for rank := LevelOwner.rank(); rank > LevelGrandMaster.rank(); rank-- {
		switch allow, deny := at(rank); {
		case allow != nil && deny != nil:
			continue // suspended by the conflict
		case deny != nil:
			return denied(deny)
		case allow != nil:
			return outcome{v: Allow, layer: LayerDecree, decree: allow.ID, level: allow.Level}
		}
	}
	o := base()
	switch allow, deny := at(LevelGrandMaster.rank()); {
	case allow != nil && deny != nil:
		// suspended by the conflict
	case deny != nil:
		o.worse(denied(deny))
	case allow != nil && o.v == Ask:
		o = outcome{v: Allow, layer: LayerDecree, decree: allow.ID, level: allow.Level}
	}
	return o
}
