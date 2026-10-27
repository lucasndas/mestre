package classes

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/providers"
)

// Env is what the decision knows about the character besides its effective
// rules: where it works, the machine's folders and the live state it asks
// about. The dispatcher builds it from the terminal registry.
type Env struct {
	AgentID, TableID string
	// Worktree is the character's worktree; Tmp its own MESTRE_TMP; Home
	// the home of the agents' account, for ~ (empty: ~ is outside the
	// worktree). Absolute and normalized like the paths of the events.
	Worktree, Tmp, Home string
	Layout              Layout
	// OperatorBase is true for a terminal of the Operador do PC base.
	OperatorBase bool
	// ComputerControl are the patterns of the MCP tools that control the
	// user's computer and browser, denied outside the Operador do PC base;
	// nil is DefaultComputerControl. The Owner configures the list
	// (docs/painel/administracao.md#operador-do-pc).
	ComputerControl []string
	// OS is the system of the machine ("" is this one): on windows a
	// command is analysed as POSIX and as PowerShell, and paths fold case.
	OS string
	// Normalize makes a path of a command argument absolute and resolves
	// its links like the adapter does for tool paths
	// (providers.Options.NormalizePath); nil only cleans it.
	Normalize func(p, cwd string) string
	// Git answers the state a git commit is checked against; nil cannot
	// check, and a commit is denied.
	Git GitState
	// Edits knows who edited what in the turns in progress of the table;
	// nil skips the check (the degraded hook does not know).
	Edits TurnEdits
	// Exceptions records the exceptions used; nil uses none (degraded).
	Exceptions ExceptionLedger
	// TableRules checks a temporary table rule at each use and records the
	// use; nil uses none (degraded, the attenuation, the classification).
	TableRules TableRuleGate
	// Risk is the owner's part of the risk classification of what asks
	// (the policy file, M5); nil is the default table.
	Risk *RiskPolicy
}

// Layout are the folders of the Mestre on the machine
// (docs/arquitetura/persistencia.md#arquivos) that the fixed rules close to
// every agent tool.
type Layout struct {
	// Data: configuration and vault key, databases, machine data, backups,
	// logs, sockets, the hook queue, global classes and models, captures,
	// the transcription.
	Data []string
	// Accounts is contas/: every account profile, with the credentials and
	// the provider CLIs installed there.
	Accounts string
	// Agents is agentes/: policy.json and the MESTRE_TMP of each character.
	Agents string
	// Workspaces holds the mirrors, the main clone and the worktrees of the
	// tables; only the character's own worktree is open.
	Workspaces string
}

// DefaultLayout is the Layout of an installed machine
// (docs/arquitetura/persistencia.md#arquivos): every folder of the Mestre,
// of every instance, is data; contas/, agentes/ and workspaces/ of the
// instance are named apart for the reason of the denial. programData is
// %ProgramData% on Windows.
func DefaultLayout(goos, programData, instance string) Layout {
	if goos == "windows" {
		root := filepath.Join(programData, "Mestre")
		inst := filepath.Join(root, instance)
		return Layout{
			Data: []string{root}, Accounts: filepath.Join(inst, "contas"),
			Agents: filepath.Join(inst, "maquina", "agentes"), Workspaces: filepath.Join(inst, "workspaces"),
		}
	}
	inst := filepath.Join("/var/lib/mestre", instance)
	return Layout{
		Data:     []string{"/etc/mestre", "/var/lib/mestre", "/var/spool/mestre", "/run/mestre", "/var/log/mestre"},
		Accounts: filepath.Join(inst, "contas"), Agents: filepath.Join(inst, "agentes"), Workspaces: filepath.Join(inst, "workspaces"),
	}
}

// DefaultComputerControl are the default patterns of the MCP tools of
// computer and browser control.
var DefaultComputerControl = []string{"mcp__claude-in-chrome__*", "mcp__computer-use__*"}

// GitState is the git of the worktree, for the checks of git commit.
type GitState interface {
	// Branch is the branch checked out in the worktree.
	Branch(ctx context.Context, worktree string) (string, error)
	// Committed lists the paths, relative to the worktree in slash form,
	// that the next commit takes: the staged ones and, with all, the
	// changed tracked files too (git commit -a or with paths).
	Committed(ctx context.Context, worktree string, all bool) ([]string, error)
}

