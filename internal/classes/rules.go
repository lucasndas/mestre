package classes

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/providers"
)

// RulesVersion is the version of the effective-rules document. It changes
// only when a field changes meaning; new fields are added without changing it.
const RulesVersion = 2

// FixedRulesVersion is the version of the fixed security rules this binary
// enforces (fixed_rules_version of the effective rules).
const FixedRulesVersion = 1

// Verdict is the outcome of a decision: allow, ask (an approval request) or
// deny. The order is the restrictiveness: allow < ask < deny.
type Verdict string

// The verdicts.
const (
	Allow Verdict = "allow"
	Ask   Verdict = "ask"
	Deny  Verdict = "deny"
)

func (v Verdict) rank() int {
	switch v {
	case Allow:
		return 0
	case Ask:
		return 1
	}
	return 2 // deny, and anything unknown
}

func (v Verdict) valid() bool { return v == Allow || v == Ask || v == Deny }

// Layer is where a rule comes from (origin of the effective rules), cited by
// the reason of a denial.
type Layer string

// The layers. LayerLocked and LayerDegraded are not origins of a pattern:
// they name the reason of a decision.
const (
	LayerFixed     Layer = "fixed"
	LayerDecree    Layer = "decree"
	LayerException Layer = "exception"
	LayerCeiling   Layer = "ceiling"
	LayerClass     Layer = "class"
	LayerSheet     Layer = "sheet"
	LayerMachine   Layer = "machine"
	LayerLocked    Layer = "locked"
	LayerDegraded  Layer = "degraded"
)

func (l Layer) origin() bool {
	switch l {
	case LayerFixed, LayerDecree, LayerCeiling, LayerClass, LayerSheet, LayerMachine:
		return true
	}
	return false
}

// label is how the reason the agent sees names the layer.
func (l Layer) label(class string) string {
	switch l {
	case LayerFixed:
		return "regra fixa de segurança"
	case LayerDecree:
		return "decreto"
	case LayerException:
		return "exceção pontual"
	case LayerCeiling:
		return "teto do workspace"
	case LayerSheet:
		return "ficha do personagem"
	case LayerMachine:
		return "conformidade desta máquina"
	case LayerLocked:
		return "sistema travado"
	case LayerDegraded:
		return "motor indisponível"
	}
	if class != "" {
		return "classe " + class
	}
	return "classe"
}

// Workflows is git.workflows: how the engine sends branches that change
// .github/workflows/.
type Workflows string

// The values of git.workflows, from the most restrictive.
const (
	WorkflowsNever Workflows = "never"
	WorkflowsAsk   Workflows = "ask"
	WorkflowsAllow Workflows = "allow"
)

func (w Workflows) rank() int {
	switch w {
	case WorkflowsAllow:
		return 2
	case WorkflowsAsk:
		return 1
	}
	return 0
}

// Rules is the effective-rules document of one character
// (docs/classes/permissoes.md#regras-efetivas): the layers already combined,
// placeholders resolved. The machine keeps the last version and the
// policy.json snapshot of it.
type Rules struct {
	Version           int              `json:"version"`
	AgentID           string           `json:"agent_id"`
	Class             string           `json:"class"`
	GeneratedAt       time.Time        `json:"generated_at"`
	FixedRulesVersion int              `json:"fixed_rules_version"`
	Files             FileRules        `json:"files"`
	Commands          PatternRules     `json:"commands"`
	Git               GitRules         `json:"git"`
	Network           NetworkRules     `json:"network"`
	Tools             PatternRules     `json:"tools"`
	CLI               CLIRules         `json:"cli"`
	Operator          bool             `json:"operator"`
	Decrees           []Decree         `json:"decrees"`
	Origin            map[string]Layer `json:"origin"`
	Locked            bool             `json:"locked"`
	// ClassHooks are the extra hooks of the approved class, for the machine that has no
	// library of classes (a Tower): nil means they were not sent, and the machine must not
	// run the character as if its class had none.
	ClassHooks *ShippedHooks `json:"class_hooks,omitempty"`
}

// ShippedHooks are the hooks of a class as the Citadel sends them with the rules, with the
// hash of the approved package they come from.
type ShippedHooks struct {
	Hash  string `json:"hash"`
	Hooks Hooks  `json:"hooks,omitempty"`
}

// FileRules are globs relative to the worktree root; read and deny may also
// hold absolute globs (reading outside the worktree).
type FileRules struct {
	Read  []string `json:"read"`
	Write []string `json:"write"`
	Deny  []string `json:"deny"`
}

// PatternRules are the patterns of commands or tools, where * matches any
// text, and what an unmatched one gets.
type PatternRules struct {
	Allow   []string `json:"allow"`
	Ask     []string `json:"ask"`
	Deny    []string `json:"deny"`
	Default Verdict  `json:"default"`
}

// GitRules is the git block.
// Commit and RequestPush are allow (true in the class), deny (false) or
// ask (absent: a request, ADR 0025).
type GitRules struct {
	Commit      Verdict   `json:"commit"`
	Branches    []string  `json:"branches"`
	RequestPush Verdict   `json:"request_push"`
	Workflows   Workflows `json:"workflows"`
}

