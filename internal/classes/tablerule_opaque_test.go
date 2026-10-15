package classes_test

import (
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/providers"
)

// A table rule never frees a part the engine does not analyse, for any level,
// the Grão-Mestre included: the classification with no group (an unclosed
// quote, a brace list, a .NET call) or a command with subshell syntax stays an
// ask, and the gate is not even asked.
func TestATableRuleNeverFreesAnOpaquePartOfACommandForAnyLevel(t *testing.T) {
	t.Parallel()
	m := newMachine()
	until := now.Add(time.Hour)
	for _, level := range []classes.Level{classes.LevelTableMaster, classes.LevelGrandMaster} {
		d := classes.Decree{ID: 9, Kind: classes.DecreeTableRule, Level: level, Until: &until, Risk: classes.RiskMedium, ToolKind: providers.KindShell,
			Effect: classes.Effect{Kind: classes.EffectPermission, Allow: &classes.Grant{Commands: []string{"mytool *"}}}}
		r := rules(t, "version: 1\ncommands:\n  default: ask\n", d)
		for _, os := range []string{"", "windows"} {
			gate := &gateFake{allow: true}
			env := m.env()
			env.OS, env.TableRules = os, gate
			want(t, decide(env, r, m.shell("mytool x")), classes.Allow, classes.LayerDecree)
			for _, c := range []string{"mytool {a,b}", "mytool \"unclosed", "mytool ([IO.File]::Delete('x'))", "mytool @(Remove-Item x)", "mytool (Remove-Item -Recurse x)"} {
				before := len(gate.asked)
				if got := decide(env, r, m.shell(c)); got.Verdict == classes.Allow {
					t.Errorf("level %v os %q: %q was freed by a table rule (%s)", level, os, c, got.Reason)
				}
				if len(gate.asked) != before {
					t.Errorf("level %v os %q: the gate was asked for the opaque %q", level, os, c)
				}
			}
		}
	}
}
