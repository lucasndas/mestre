package classes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/lucasndas/mestre/internal/providers"
)

// RiskAdjustments are the owner's adjustments of the risco section of the
// approval policy file (docs/classes/classificacao-de-risco.md#ajustes-do-dono):
// they hold before the default table, in the order of the file.
type RiskAdjustments struct {
	Items []RiskAdjustment
	// MinLevel is nivel_minimo: the minimum level of the people for each
	// risk; it only goes up from DefaultMinLevel.
	MinLevel map[Risk]Level
}

// RiskAdjustment is one adjustment: a command or a tool pattern and the
// risk it gets.
type RiskAdjustment struct {
	// Index is its number in the file, from 1.
	Index int
	// Command matches like the patterns of commands; Tool like those of
	// tools. Exactly one is set.
	Command, Tool string
	Risk          Risk
}

func (a RiskAdjustment) rule() string {
	if a.Command != "" {
		return fmt.Sprintf("ajuste %d do Dono: comando %q → %s", a.Index, a.Command, a.Risk)
	}
	return fmt.Sprintf("ajuste %d do Dono: ferramenta %q → %s", a.Index, a.Tool, a.Risk)
}

// DefaultMinLevel is nivel_minimo of the default content: alto needs an
// Administrator or above.
var DefaultMinLevel = map[Risk]Level{RiskHigh: LevelAdmin}

// MinLevelFor is the minimum level of the people for a risk; nil
// adjustments are the default content.
func (a *RiskAdjustments) MinLevelFor(r Risk) Level {
	if a != nil {
		if l, ok := a.MinLevel[r]; ok {
			return l
		}
	}
	if l, ok := DefaultMinLevel[r]; ok {
		return l
	}
	return LevelApprover
}

// ErrInvalidRisk wraps every error of an invalid risco section: the whole
// set is refused, and with it the file, whose last valid version holds.
var ErrInvalidRisk = errors.New("invalid risco section")

// RiskAdjustmentError is an invalid item of the risco section, with the
// reason.
type RiskAdjustmentError struct {
	// Item names it: "ajustes[2]" (from 1) or "nivel_minimo.alto".
	Item   string
	Reason string
}

func (e *RiskAdjustmentError) Error() string {
	return fmt.Sprintf("%v: %s: %s", ErrInvalidRisk, e.Item, e.Reason)
}

func (e *RiskAdjustmentError) Unwrap() error { return ErrInvalidRisk }

// peopleLevels are the values of nivel_minimo.
var peopleLevels = map[string]Level{"aprovador": LevelApprover, "administrador": LevelAdmin, "dono": LevelOwner}

type riskSectionYAML struct {
	Ajustes []struct {
		Comando    *string `yaml:"comando"`
		Ferramenta *string `yaml:"ferramenta"`
		Risco      string  `yaml:"risco"`
	} `yaml:"ajustes"`
	NivelMinimo map[string]string `yaml:"nivel_minimo"`
}

// ParseRiskAdjustments reads and checks the risco section of the approval
// policy file (the YAML of the section's value). Valid: raising any
// pattern, and lowering only from medio to baixo. Invalid, and the whole
// set is refused with the item and the reason: risco fixo or a pattern
// that matches an action of a fixed rule; lowering what the default table
// gives as alto, or a pattern that can match an alto command; * alone or
// an interpreter with a wildcard; any other key (none changes how far each
// level decides); nivel_minimo below the default.
func ParseRiskAdjustments(data []byte) (*RiskAdjustments, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var s riskSectionYAML
	if err := dec.Decode(&s); err != nil && !errors.Is(err, io.EOF) {
		return nil, &RiskAdjustmentError{Item: "risco", Reason: err.Error()}
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, &RiskAdjustmentError{Item: "risco", Reason: "more than one YAML document"}
	}
	out := &RiskAdjustments{MinLevel: map[Risk]Level{}}
	for i, a := range s.Ajustes {
		item := fmt.Sprintf("ajustes[%d]", i+1)
		adj := RiskAdjustment{Index: i + 1, Risk: Risk(a.Risco)}
		switch {
		case (a.Comando == nil) == (a.Ferramenta == nil):
			return nil, &RiskAdjustmentError{Item: item, Reason: "needs exactly one of comando and ferramenta"}
		case a.Comando != nil:
			adj.Command = *a.Comando
		default:
			adj.Tool = *a.Ferramenta
		}
		if err := checkAdjustment(adj); err != "" {
			return nil, &RiskAdjustmentError{Item: item + " (" + adj.target() + ")", Reason: err}
		}
		out.Items = append(out.Items, adj)
	}
	for _, k := range slices.Sorted(maps.Keys(s.NivelMinimo)) {
		v := s.NivelMinimo[k]
		item := "nivel_minimo." + k
		r := Risk(k)
		l, ok := peopleLevels[v]
		switch {
		case r == RiskFixed:
			return nil, &RiskAdjustmentError{Item: item, Reason: "fixo keeps the rule of its request type"}
		case !r.valid():
			return nil, &RiskAdjustmentError{Item: item, Reason: "not a risk (baixo, medio, alto)"}
		case !ok:
			return nil, &RiskAdjustmentError{Item: item, Reason: fmt.Sprintf("%q is not aprovador, administrador or dono", v)}
		case l.rank() < out.MinLevelFor(r).rank():
			return nil, &RiskAdjustmentError{Item: item, Reason: fmt.Sprintf("%s is below the default (%s): the minimum level only goes up", v, levelName(DefaultMinLevel[r]))}
		}
		out.MinLevel[r] = l
	}
	return out, nil
}