// NetworkRules is the network block. Internet with no domains is any domain.
type NetworkRules struct {
	Internet    bool     `json:"internet"`
	Domains     []string `json:"domains"`
	Credentials []string `json:"credentials"`
}

// CLIRules is the cli block (checked by the engine at the call).
type CLIRules struct {
	Allow []string `json:"allow"`
}

// Level is the level of the author of a decree; the higher one wins
// (docs/arquitetura/precedencia.md#a-ordem). The decisions of the agents of
// the approval chain (the Grand Master and the Mestre da mesa) are below
// every decree of a person and only turn an ask into allow: they never beat
// a deny.
type Level string

// The levels, from the strongest.
const (
	LevelOwner       Level = "owner"
	LevelAdmin       Level = "admin"
	LevelApprover    Level = "approver"
	LevelGrandMaster Level = "grand_master"
)

func (l Level) rank() int {
	switch l {
	case LevelOwner:
		return 3
	case LevelAdmin:
		return 2
	case LevelApprover:
		return 1
	case LevelGrandMaster, LevelTableMaster:
		return 0
	}
	return -1 // unknown: no decree of it counts
}

// AtLeast says whether l is a person at or above min: the check of "nivel_minimo"
// of the approval policy (docs/aprovacoes/politica.md#tipos-de-ação).
func (l Level) AtLeast(min Level) bool {
	return l.person() && min.rank() >= 0 && l.rank() >= min.rank()
}

// person reports a level of a person: owner, admin or approver.
func (l Level) person() bool { return l.rank() > 0 }

// agent reports a level of an agent of the approval chain: grand_master or
// table_master.
func (l Level) agent() bool { return l == LevelGrandMaster || l == LevelTableMaster }

// DecreeKind tells a permanent rule from a one-time exception.
type DecreeKind string

// The kinds of decree.
const (
	DecreeRule      DecreeKind = "rule"
	DecreeException DecreeKind = "exception"
	// DecreeTableRule is a temporary table rule of an agent of the approval
	// chain (docs/orquestracao/regras-temporarias-de-mesa.md): it only turns
	// an ask into allow, the engine checks it again at each use (Env.TableRules)
	// and it never reaches the degraded mode nor the native controls.
	DecreeTableRule DecreeKind = "table_rule"
)

// Decree is an active decree or exception that applies to the character
// (docs/telegram/decretos.md). Only permission effects reach the decision;
// the others act on other layers. An exception carries the exact action it
// frees, once.
type Decree struct {
	ID     int64      `json:"id"`
	Kind   DecreeKind `json:"kind,omitempty"` // "" is a rule
	Level  Level      `json:"level,omitempty"`
	Effect Effect     `json:"effect"`
	Action *Action    `json:"action,omitempty"`
	Until  *time.Time `json:"until,omitempty"`
	// Risk and ToolKind are of a table rule: the highest risk it covers
	// (baixo or medio) and the kind of tool its pattern is for. A table rule
	// binds either Action (the exact action) or one pattern in Effect.Allow
	// (commands for a shell, tools for the others).
	Risk     Risk               `json:"risk,omitempty"`
	ToolKind providers.ToolKind `json:"tool_kind,omitempty"`
}

// Effect is what a decree does; kind permission frees or forbids patterns.
type Effect struct {
	Kind  string `json:"kind"`
	Allow *Grant `json:"allow,omitempty"`
	Deny  *Grant `json:"deny,omitempty"`
}

// EffectPermission is the effect kind that reaches the decision.
const EffectPermission = "permission"

// Grant lists patterns with the vocabulary of permissions.yaml: files (read
// and write), commands, tools and domains.
type Grant struct {
	Files    []string `json:"files,omitempty"`
	Commands []string `json:"commands,omitempty"`
	Tools    []string `json:"tools,omitempty"`
	Domains  []string `json:"domains,omitempty"`
}

// active reports whether the decree counts at instant now. One with a
// validity and no known instant does not count.
func (d Decree) active(now time.Time) bool {
	if d.Until == nil {
		return true
	}
	return !now.IsZero() && now.Before(*d.Until)
}

// Action is a tool call as an exception binds it: the kind of tool and the
// command, the paths (relative to the worktree, slash form; absolute when
// outside it), the native name or the URL.
type Action struct {
	Kind    providers.ToolKind `json:"kind"`
	Tool    string             `json:"tool,omitempty"`
	Command string             `json:"command,omitempty"`
	Paths   []string           `json:"paths,omitempty"`
	URL     string             `json:"url,omitempty"`
}

// String is how a reason names the action.
func (a Action) String() string {
	switch a.Kind {
	case providers.KindShell:
		return a.Command
	case providers.KindRead, providers.KindWrite, providers.KindEdit, providers.KindSearch:
		return string(a.Kind) + " " + strings.Join(a.Paths, ", ")
	case providers.KindWeb:
		if a.URL != "" {
			return a.Tool + " " + a.URL
		}
	}
	return a.Tool
}

