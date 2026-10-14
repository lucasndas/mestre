package conformance_test

import (
	"slices"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/conformance"
	"github.com/lucasndas/mestre/internal/providers"
)

// A failed step 12 turns the plan B on for that provider only, and the
// effective rules then deny its subagent tools; the other providers keep
// them (docs/provedores/despachante-de-hooks.md#verificar-na-implementação).
// CA-PERM-24
func TestAFailedSubagentStepDeniesTheSubagentToolsOfThatProviderOnly(t *testing.T) {
	t.Parallel()
	steps := func(outcome conformance.Outcome, reason string) []conformance.StepResult {
		return []conformance.StepResult{{Step: conformance.StepSubagent, Outcome: outcome, Reason: reason}}
	}
	cases := []struct {
		name     string
		provider providers.ID
		steps    []conformance.StepResult
		deny     bool
	}{
		{"antigravity approval screen", providers.Antigravity, steps(conformance.Failed, "o turno não terminou em 3m0s"), true},
		{"claude held", providers.ClaudeCode, steps(conformance.Passed, ""), false},
		{"codex held", providers.Codex, steps(conformance.Passed, ""), false},
		{"step 12 did not run", providers.Antigravity, steps(conformance.Failed, "não rodou: a etapa 1 falhou"), false},
	}
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	for _, tt := range cases {
		res := conformance.Result{Provider: tt.provider, Version: "1.3.5", Steps: tt.steps}
		res.Decide()
		got := res.DenyTools()
		if res.Flags.DenySubagent != tt.deny || (len(got) > 0) != tt.deny {
			t.Errorf("%s: deny_subagent = %v, tools %v; want %v", tt.name, res.Flags.DenySubagent, got, tt.deny)
			continue
		}
		perms, err := classes.ParsePermissions([]byte("version: 1\n"))
		if err != nil {
			t.Fatal(err)
		}
		rules, err := classes.Build(classes.Layers{AgentID: "01JQAGENTQA", Class: "dev", Permissions: perms, DenyTools: got, Now: now})
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range providers.SubagentTools(tt.provider) {
			if slices.Contains(rules.Tools.Deny, name) != tt.deny {
				t.Errorf("%s: tools.deny %v for %s; want denied = %v", tt.name, rules.Tools.Deny, name, tt.deny)
			}
		}
	}
	if got := providers.SubagentTools(providers.Antigravity); !slices.Contains(got, "invoke_subagent") {
		t.Errorf("antigravity subagent tools = %v", got)
	}
}
