package classes_test

import (
	"slices"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/providers"
)

// CA-PERM-24
func TestTheMachineDeniesTheSubagentToolsOfTheProviderInTheCharacterItself(t *testing.T) {
	t.Parallel()
	m := newMachine()
	p, err := classes.ParsePermissions([]byte("version: 1\ntools: {allow: [\"invoke_subagent\"]}\n"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := classes.Build(classes.Layers{
		AgentID: agentID, Class: "dev", Permissions: p, Now: now,
		DenyTools: providers.SubagentTools(providers.Antigravity),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range providers.SubagentTools(providers.Antigravity) {
		want(t, decide(m.env(), r, toolEvent(providers.KindSubagent, name)), classes.Deny, classes.LayerMachine, name, "conformidade desta máquina")
		if r.Origin["tools.deny:"+name] != classes.LayerMachine {
			t.Errorf("origin of %s = %q", name, r.Origin["tools.deny:"+name])
		}
	}
	if len(r.Tools.Allow) != 0 {
		t.Errorf("tools.allow = %v; the denial of the machine wins over the class", r.Tools.Allow)
	}
	// The subagent of another provider stays allowed (ADR 0025).
	want(t, decide(m.env(), r, toolEvent(providers.KindSubagent, "Agent")), classes.Allow, "")
	// The native tools field of Antigravity loses the subagent tools and
	// keeps the others.
	ch := classes.NativeCharacter{AgentID: agentID, Provider: providers.Antigravity, Worktree: m.wt}
	tools := providers.AntigravityTools(classes.NativePolicy(r, ch, providers.NativeFallbacks{}))
	for _, name := range providers.SubagentTools(providers.Antigravity) {
		if slices.Contains(tools, name) {
			t.Errorf("the tools field keeps %s", name)
		}
	}
	if !slices.Contains(tools, "write_to_file") {
		t.Errorf("the tools field %v lost write_to_file", tools)
	}
}
