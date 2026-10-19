package classes_test

import (
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/providers"
)

// exactRule is a temporary table rule of the exact action of a shell command, as
// the chain makes it for "approve for a time".
func exactRule(id int64, level classes.Level, risk classes.Risk, command string) classes.Decree {
	until := now.Add(time.Hour)
	return classes.Decree{ID: id, Kind: classes.DecreeTableRule, Level: level, Until: &until, Risk: risk, ToolKind: providers.KindShell,
		Action: &classes.Action{Kind: providers.KindShell, Command: command}, Effect: classes.Effect{Kind: classes.EffectPermission}}
}

// CA-RTM-09: the rule of the exact action is held to what the rule of a pattern
// is. The same text of a command can do another thing at each use (a script of
// the worktree that changes, a substitution, a chain whose last part reads a
// file), so a part the engine does not analyse and a chain, a redirection or a
// substitution are never freed by a rule, whoever made it. The line break counts
// as the tool gave it: the action keeps the command trimmed, and a break followed
// by a space of another script is not trimmed away for this.
func TestATableRuleOfTheExactActionNeverFreesAnOpaquePartNorAChainedCommand(t *testing.T) {
	t.Parallel()
	m := newMachine()
	for _, level := range []classes.Level{classes.LevelTableMaster, classes.LevelGrandMaster} {
		for _, c := range []struct {
			rule    string // the command the rule was made for; the call, when empty
			command string
			risk    classes.Risk
			freed   bool
		}{
			{"", "npm install lodash", classes.RiskLow, true},
			{"", "make build", classes.RiskMedium, true},
			{"", "alias ll=ls", classes.RiskMedium, false},            // a part the engine does not analyse (no group)
			{"", "make build && ./run.sh", classes.RiskMedium, false}, // a chain whose last part reads a file of the worktree
			{"", "make build; bash run.sh", classes.RiskMedium, false},
			{"", "npm install lodash && npm test", classes.RiskMedium, false},
			{"", "npm install lodash > out.txt", classes.RiskMedium, false},
			{"", "npm install lodash | tee out.txt", classes.RiskMedium, false},
			{"", "npm install lodash\nnpm test", classes.RiskMedium, false},
			{"make build", "make build\n\U00003000", classes.RiskMedium, false},            // the break is not trimmed away by a space of another script
			{"make build", "make build\n\U00002028", classes.RiskMedium, false},            // nor by a line separator
			{"make build", "\v\nmake build", classes.RiskMedium, false},                    // nor a vertical tab before it
			{"npm install lodash", "npm install lodash\U000000a0", classes.RiskLow, false}, // a no-break space after it is part of the word for the shell
			{"npm install lodash", "\fnpm install lodash", classes.RiskLow, false},         // a form feed before it
			{"npm install lodash", "npm install lodash\U00003000", classes.RiskLow, false}, // an ideographic space after it
			{"./run.sh", "./run.sh\U000000a0", classes.RiskMedium, false},                  // the script of the worktree, with one: another file
			{"npm install lodash", "npm install lodash\n", classes.RiskLow, true},          // a plain end of line is not a second line
			{"npm install lodash", "  npm install lodash \r\n", classes.RiskLow, true},     // nor blanks around it
		} {
			gate := &gateFake{allow: true}
			env := m.env()
			env.TableRules = gate
			rule := c.rule
			if rule == "" {
				rule = c.command
			}
			r := rules(t, tableRuleYAML, exactRule(3, level, c.risk, rule))
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

// CA-RTM-09: an action with no group is not freed either when it is not a shell
// command. A tool the table has no row for (an MCP tool, a tool of the provider)
// is a medio with no group, and the rule of its exact action compares only the
// name of the tool, so it would free every later call of the tool, whatever its
// arguments.
func TestATableRuleOfTheExactActionNeverFreesAToolTheTableHasNoRowFor(t *testing.T) {
	t.Parallel()
	m := newMachine()
	const yaml = "version: 1\ncommands: {default: ask}\ntools: {default: ask}\n"
	for _, level := range []classes.Level{classes.LevelTableMaster, classes.LevelGrandMaster} {
		for _, tool := range []struct {
			kind providers.ToolKind
			name string
		}{{providers.KindMCP, "mcp__db__query"}, {providers.KindOther, "run_command"}} {
			until := now.Add(time.Hour)
			d := classes.Decree{ID: 4, Kind: classes.DecreeTableRule, Level: level, Until: &until, Risk: classes.RiskMedium, ToolKind: tool.kind,
				Action: &classes.Action{Kind: tool.kind, Tool: tool.name}, Effect: classes.Effect{Kind: classes.EffectPermission}}
			gate := &gateFake{allow: true}
			env := m.env()
			env.TableRules = gate
			got := decide(env, rules(t, yaml, d), toolEvent(tool.kind, tool.name))
			if got.Verdict == classes.Allow {
				t.Errorf("level %v: every call of %s was freed by a rule of its exact action (%s)", level, tool.name, got.Reason)
			}
			if len(gate.asked) != 0 {
				t.Errorf("level %v: the gate was asked for %s, which no rule may free", level, tool.name)
			}
		}
	}
}
