//go:build full

package tablerules_test

import (
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/providers"
)

// Part of CA-RTM-09: the gate that confers the rule again at each use holds the rule of
// the exact action to the same conditions as the one of a pattern, for the
// Mestre da mesa and for the Grão-Mestre: a call with a part the engine does
// not analyse, or that chains commands, is not let through, and nothing is
// recorded as used.
func TestTheGateDoesNotLetAnOpaquePartNorAChainedCommandThroughTheRuleOfTheExactAction(t *testing.T) {
	t.Parallel()
	medio := func(groups ...string) classes.RiskResult {
		return classes.RiskResult{Risk: classes.RiskMedium, Groups: groups}
	}
	for _, level := range []chain.LevelKind{chain.LevelMesa, chain.LevelGrandMaster} {
		r := newRig(t)
		plain := r.makeExact(level, "make build", classes.RiskMedium)
		if !r.useAs(plain, "make build", medio(classes.GroupChangedScript)) {
			t.Fatalf("%s: the gate does not let the exact action through", level)
		}
		for name, c := range map[string]struct {
			rule    string // the command the rule was made for; the call, when empty
			command string
			own     classes.RiskResult
		}{
			"a part with no group":               {"", "alias ll=ls", medio(classes.GroupNone)},
			"a part with no group, among others": {"", "make build", medio(classes.GroupChangedScript, classes.GroupNone)},
			"a chain":                            {"", "make build && ./run.sh", medio(classes.GroupChangedScript)},
			"a list":                             {"", "make build; bash run.sh", medio(classes.GroupChangedScript)},
			"a pipe":                             {"", "make build | tee out", medio(classes.GroupChangedScript)},
			"a substitution":                     {"", "make $(cat target)", medio(classes.GroupChangedScript)},
			"a redirection":                      {"", "make build > out", medio(classes.GroupChangedScript)},
			"a line break":                       {"", "make build\n./run.sh", medio(classes.GroupChangedScript)},
			"a line break the action trims away": {"make build", "make build\n\U00003000", medio(classes.GroupChangedScript)},
			"a vertical tab and a line break":    {"make build", "\v\nmake build", medio(classes.GroupChangedScript)},
			"a no-break space at the end":        {"make build", "make build\U000000a0", medio(classes.GroupChangedScript)},
			"a form feed at the start":           {"make build", "\fmake build", medio(classes.GroupChangedScript)},
		} {
			rule := c.rule
			if rule == "" {
				rule = c.command
			}
			id := r.makeExact(level, rule, classes.RiskMedium)
			if r.useAs(id, c.command, c.own) {
				t.Errorf("%s, %s: the gate let %q through the rule of its exact action", level, name, c.command)
			}
			if uses, err := r.svc.Uses(r.ctx, id); err != nil || len(uses) != 0 {
				t.Errorf("%s, %s: uses = %+v, %v; want none", level, name, uses, err)
			}
		}
		if uses, _ := r.svc.Uses(r.ctx, plain); len(uses) != 1 {
			t.Errorf("%s: uses of the plain rule = %d, want 1", level, len(uses))
		}
	}
}

// makeExact creates the rule of the exact action of a command, made by the
// Mestre da mesa or by the Grão-Mestre, and returns its id.
func (r *rig) makeExact(level chain.LevelKind, command string, risk classes.Risk) int64 {
	r.t.Helper()
	return r.makeExactAction(level, classes.Action{Kind: providers.KindShell, Command: command}, risk)
}

// useAs asks the gate to let a command through under a rule, with the
// classification the engine gave the call.
func (r *rig) useAs(id int64, command string, own classes.RiskResult) bool {
	r.t.Helper()
	// the action keeps the command trimmed, as the engine builds it; the tool keeps it as it came
	ok, err := r.svc.Use(r.ctx, classes.TableRuleUse{Rule: id, Level: classes.LevelTableMaster, Risk: own.Risk, AgentID: r.dev.ID, TableID: "42-login",
		Action: classes.Action{Kind: providers.KindShell, Command: strings.TrimSpace(command)}, Tool: &providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: command}, Own: own})
	if err != nil {
		r.t.Fatal(err)
	}
	return ok
}