// CommitState is what a decision on a commit needs of the git state, read at
// the moment of the decision.
type CommitState struct {
	// Branch is the branch checked out; BranchErr is why it is not known
	// (detached, unreadable), and Branch is then empty.
	Branch    string
	BranchErr error
	// Files are the paths the next commit takes (see GitState.Committed).
	Files []string
}

// CommitStater is the part of a GitState that answers the branch and the
// files of a commit in one reading (one git process). The decision uses it
// when the GitState has it, and asks Branch and Committed otherwise; the
// answer is the same. An implementation must read the real state each time:
// the index at the moment of the decision is what the decision judges, so it
// is never kept between decisions.
type CommitStater interface {
	CommitState(ctx context.Context, worktree string, all bool) (CommitState, error)
}

// TableScripts is the part of a GitState that tells a script of the
// repository from one created or changed at the table, for the risk of
// running it (docs/classes/classificacao-de-risco.md#sinais-de-risco). With
// a GitState that does not implement it, or none, every script of the
// worktree counts as changed.
type TableScripts interface {
	// Changed reports whether the file at path (absolute, in the worktree)
	// differs from the base of the table branch: its content, up to the
	// line endings, is not the one of the merge-base of the branch with the
	// main branch, or it is not there. A file that is not a regular file of
	// the worktree is changed; so is one it cannot check (err).
	Changed(ctx context.Context, worktree, path string) (bool, error)
}

// TurnEdits answers who else edited a file in a turn still in progress.
type TurnEdits interface {
	OtherEditor(tableID, agentID, path string) (who string, ok bool)
}

// ExceptionLedger records that an exception was used: Use is true only the
// first time for an id, atomically.
type ExceptionLedger interface {
	Use(ctx context.Context, agentID string, id int64) (bool, error)
}

// TableRuleGate is the engine side of the temporary table rules
// (docs/orquestracao/regras-temporarias-de-mesa.md): the rules travel in the
// effective rules, but the engine asks it again at each use. Use is true when
// the rule still holds now (not revoked, not suspended, not expired, its author
// still the Mestre da mesa) and, for a medio rule, the conditions of the table
// pass for this call; it then records the use. An error leaves the call in ask.
type TableRuleGate interface {
	Use(ctx context.Context, u TableRuleUse) (bool, error)
}

// TableRuleUse is what the gate is asked about: the rule that covers the call,
// the character that makes it and the call with its own classification.
type TableRuleUse struct {
	Rule             int64
	Level            Level
	Risk             Risk
	AgentID, TableID string
	Action           Action
	Tool             *providers.Tool
	// Own is the classification of this call alone, groups included.
	Own RiskResult
}

// Decision is the outcome of the pipeline for one tool call.
type Decision struct {
	Verdict Verdict
	// Reason is what the agent sees with a denial; for ask, what the
	// approval request is about (AskReason builds the denial).
	Reason string
	// Layer decided it.
	Layer Layer
	// Decree is the decree or exception that decided, if one did; TableRule
	// says it is a temporary table rule, whose numbers are another sequence.
	Decree    int64
	TableRule bool
	// ClassHooks is true when an allow still goes through the synchronous
	// pre_tool hooks of the class, which can only deny
	// (docs/classes/classes.md#hooks-extras-da-classe).
	ClassHooks bool
	// Action is the call as an exception would bind it.
	Action Action
	// Risk is the risk of the approval request an ask opens
	// (docs/classes/classificacao-de-risco.md), or of the default baixo
	// that let the call through; zero for the other verdicts and in the
	// degraded mode.
	Risk RiskResult
}

// Decide runs the decision pipeline of docs/classes/permissoes.md#pipeline-de-decisão
// over one pre_tool or permission_request: the locked system and the fixed
// rules deny and stop; an exception that matches the exact action allows
// and is used; then the decrees and the effective rules give allow, ask or
// deny. Any other event has nothing to decide and is allowed.
func Decide(ctx context.Context, env Env, rules *Rules, ev providers.Event) Decision {
	return decide(ctx, env, rules, ev, false)
}

