package tablerules_test

import (
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/providers"
)

// makeExactAction is makeExact for any action.
func (r *rig) makeExactAction(level chain.LevelKind, act classes.Action, risk classes.Risk) int64 {
	r.t.Helper()
	author := r.master
	if level == chain.LevelGrandMaster {
		author = r.gm
	}
	err := r.svc.Create(r.ctx, chain.TempRule{Request: 8, Workspace: "meu-app", Table: "42-login", Class: "Dev", Requester: r.dev.ID,
		Action: act, Risk: risk, Until: r.clk.Now().Add(time.Hour), By: author.ID,
		Level: chain.Level{Kind: level, Agent: author.ID}})
	if err != nil {
		r.t.Fatal(err)
	}
	rules, err := r.svc.List(r.ctx, "meu-app", "42-login")
	if err != nil || len(rules) == 0 {
		r.t.Fatalf("rules = %+v, %v", rules, err)
	}
	return rules[len(rules)-1].ID
}

// CA-RTM-09: nor does the gate let a call of a tool the table has no row for
// through the rule of its exact action, whoever made it: the rule compares only
// the name of the tool, so it would free every call of it, whatever its
// arguments.
func TestTheGateDoesNotLetAToolTheTableHasNoRowForThroughTheRuleOfItsExactAction(t *testing.T) {
	t.Parallel()
	for _, level := range []chain.LevelKind{chain.LevelMesa, chain.LevelGrandMaster} {
		r := newRig(t)
		mcp := classes.Action{Kind: providers.KindMCP, Tool: "mcp__db__query"}
		id := r.makeExactAction(level, mcp, classes.RiskMedium)
		ok, err := r.svc.Use(r.ctx, classes.TableRuleUse{Rule: id, Level: classes.LevelTableMaster, Risk: classes.RiskMedium, AgentID: r.dev.ID, TableID: "42-login",
			Action: mcp, Tool: &providers.Tool{NativeName: mcp.Tool, Kind: providers.KindMCP}, Own: classes.RiskResult{Risk: classes.RiskMedium, Groups: []string{classes.GroupNone}}})
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Errorf("%s: the gate let a call of %s through the rule of its exact action", level, mcp.Tool)
		}
		if uses, err := r.svc.Uses(r.ctx, id); err != nil || len(uses) != 0 {
			t.Errorf("%s: uses = %+v, %v; want none", level, uses, err)
		}
	}
}