// LoadRiskAdjustments is the policy file loader's step for the risco
// section: the adjustments that hold after reading data, the new ones when
// they are valid; otherwise the error and last, the last valid version
// (nil: the default table).
func LoadRiskAdjustments(last *RiskAdjustments, data []byte) (*RiskAdjustments, error) {
	a, err := ParseRiskAdjustments(data)
	if err != nil {
		return last, err
	}
	return a, nil
}

func levelName(l Level) string {
	for k, v := range peopleLevels {
		if v == l {
			return k
		}
	}
	return string(l)
}

func (a RiskAdjustment) target() string {
	if a.Command != "" {
		return fmt.Sprintf("comando %q", a.Command)
	}
	return fmt.Sprintf("ferramenta %q", a.Tool)
}

// checkAdjustment is the reason an adjustment is invalid, or "".
func checkAdjustment(a RiskAdjustment) string {
	p := a.Command + a.Tool
	switch {
	case strings.TrimSpace(p) == "" || strings.ContainsAny(p, "\x00\n\r"):
		return "empty pattern or with a line break"
	case a.Risk == RiskFixed:
		return "risco fixo: fixo never changes"
	case !a.Risk.valid():
		return fmt.Sprintf("risco %q is not baixo, medio or alto", a.Risk)
	}
	var base riskOut
	if a.Command != "" {
		if why := tooBroad(p); why != "" {
			return why
		}
		if why := reachesFixed(p); why != "" {
			return "matches an action of a fixed rule (" + why + "): fixo never changes"
		}
		base = commandReach(p)
	} else {
		if p == "*" {
			return "* alone"
		}
		if reachesComputerControl(p) {
			return "matches a tool of a fixed rule (computer and browser control): fixo never changes"
		}
		base = toolReach(p)
	}
	switch {
	case a.Risk.rank() >= base.risk.rank():
		return "" // raising, or the same
	case base.risk == RiskHigh:
		return fmt.Sprintf("lowers to %s what can be alto (%s): alto only opens by the owner's delegation", a.Risk, base.rule)
	case base.risk == RiskMedium && a.Risk == RiskLow:
		return ""
	}
	return fmt.Sprintf("lowers %s to %s: only medio goes down, to baixo", base.risk, a.Risk)
}

// broadInterpreters run any code given to them: a pattern with a wildcard
// in their place of the code is never approvable nor an adjustment.
var broadInterpreters = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "pwsh": true, "powershell": true, "cmd": true, "python": true, "python3": true,
	"node": true, "deno": true, "bun": true, "ruby": true, "perl": true, "php": true, "env": true, "xargs": true, "npx": true,
}

// anyCode are the broad interpreters whose every argument can be code.
var anyCode = map[string]bool{"env": true, "xargs": true, "cmd": true, "pwsh": true, "powershell": true}

// tooBroad is why a command pattern is too broad (pattern_too_broad), or
// "": * alone, a pattern that does not start with a literal program, or an
// interpreter with a wildcard where its code or script goes (python *,
// python -c *, node*), not after a literal script or subcommand
// (node scripts/build.js *, bun install *).
func tooBroad(p string) string {
	f := strings.Fields(p)
	if len(f) == 0 || strings.Contains(f[0], "*") {
		return "does not start with a literal program (* alone, or a wildcard in the program)"
	}
	if !strings.Contains(p, "*") || !broadInterpreters[canonical(f[0])] {
		return ""
	}
	if anyCode[canonical(f[0])] || len(f) < 2 || strings.Contains(f[1], "*") || strings.HasPrefix(f[1], "-") || strings.HasPrefix(f[1], "/") {
		return fmt.Sprintf("an interpreter with a wildcard (%s)", f[0])
	}
	return ""
}

// riskEnv is the environment the reach of a pattern is classified in: a
// worktree of its own, POSIX, no home.
func riskEnv() *evaluator {
	return newEvaluator(context.Background(), Env{Worktree: "/w", OS: "linux"}, &Rules{}, providers.Event{}, false)
}

// fixedRoots are the commands the fixed rules deny, whole or in part: a
// pattern that reaches one of them matches an action of a fixed rule.
var fixedRoots = []string{
	"git push", "git remote", "git update-ref", "git tag", "git worktree", "git gc", "git prune", "git fetch", "git pull",
	"git stash", "git config", "git branch", "git clean", "claude", "codex", "agy",
	"sudo", "su", "doas", "pkexec", "runas", "gsudo", "start-process",
}