// DecideDegraded is the decision of the hook with the engine down
// (docs/classes/permissoes.md#modo-degradado): the fixed rules and the last
// snapshot; what would ask is denied as DegradedReason, no exception is used.
// Without a snapshot, only the fixed rules hold and everything that is not
// read-only is denied as DegradedReason.
func DecideDegraded(ctx context.Context, env Env, snapshot *Rules, ev providers.Event) Decision {
	env.Exceptions, env.TableRules = nil, nil
	env.Edits = nil
	r := snapshot
	if r == nil {
		r = readOnlyRules(ev.AgentID)
	}
	d := decide(ctx, env, r, ev, true)
	switch {
	case d.Verdict == Ask:
		d.Verdict, d.Reason, d.Layer = Deny, DegradedReason, LayerDegraded
	case d.Verdict == Deny && snapshot == nil && d.Layer != LayerFixed:
		d.Reason, d.Layer = DegradedReason+" (sem o retrato das regras, só leitura)", LayerDegraded
	}
	return d
}

// readOnlyRules stand for a missing snapshot: reading the worktree and the
// read-only commands, nothing else.
func readOnlyRules(agentID string) *Rules {
	return &Rules{
		Version: RulesVersion, AgentID: agentID, FixedRulesVersion: FixedRulesVersion,
		Files:    FileRules{Read: []string{"**"}},
		Commands: PatternRules{Default: Deny}, Tools: PatternRules{Default: Deny},
		Git: GitRules{Workflows: WorkflowsNever},
	}
}

func decide(ctx context.Context, env Env, rules *Rules, ev providers.Event, degraded bool) Decision {
	return failClosed(func() Decision { return decideCounting(ctx, env, rules, ev, degraded, nil) })
}

// ReasonInternalError is the reason of the denial of a line the analysis fell
// on: no line may bring the process down, and a failure of the analysis is a
// denial, never an allow.
const ReasonInternalError = "falha interna na análise do comando; negado por segurança"

// failClosed runs the decision and turns a panic of the analysis into a fixed
// denial (the callers of the decision are the hook, the pre-run of the
// rituals and the engine: none of them may fall on a line).
func failClosed(run func() Decision) (d Decision) {
	defer func() {
		if r := recover(); r != nil {
			d = Decision{Verdict: Deny, Reason: ReasonInternalError, Layer: LayerFixed}
		}
	}()
	return run()
}

// decideCounting is decide that also reports, in work when it is not nil, the
// units the analysis spent (budget.go); the tests of the bound read it.
func decideCounting(ctx context.Context, env Env, rules *Rules, ev providers.Event, degraded bool, work *int) Decision {
	if ev.Event != providers.EventPreTool && ev.Event != providers.EventPermissionRequest {
		return Decision{Verdict: Allow}
	}
	if ev.Tool == nil {
		return Decision{Verdict: Deny, Reason: "evento de ferramenta sem a ferramenta", Layer: LayerFixed}
	}
	e := newEvaluator(ctx, env, rules, ev, degraded)
	if work != nil {
		defer func() { *work = e.work }()
	}
	act := e.action(ev.Tool)
	deny := func(reason string, l Layer) Decision {
		return Decision{Verdict: Deny, Reason: reason, Layer: l, Action: act}
	}
	switch {
	case rules == nil:
		return deny("sem regras efetivas para este personagem", LayerFixed)
	case ev.AgentID != "" && rules.AgentID != ev.AgentID:
		return deny("as regras efetivas são de outro personagem", LayerFixed)
	case rules.Locked:
		return deny("sistema travado: toda ferramenta está negada até o Senhor da Guilda destravar", LayerLocked)
	}
	res := e.analyze(ev.Tool)
	if res.fixed.v == Deny {
		return Decision{Verdict: Deny, Reason: res.fixed.reason, Layer: LayerFixed, Action: act}
	}
	if d, ok := e.exception(act, res); ok {
		return d
	}
	o := res.rules
	if d, ok := e.tableRule(ev.Tool, act, res); ok {
		return d
	}
	d := Decision{Verdict: o.v, Reason: o.reason, Layer: o.layer, Decree: o.decree, Action: act}
	switch o.v {
	case Allow:
		d.ClassHooks = true
	case Ask:
		d.Reason = act.String()
		if o.reason != "" {
			d.Reason += " (" + o.reason + ")"
		}
		if e.degraded {
			// The fail-safe: what asks is denied with the engine down, the
			// default baixo too (the owner's adjustments are not here).
			break
		}
		d.Risk = e.classify(ev.Tool, false)
		if o.byDefault && d.Risk.Risk == RiskLow {
			// No rule covers it and it is baixo: it runs directly, the
			// deterministic counterpart of the providers' auto mode (ADR 0025).
			d.Verdict, d.Reason, d.ClassHooks = Allow, "padrão: risco baixo ("+d.Risk.Rule+")", true
		}
	}
	return d
}

