package classes_test

import (
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/providers"
)

// exactRule is a temporary table rule of the exact action, as the chain makes it
// for "approve for a time".
func exactRule(id int64, level classes.Level, risk classes.Risk, command string) classes.Decree {
	until := now.Add(time.Hour)
	return classes.Decree{ID: id, Kind: classes.DecreeTableRule, Level: level, Until: &until, Risk: risk, ToolKind: providers.KindShell,
		Action: &classes.Action{Kind: providers.KindShell, Command: command}, Effect: classes.Effect{Kind: classes.EffectPermission}}
}

// CA-RTM-09: the rule of the exact action is held to what the rule of a pattern
// is. The same text of a command can do another thing at each use (a script of
// the worktree that changes, a substitution, a chain whose last part reads a
// file), so a part the engine does not analyse and a chain, a redirection or a
// substitution are never freed by a rule, whoever made it.
func TestATableRuleOfTheExactActionNeverFreesAnOpaquePartNorAChainedCommand(t *testing.T) {
	t.Parallel()
	m := newMachine()
	for _, level := range []classes.Level{classes.LevelTableMaster, classes.LevelGrandMaster} {
		for _, c := range []struct {
			command string
			risk    classes.Risk
			freed   bool
		}{
			{"npm install lodash", classes.RiskLow, true},
			{"make build", classes.RiskMedium, true},
			{"alias ll=ls", classes.RiskMedium, false},            // a part the engine does not analyse (no group)
			{"echo $(cat run.sh)", classes.RiskMedium, false},     // a substitution
			{"make build && ./run.sh", classes.RiskMedium, false}, // a chain whose last part reads a file of the worktree
			{"make build; bash run.sh", classes.RiskMedium, false},
			{"npm install lodash && npm test", classes.RiskMedium, false},
			{"npm install lodash > out.txt", classes.RiskMedium, false},
			{"npm install lodash | tee out.txt", classes.RiskMedium, false},
			{"npm install lodash\nnpm test", classes.RiskMedium, false},
		} {
			gate := &gateFake{allow: true}
			env := m.env()
			env.TableRules = gate
			r := rules(t, tableRuleYAML, exactRule(3, level, c.risk, c.command))
			got := decide(env, r, m.shell(c.command))
			if c.freed {
				want(t, got, classes.Allow, classes.LayerDecree)
				continue
			}
			if got.Verdict == classes.Allow {
				t.Errorf("level %v: %q was freed by a rule of the exact action (%s)", level, c.command, got.Reason)
			}
			if len(gate.asked) != 0 {
				t.Errorf("level %v: the gate was asked for %q, which no rule may free", level, c.command)
			}
		}
	}
}
