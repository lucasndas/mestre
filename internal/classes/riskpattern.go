package classes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lucasndas/mestre/internal/providers"
)

// The refusals of an approvable pattern.
var (
	// ErrPatternTooBroad is * alone, a pattern that does not start with a
	// literal program, or an interpreter with a wildcard.
	ErrPatternTooBroad = errors.New("pattern_too_broad")
	// ErrPatternNoMatch is a pattern that does not match the action of the
	// request it comes from.
	ErrPatternNoMatch = errors.New("pattern_no_match")
)

// ApprovablePattern is a pattern accepted as the target of an approval
// that approves a pattern, or of a rule suggestion
// (docs/classes/classificacao-de-risco.md#padrões-aprováveis).
type ApprovablePattern struct {
	Pattern string             `json:"pattern"`
	Kind    providers.ToolKind `json:"kind"`
	Class   string             `json:"class"`
	Table   string             `json:"table"`
	// Risk is the highest a command (or tool) the pattern matches can have.
	Risk RiskResult `json:"risk"`
	// Fingerprint is the SHA-256 of the canonical JSON of the pattern, the
	// tool kind, the class and the table: a target of its own.
	Fingerprint string `json:"fingerprint"`
}

// CheckApprovablePattern checks a pattern against the action of the
// request it comes from, for the class and the table of who asked: it must
// match the action and start with a literal program (a literal name, for a
// tool), never an interpreter with a wildcard nor * alone
// (ErrPatternTooBroad). Its risk is the highest a command it matches can
// have by the default table: npm install * is baixo, npm * reaches npm
// publish and is alto.
func CheckApprovablePattern(pattern string, action Action, class, table string) (ApprovablePattern, error) {
	out := ApprovablePattern{Pattern: pattern, Kind: action.Kind, Class: class, Table: table}
	var target string
	switch action.Kind {
	case providers.KindShell:
		target = action.Command
		if why := tooBroad(pattern); why != "" {
			return out, fmt.Errorf("%w: %s", ErrPatternTooBroad, why)
		}
	case providers.KindMCP, providers.KindOther, providers.KindSubagent:
		target = action.Tool
		if strings.HasPrefix(pattern, "*") || strings.TrimSpace(pattern) == "" {
			return out, fmt.Errorf("%w: does not start with a literal name", ErrPatternTooBroad)
		}
	default:
		return out, fmt.Errorf("%w: a %s action is approved exactly, not by a pattern", ErrPatternNoMatch, action.Kind)
	}
	if !matchText(pattern, target) {
		return out, fmt.Errorf("%w: %q does not match %q", ErrPatternNoMatch, pattern, target)
	}
	var r riskOut
	if action.Kind == providers.KindShell {
		r = commandReach(pattern)
	} else {
		r = toolReach(pattern)
	}
	out.Risk = RiskResult{Risk: r.risk, TableVersion: RiskTableVersion, Rule: r.rule}
	out.Fingerprint = fingerprint(struct {
		Class   string             `json:"class"`
		Kind    providers.ToolKind `json:"kind"`
		Pattern string             `json:"pattern"`
		Table   string             `json:"table"`
	}{class, action.Kind, pattern, table})
	return out, nil
}

// ClassPermissionFingerprint is the fingerprint of the target of a
// permissao_de_classe request (docs/aprovacoes/fluxo.md#vínculo-ao-estado-exato):
// the character, the exact action and the folder it runs in.
func ClassPermissionFingerprint(agentID string, a Action, dir string) string {
	return fingerprint(struct {
		Action Action `json:"action"`
		Agent  string `json:"agent"`
		Dir    string `json:"dir"`
	}{a, agentID, dir})
}

// fingerprint is the SHA-256, in hex, of the canonical JSON of a target:
// the fields in a fixed order, no spaces.
func fingerprint(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("fingerprint of a plain struct: %v", err))
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// PatternCovers reports whether an approved pattern, or a temporary table
// rule made of one, lets an action through
// (docs/classes/classificacao-de-risco.md#padrões-aprováveis): the action is
// of the pattern's kind, the pattern matches it, and the action's own
// classification (own: ClassifyRisk, or the Decision.Risk of the ask) fits
// the risk the pattern was approved at. npm install * approved as baixo
// never lets npm install -g x through: that command is medio.
func PatternCovers(p ApprovablePattern, a Action, own RiskResult) bool {
	if a.Kind != p.Kind || !own.Risk.valid() || own.Risk == RiskFixed || own.Risk.rank() > p.Risk.Risk.rank() {
		return false
	}
	target := a.Tool
	if a.Kind == providers.KindShell {
		target = a.Command
	}
	return matchText(p.Pattern, target)
}