// reachesFixed is the fixed rule a command pattern reaches, or "".
func reachesFixed(p string) string {
	e := riskEnv()
	lit := literal(p)
	if res := e.shell(lit, "/w", 0); res.fixed.v == Deny {
		return res.fixed.reason
	}
	if !strings.Contains(p, "*") {
		return ""
	}
	for _, r := range fixedRoots {
		if extends(p, r) || strings.HasPrefix(lit+" ", r+" ") {
			return r
		}
	}
	return ""
}

func reachesComputerControl(p string) bool {
	for _, c := range DefaultComputerControl {
		if mayMatchText(p, c) {
			return true
		}
	}
	return false
}

// literal is a pattern up to its first wildcard, without the trailing
// space.
func literal(p string) string {
	if i := strings.IndexByte(p, '*'); i >= 0 {
		p = p[:i]
	}
	return strings.TrimRight(p, " ")
}

// extends reports whether row (a row pattern of the table, or a fixed
// root) names commands the wildcard of p can reach by its program and
// subcommand words: the literal words of row continue those of p where p's
// wildcard is (in the same word, when the wildcard is inside it), and the
// first word row adds is not an option. The options and the content of the
// arguments (paths, URLs, SQL, --global) are not the reach of a pattern;
// the classification of each command still sees them.
func extends(p, row string) bool {
	i := strings.IndexByte(p, '*')
	if i < 0 {
		return false
	}
	mid := i > 0 && p[i-1] != ' '
	pw, rw := strings.Fields(literal(p)), strings.Fields(literal(row))
	if len(pw) == 0 || len(rw) < len(pw) {
		return false
	}
	last := len(pw) - 1
	for j := 0; j < last; j++ {
		if pw[j] != rw[j] {
			return false
		}
	}
	if pw[last] != rw[last] && (!mid || !strings.HasPrefix(rw[last], pw[last])) {
		return false
	}
	rest := rw[len(pw):]
	return len(rest) == 0 || !strings.HasPrefix(rest[0], "-")
}

// commandReach is the highest risk a command matched by the pattern can
// have by its program and subcommands, under the default table: the
// classification of its literal part (with the signals of its program), the
// row that covers the whole pattern, the rows and the signal subcommands it
// reaches (npm * reaches npm publish, git * reaches git rm) and the
// conditions of its program (rm * reaches rm with a path outside the
// worktree). The content of the arguments is not the reach: each command
// still has its own classification, and PatternCovers asks it to fit.
func commandReach(p string) riskOut {
	e := riskEnv()
	w := e.riskWalker()
	out := w.line(literal(p), "/w", dialectPOSIX, 0)
	if !strings.Contains(p, "*") {
		return out
	}
	covered := false
	prog := canonical(strings.Fields(p)[0])
	for _, r := range riskTable {
		switch {
		case r.pattern == "":
			if r.program == prog && r.program != "" {
				out.raise(riskOut{risk: r.risk, rule: r.rule()})
			}
		case strings.HasPrefix(r.pattern, "*"):
			// a row on the content of the arguments: not the reach
		case coversText(r.pattern, p) || (r.risk != RiskLow && coversText(strings.ToLower(r.pattern), strings.ToLower(p))):
			if !covered {
				covered = true
				out.raise(riskOut{risk: r.risk, rule: r.rule()})
			}
		case extends(p, r.pattern):
			out.raise(riskOut{risk: r.risk, rule: r.rule()})
		}
	}
	for _, r := range signalPatterns {
		if coversText(r.pattern, p) || extends(p, r.pattern) {
			out.raise(riskOut{risk: r.risk, rule: "sinal de " + r.group + ": " + r.pattern})
		}
	}
	return out
}

// toolReach is commandReach for a tool pattern: with a wildcard, any name,
// so alto (it can name a GitHub or a destructive tool).
func toolReach(p string) riskOut {
	if strings.Contains(p, "*") {
		if mayMatchText(p, "mcp__github__*") {
			return riskOut{risk: RiskHigh, rule: fmt.Sprintf("%q pode casar mcp__github__*", p)}
		}
		return riskOut{risk: RiskHigh, rule: fmt.Sprintf("%q pode casar uma ferramenta com %s", p, strings.Join(destructiveWords, ", "))}
	}
	return defaultToolRisk(p, "")
}

// mayMatchText reports whether two text patterns can match the same text:
// their literal prefixes agree (conservative).
func mayMatchText(a, b string) bool {
	if !strings.Contains(a, "*") && !strings.Contains(b, "*") {
		return a == b
	}
	if !strings.Contains(a, "*") {
		return matchText(b, a)
	}
	if !strings.Contains(b, "*") {
		return matchText(a, b)
	}
	x, y := strings.SplitN(a, "*", 2)[0], strings.SplitN(b, "*", 2)[0]
	return strings.HasPrefix(x, y) || strings.HasPrefix(y, x)
}
