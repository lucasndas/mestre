package classes

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/providers"
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

// TurnEdits answers who else edited a file in a turn still in progress.
type TurnEdits interface {
	OtherEditor(tableID, agentID, path string) (who string, ok bool)
}

// ExceptionLedger records that an exception was used: Use is true only the
// first time for an id, atomically.
type ExceptionLedger interface {
	Use(ctx context.Context, agentID string, id int64) (bool, error)
}

// Decision is the outcome of the pipeline for one tool call.
type Decision struct {
	Verdict Verdict
	// Reason is what the agent sees with a denial; for ask, what the
	// approval request is about (AskReason builds the denial).
	Reason string
	// Layer decided it.
	Layer Layer
	// Decree is the decree or exception that decided, if one did.
	Decree int64
	// ClassHooks is true when an allow still goes through the synchronous
	// pre_tool hooks of the class, which can only deny
	// (docs/classes/classes.md#hooks-extras-da-classe).
	ClassHooks bool
	// Action is the call as an exception would bind it.
	Action Action
	// Risk is the risk of the approval request an ask opens
	// (docs/classes/classificacao-de-risco.md); zero for the other verdicts
	// and in the degraded mode.
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
	env.Exceptions = nil
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
	if ev.Event != providers.EventPreTool && ev.Event != providers.EventPermissionRequest {
		return Decision{Verdict: Allow}
	}
	if ev.Tool == nil {
		return Decision{Verdict: Deny, Reason: "evento de ferramenta sem a ferramenta", Layer: LayerFixed}
	}
	e := newEvaluator(ctx, env, rules, ev, degraded)
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
		return deny("sistema travado: toda ferramenta está negada até o Dono destravar", LayerLocked)
	}
	res := e.analyze(ev.Tool)
	if res.fixed.v == Deny {
		return Decision{Verdict: Deny, Reason: res.fixed.reason, Layer: LayerFixed, Action: act}
	}
	if d, ok := e.exception(act, res); ok {
		return d
	}
	o := res.rules
	d := Decision{Verdict: o.v, Reason: o.reason, Layer: o.layer, Decree: o.decree, Action: act}
	switch o.v {
	case Allow:
		d.ClassHooks = true
	case Ask:
		d.Reason = act.String()
		if o.reason != "" {
			d.Reason += " (" + o.reason + ")"
		}
		if !e.degraded {
			d.Risk = e.classify(ev.Tool, false)
		}
	}
	return d
}

// exception looks for an exception bound to exactly this action. It does
// not count when a deny decree of a higher level takes a part of it, nor
// when it is suspended by a deny decree of the same level.
func (e *evaluator) exception(act Action, res result) (Decision, bool) {
	if e.env.Exceptions == nil {
		return Decision{}, false
	}
	for _, d := range e.r.Decrees {
		if d.Kind != DecreeException || d.Action == nil || !d.active(e.now) || !d.Action.same(act) {
			continue
		}
		if res.denyRank >= d.Level.rank() {
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
	k = path.Clean(k)
	if e.fold {
		k = strings.ToLower(k)
	}
	return k
}

// windowsKey undoes the spellings Windows takes for the same file: the
// \\?\ and \\.\ prefixes, the trailing dots and spaces it drops from a
// name, and an alternate data stream (.git::$INDEX_ALLOCATION is .git).
func windowsKey(k string) string {
	for _, prefix := range []string{"//?/", "//./", "/??/"} {
		if rest, ok := strings.CutPrefix(k, prefix); ok {
			k = rest
			if u, ok := strings.CutPrefix(strings.ToLower(k), "unc/"); ok {
				k = "//" + k[len(k)-len(u):]
			}
			break
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