// exception looks for an exception bound to exactly this action. One of a
// person does not count when a deny decree of a higher level takes a part
// of it, nor when it is suspended by a deny decree of the same level. One
// of an agent of the approval chain (grand_master, table_master) only turns
// an ask into allow: it does not count over any deny
// (docs/arquitetura/precedencia.md#combinação-na-decisão-de-permissão). One
// of an unknown level never counts.
func (e *evaluator) exception(act Action, res result) (Decision, bool) {
	if e.env.Exceptions == nil {
		return Decision{}, false
	}
	for _, d := range e.r.Decrees {
		if d.Kind != DecreeException || d.Action == nil || !d.active(e.now) || !d.Action.same(act) {
			continue
		}
		switch {
		case d.Level.person():
			if res.denyRank >= d.Level.rank() {
				continue
			}
		case d.Level.agent():
			if res.rules.v != Ask {
				continue
			}
		default:
			continue
		}
		used, err := e.env.Exceptions.Use(e.ctx, e.r.AgentID, d.ID)
		if err != nil {
			return Decision{Verdict: Deny, Reason: fmt.Sprintf("não foi possível registrar o uso da exceção %d: tente de novo", d.ID), Layer: LayerException, Decree: d.ID, Action: act}, true
		}
		if !used {
			continue // already used: it does not count again
		}
		return Decision{Verdict: Allow, Reason: fmt.Sprintf("exceção pontual %d", d.ID), Layer: LayerException, Decree: d.ID, Action: act}, true
	}
	return Decision{}, false
}

// tableRule looks for a temporary table rule that lets an ask through. It only
// turns an ask into allow: an allow needs no rule, a deny (of a person's decree,
// the class, the ceiling, the sheet) stays, and so does an ask the engine could
// not analyse. The rule must cover the call by its own classification
// (PatternCovers: a pattern approved as baixo never frees a medio command) and
// the gate checks it again, now, before the call goes through.
func (e *evaluator) tableRule(t *providers.Tool, act Action, res result) (Decision, bool) {
	if e.degraded || e.env.TableRules == nil || res.rules.v != Ask || res.rules.layer == LayerFixed {
		return Decision{}, false
	}
	var own *RiskResult
	for i := range e.r.Decrees {
		d := &e.r.Decrees[i]
		if d.Kind != DecreeTableRule || !d.Level.agent() || !d.active(e.now) {
			continue
		}
		if own == nil {
			r := e.classify(t, false)
			own = &r
		}
		if !tableRuleCovers(*d, act, t, *own) {
			continue
		}
		ok, err := e.env.TableRules.Use(e.ctx, TableRuleUse{Rule: d.ID, Level: d.Level, Risk: d.Risk, AgentID: e.r.AgentID, TableID: e.env.TableID, Action: act, Tool: t, Own: *own})
		if err != nil || !ok {
			continue
		}
		return Decision{Verdict: Allow, Reason: fmt.Sprintf("regra temporária de mesa %d", d.ID), Layer: LayerDecree, Decree: d.ID, TableRule: true, Action: act, ClassHooks: true}, true
	}
	return Decision{}, false
}

// tableRuleCovers says whether a table rule covers the call: the exact action
// it was made for, or a command (a tool) its pattern matches, in both cases only
// if the classification of the call fits the risk of the rule and the call has
// nothing TableRuleBlocker names.
func tableRuleCovers(d Decree, act Action, t *providers.Tool, own RiskResult) bool {
	if !own.Risk.valid() || own.Risk == RiskFixed || own.Risk.rank() > d.Risk.rank() {
		return false
	}
	if TableRuleBlocker(act, t, own) != "" {
		return false
	}
	if d.Action != nil {
		return d.Action.same(act)
	}
	g := d.Effect.Allow
	if d.Effect.Kind != EffectPermission || g == nil {
		return false
	}
	list := g.Tools
	if d.ToolKind == providers.KindShell {
		list = g.Commands
	}
	if len(list) != 1 {
		return false
	}
	return PatternCovers(ApprovablePattern{Pattern: list[0], Kind: d.ToolKind, Risk: RiskResult{Risk: d.Risk}}, act, own)
}