// same reports whether b is exactly the action a binds.
func (a Action) same(b Action) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case providers.KindShell:
		return a.Command == b.Command
	case providers.KindRead, providers.KindWrite, providers.KindEdit, providers.KindSearch:
		x, y := slices.Clone(a.Paths), slices.Clone(b.Paths)
		sort.Strings(x)
		sort.Strings(y)
		return len(x) > 0 && slices.Equal(x, y)
	case providers.KindWeb:
		return a.Tool == b.Tool && a.URL == b.URL
	}
	return a.Tool != "" && a.Tool == b.Tool
}

// AskReason is the denial the agent gets when the decision is to ask: the
// same in the three providers (docs/classes/permissoes.md#quando-a-decisão-é-pedir-aprovação).
func AskReason(request int64, a Action) string {
	return fmt.Sprintf("pedido de aprovação #%d aberto para %s; encerre o turno, você será avisado", request, a)
}

// DegradedReason is the denial of what would ask while the engine is down.
const DegradedReason = "motor indisponível, tente depois"

var agentIDRe = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z_-]{0,63}$`)

// ValidAgentID reports whether id can name a character: it becomes a folder
// name of the snapshot, so nothing but letters, digits, _ and -.
func ValidAgentID(id string) bool { return agentIDRe.MatchString(id) }

// ParseRules reads an effective-rules document and validates it. Unknown
// fields are accepted (they are added without a new version).
func ParseRules(data []byte) (*Rules, error) {
	var r Rules
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("effective rules: %w", err)
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return &r, nil
}

// Validate checks what the decision relies on.
func (r *Rules) Validate() error {
	switch {
	case r.Version != RulesVersion:
		return fmt.Errorf("effective rules: version %d, this binary reads %d", r.Version, RulesVersion)
	case !ValidAgentID(r.AgentID):
		return fmt.Errorf("effective rules: invalid agent_id %q", r.AgentID)
	case r.GeneratedAt.IsZero():
		return fmt.Errorf("effective rules: generated_at is missing")
	case !r.Commands.Default.valid():
		return fmt.Errorf("effective rules: commands.default %q", r.Commands.Default)
	case !r.Tools.Default.valid():
		return fmt.Errorf("effective rules: tools.default %q", r.Tools.Default)
	case !r.Git.Commit.valid():
		return fmt.Errorf("effective rules: git.commit %q", r.Git.Commit)
	case !r.Git.RequestPush.valid():
		return fmt.Errorf("effective rules: git.request_push %q", r.Git.RequestPush)
	}
	switch r.Git.Workflows {
	case WorkflowsNever, WorkflowsAsk, WorkflowsAllow:
	default:
		return fmt.Errorf("effective rules: git.workflows %q", r.Git.Workflows)
	}
	for k, l := range r.Origin {
		if !l.origin() {
			return fmt.Errorf("effective rules: origin %q: layer %q", k, l)
		}
	}
	lists := map[string][]string{
		"files.read": r.Files.Read, "files.write": r.Files.Write, "files.deny": r.Files.Deny,
		"commands.allow": r.Commands.Allow, "commands.ask": r.Commands.Ask, "commands.deny": r.Commands.Deny,
		"tools.allow": r.Tools.Allow, "tools.ask": r.Tools.Ask, "tools.deny": r.Tools.Deny,
		"git.branches": r.Git.Branches, "network.domains": r.Network.Domains,
	}
	for name, l := range lists {
		for _, p := range l {
			if strings.TrimSpace(p) == "" || strings.ContainsAny(p, "\x00\n") {
				return fmt.Errorf("effective rules: %s: invalid pattern %q", name, p)
			}
			if strings.Contains(p, "{") && name == "git.branches" {
				return fmt.Errorf("effective rules: %s: unresolved placeholder in %q", name, p)
			}
		}
	}
	for _, d := range r.Decrees {
		if d.Kind != "" && d.Kind != DecreeRule && d.Kind != DecreeException && d.Kind != DecreeTableRule {
			return fmt.Errorf("effective rules: decree %d: kind %q", d.ID, d.Kind)
		}
		if d.Kind == DecreeTableRule {
			if !d.Level.agent() || d.Until == nil || (d.Risk != RiskLow && d.Risk != RiskMedium) {
				return fmt.Errorf("effective rules: decree %d: a table rule needs the level of an agent, a validity and the risk baixo or medio", d.ID)
			}
		}
		if !d.Level.person() && !d.Level.agent() {
			return fmt.Errorf("effective rules: decree %d: level %q", d.ID, d.Level)
		}
	}
	return nil
}

// originOf is the layer of a pattern of a list ("commands.deny"); a pattern
// with no entry comes from the class.
func (r *Rules) originOf(list, pattern string) Layer {
	if l, ok := r.Origin[list+":"+pattern]; ok {
		return l
	}
	return LayerClass
}

// originField is the layer of a scalar field ("git.commit").
func (r *Rules) originField(field string) Layer {
	if l, ok := r.Origin[field]; ok {
		return l
	}
	return LayerClass
}
