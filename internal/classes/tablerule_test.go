package classes_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/providers"
)

// gateFake is a TableRuleGate that answers what it was told and records the
// uses it was asked.
type gateFake struct {
	allow bool
	err   error
	asked []classes.TableRuleUse
}

func (g *gateFake) Use(_ context.Context, u classes.TableRuleUse) (bool, error) {
	g.asked = append(g.asked, u)
	return g.allow, g.err
}

func tableRule(id int64, pattern string, risk classes.Risk) classes.Decree {
	until := now.Add(time.Hour)
	return classes.Decree{ID: id, Kind: classes.DecreeTableRule, Level: classes.LevelTableMaster, Until: &until, Risk: risk, ToolKind: providers.KindShell,
		Effect: classes.Effect{Kind: classes.EffectPermission, Allow: &classes.Grant{Commands: []string{pattern}}}}
}

const tableRuleYAML = "version: 1\ncommands:\n  deny: [\"npm install evil\"]\n  ask: [\"npm install *\", \"curl *\"]\n  default: ask\n"

// A table rule turns an ask into allow and nothing else: a fixed rule, a deny
// of the class, a call it does not cover, a gate that says no or fails, and
// the degraded mode all leave the call as it was.
func TestATableRuleOnlyTurnsAnAskIntoAllowAndTheEngineChecksItAtEachUse(t *testing.T) {
	t.Parallel()
	m := newMachine()
	r := rules(t, tableRuleYAML, tableRule(5, "npm install *", classes.RiskLow), tableRule(6, "git push *", classes.RiskMedium))
	gate := &gateFake{allow: true}
	env := m.env()
	env.TableRules = gate

	d := decide(env, r, m.shell("npm install y"))
	want(t, d, classes.Allow, classes.LayerDecree, "regra temporária de mesa 5")
	if d.Decree != 5 || !d.ClassHooks || len(gate.asked) != 1 || gate.asked[0].Rule != 5 || gate.asked[0].AgentID != agentID || gate.asked[0].Action.Command != "npm install y" {
		t.Errorf("decision %+v, gate asked %+v", d, gate.asked)
	}
	want(t, decide(env, r, m.shell("npm install evil")), classes.Deny, classes.LayerClass)
	want(t, decide(env, r, m.shell("git push origin x")), classes.Deny, classes.LayerFixed)
	want(t, decide(env, r, m.shell("npm install -g y")), classes.Ask, "")
	want(t, decide(env, r, m.shell("npm install y && npm install z")), classes.Ask, "")
	want(t, decide(env, r, m.shell("npm install $(cat x)")), classes.Ask, "")
	want(t, decide(env, r, m.shell("npm install y > out.txt")), classes.Ask, "")
	if len(gate.asked) != 1 {
		t.Errorf("the gate was asked %d times, want only for the call the rule covers", len(gate.asked))
	}

	gate.allow = false
	want(t, decide(env, r, m.shell("npm install y")), classes.Ask, "")
	gate.allow, gate.err = true, errors.New("database is down")
	want(t, decide(env, r, m.shell("npm install y")), classes.Ask, "")

	// no gate: the rules in the document do nothing (degraded, the attenuation, the classification)
	env.TableRules = nil
	want(t, decide(env, r, m.shell("npm install y")), classes.Ask, "")
	gate.err = nil
	env.TableRules = gate
	want(t, classes.DecideDegraded(context.Background(), env, r, m.shell("npm install y")), classes.Deny, classes.LayerDegraded)
	if ok, _ := classes.Attenuates(context.Background(), env, r, m.shell("npm install y")); !ok {
		t.Error("the attenuation counts an ask as allowed")
	}
}

// A table rule has a validity and the risk it covers, and the level of an
// agent of the chain.
func TestATableRuleWithoutValidityRiskOrAgentLevelIsInvalid(t *testing.T) {
	t.Parallel()
	good := tableRule(1, "npm install *", classes.RiskLow)
	if err := rules(t, tableRuleYAML, good).Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mod := range map[string]func(*classes.Decree){
		"no validity": func(d *classes.Decree) { d.Until = nil },
		"alto":        func(d *classes.Decree) { d.Risk = classes.RiskHigh },
		"no risk":     func(d *classes.Decree) { d.Risk = "" },
		"a person":    func(d *classes.Decree) { d.Level = classes.LevelOwner },
	} {
		bad := good
		mod(&bad)
		p, err := classes.ParsePermissions([]byte(tableRuleYAML))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := classes.Build(classes.Layers{AgentID: agentID, Class: "qa", Permissions: p, Decrees: []classes.Decree{bad}, Now: now}); err == nil {
			t.Errorf("%s: the rules were built", name)
		}
	}
}

// A table rule never lifts a native block.
func TestATableRuleLiftsNoNativeBlock(t *testing.T) {
	t.Parallel()
	m := newMachine()
	exception := classes.Decree{ID: 9, Kind: classes.DecreeException, Level: classes.LevelTableMaster, Effect: classes.Effect{Kind: classes.EffectPermission},
		Action: &classes.Action{Kind: providers.KindShell, Command: "npm install evil"}}
	ch := classes.NativeCharacter{AgentID: agentID, Provider: providers.ClaudeCode, Worktree: m.wt}
	withRule := classes.NativePolicy(rules(t, tableRuleYAML, tableRule(5, "npm install *", classes.RiskLow)), ch, providers.NativeFallbacks{})
	if len(withRule.DenyCommands) != 1 || withRule.DenyCommands[0] != "npm install evil" {
		t.Errorf("native denials with a table rule = %v, want the class's kept", withRule.DenyCommands)
	}
	withException := classes.NativePolicy(rules(t, tableRuleYAML, exception), ch, providers.NativeFallbacks{})
	if len(withException.DenyCommands) != 0 {
		t.Errorf("native denials with an exception = %v, want it lifted", withException.DenyCommands)
	}
}