// TableRuleBlocker says why no temporary table rule frees a call, "" when one
// may. It holds for the rule of the exact action as for the rule of a pattern,
// whoever made it, the Grão-Mestre included
// (docs/orquestracao/regras-temporarias-de-mesa.md#como-o-motor-decide-o-que-o-texto-deixa-em-aberto):
// the same text of a command does another thing at each use when the engine does
// not read all of it, so a rule never frees a part with no group (an unclosed
// quote, a brace list, a .NET call, the reading of the shell; also a write, a
// tool the table has no row for, or a web tool with no URL) nor a shell command
// that chains, pipes, redirects or substitutes, breaks the line, or begins or ends
// with a space only the engine reads as one. The command is read as the tool gave
// it, not as the action keeps it (the action trims it: a line break followed by a
// space of another script would go). The chain asks it again when it makes the
// rule of an exact action; tool is nil when it has none.
func TableRuleBlocker(act Action, tool *providers.Tool, own RiskResult) string {
	command := act.Command
	if tool != nil && tool.Kind == providers.KindShell {
		command = strings.Trim(tool.Command, " \t\r\n")
	}
	switch {
	case slices.Contains(own.Groups, GroupNone):
		return "the engine does not analyse a part of the call"
	case act.Kind == providers.KindShell && (strings.ContainsAny(command, ";&|`<>\n\r(){}") || strings.Contains(command, "$(")):
		return "the command chains, pipes, redirects or substitutes other commands"
	case act.Kind == providers.KindShell && strings.TrimSpace(command) != command:
		// The action keeps the command trimmed of every space of every script;
		// the shell skips only blanks and line ends, so "./run.sh" followed by a
		// form feed or a no-break space is another file for it.
		return "the command begins or ends with a character the engine reads as a space and the shell does not"
	}
	return ""
}

// action is the tool call as an exception binds it.
func (e *evaluator) action(t *providers.Tool) Action {
	a := Action{Kind: t.Kind, Tool: t.NativeName, URL: t.URL}
	switch t.Kind {
	case providers.KindShell:
		a.Command = strings.TrimSpace(t.Command)
	case providers.KindRead, providers.KindWrite, providers.KindEdit, providers.KindSearch:
		for _, p := range t.Paths {
			a.Paths = append(a.Paths, e.display(e.key(p)))
		}
	}
	return a
}

// RulesSource gives the effective rules of a character: the machine
// database (Store), keyed by MESTRE_AGENT_ID.
type RulesSource interface {
	Rules(ctx context.Context, agentID string) (*Rules, error)
}

// Policy decides for whichever character an event names: a hook from
// inside a native subagent carries the character's MESTRE_AGENT_ID and so
// goes through the character's rules
// (docs/classes/permissoes.md#subagentes-nativos).
type Policy struct {
	Source RulesSource
	Env    func(ctx context.Context, agentID string) (Env, error)
}

// Decide finds the rules and the environment of ev.AgentID and decides.
// A character without rules or environment is denied.
func (p *Policy) Decide(ctx context.Context, ev providers.Event) Decision {
	if ev.AgentID == "" {
		return Decision{Verdict: Deny, Reason: "evento sem MESTRE_AGENT_ID", Layer: LayerFixed}
	}
	env, err := p.Env(ctx, ev.AgentID)
	if err != nil {
		return Decision{Verdict: Deny, Reason: "personagem desconhecido nesta máquina", Layer: LayerFixed}
	}
	r, err := p.Source.Rules(ctx, ev.AgentID)
	if err != nil {
		r = nil
	}
	return Decide(ctx, env, r, ev)
}

type evaluator struct {
	ctx      context.Context
	env      Env
	r        *Rules
	now      time.Time
	fold     bool
	windows  bool
	degraded bool
	wt, tmp  string // keys
	evCwd    string
	work     int // the units the analysis spent (budget.go)
	// homeSet is the variable that moves the global configuration of git
	// (HOME, XDG_CONFIG_HOME…) set or removed by an earlier part of the line
	// being analysed, and homeScope the one set for the command being
	// analysed (a prefix, env): a git that runs under either is a fixed denial
	// (gitenv.go).
	homeSet, homeScope string
	// commitStates are the readings of the git state this one decision made
	// (by all): the analysis may reach the same git commit more than once
	// (the two readings of a line on Windows), and the decision reads the
	// real state once. It lives and dies with the decision.
	commitStates map[bool]commitReading
}

type commitReading struct {
	state CommitState
	err   error
}

// commitState reads the branch and the files of a commit once per decision.
func (e *evaluator) commitState(cs CommitStater, all bool) (CommitState, error) {
	if r, ok := e.commitStates[all]; ok {
		return r.state, r.err
	}
	state, err := cs.CommitState(e.ctx, e.env.Worktree, all)
	if e.commitStates == nil {
		e.commitStates = map[bool]commitReading{}
	}
	e.commitStates[all] = commitReading{state, err}
	return state, err
}

func newEvaluator(ctx context.Context, env Env, r *Rules, ev providers.Event, degraded bool) *evaluator {
	goos := env.OS
	if goos == "" {
		goos = runtime.GOOS
	}
	e := &evaluator{ctx: ctx, env: env, r: r, now: ev.ReceivedAt, windows: goos == "windows", degraded: degraded, evCwd: ev.Cwd}
	e.fold = e.windows
	e.wt, e.tmp = e.key(env.Worktree), e.key(env.Tmp)
	return e
}

// key is the form paths are compared in: clean, slash-separated and, on
// Windows, lower case. An empty path stays empty.
func (e *evaluator) key(p string) string {
	if p == "" {
		return ""
	}
	k := filepath.ToSlash(p)
	if e.windows {
		k = windowsKey(strings.ReplaceAll(k, `\`, "/"))
	}
	if e.windows && strings.HasPrefix(k, "//") {
		k = "/" + path.Clean(k[1:]) // a UNC path keeps its two slashes
	} else {
		k = path.Clean(k)
	}
	if e.fold {
		k = strings.ToLower(k)
	}
	return k
}

// windowsKey undoes the spellings Windows takes for the same file: the
// \\?\ and \\.\ prefixes, the administrative share of a drive of this
// machine (\\localhost\C$\x is C:\x), the trailing dots and spaces it drops
// from a name, and an alternate data stream (.git::$INDEX_ALLOCATION is
// .git). A device path that is not a drive (\\?\GLOBALROOT\…,
// \\?\Volume{…}\…, \\.\pipe\…) becomes a UNC path of the host "?", which
// the fixed rules deny with the shares of this machine (windowsShare).
func windowsKey(k string) string {
	for _, prefix := range []string{"//?/", "//./", "/??/"} {
		if rest, ok := strings.CutPrefix(k, prefix); ok {
			k = rest
			if u, ok := strings.CutPrefix(strings.ToLower(k), "unc/"); ok {
				k = "//" + k[len(k)-len(u):]
			} else if len(k) < 2 || k[1] != ':' || !isDriveLetter(k[0]) {
				return "//?/" + k
			}
			break
		}
	}
	if rest, ok := strings.CutPrefix(k, "//"); ok {
		host, after, _ := strings.Cut(rest, "/")
		share, tail, _ := strings.Cut(after, "/")
		if len(share) == 2 && share[1] == '$' && isDriveLetter(share[0]) && localHost(host) {
			k = share[:1] + ":/" + tail
		}
	}
	segs := strings.Split(k, "/")
	for i, s := range segs {
		if i == 0 && len(s) == 2 && s[1] == ':' {
			continue // the drive
		}
		if j := strings.IndexByte(s, ':'); j >= 0 {
			s = s[:j]
		}
		segs[i] = strings.TrimRight(s, ". ")
		if segs[i] == "" && s != "" {
			segs[i] = s // "." and ".." stay what they are
		}
	}
	return strings.Join(segs, "/")
}

// within reports whether key a is b or inside it.
func within(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	if strings.HasSuffix(b, "/") {
		return strings.HasPrefix(a, b)
	}
	return strings.HasPrefix(a, b+"/")
}

// rel is a key relative to the worktree ("" for the root), and whether it
// is inside it.
func (e *evaluator) rel(k string) (string, bool) {
	if !within(k, e.wt) {
		return "", false
	}
	return strings.TrimPrefix(strings.TrimPrefix(k, e.wt), "/"), true
}

// display is how a reason and an action show a path: relative to the
// worktree when inside it.
func (e *evaluator) display(k string) string {
	if r, ok := e.rel(k); ok {
		if r == "" {
			return "."
		}
		return r
	}
	return k
}
