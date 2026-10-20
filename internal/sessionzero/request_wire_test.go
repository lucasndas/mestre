package sessionzero_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/presets"
	"github.com/lucasndas/mestre/internal/sessionzero"
)

// Part of CA-CIDADELA-16: the request of the session zero that the Citadel
// renders crosses the connection to a Tower as JSON and must come out the
// same, with the class, the rules, the base and the key of the provider.
func TestTheRequestOfACharacterIsTheSameAfterTheWire(t *testing.T) {
	t.Parallel()
	r := newCharRig(t, nil)
	for _, role := range []sessionzero.Role{sessionzero.RoleCharacter, sessionzero.RoleTableMaster, sessionzero.RoleGrandMaster} {
		for _, agent := range []string{presets.AgentClaudeCode, presets.AgentCodex, presets.AgentShell} {
			q := r.req("c1", agent, role)
			q.ProviderKey = &sessionzero.EnvVar{Name: "ANTHROPIC_API_KEY", Value: "k"}
			q.Model, q.ResumeSessionID = "opus", "22222222-2222-4333-8444-555555555555"
			q.Instructions = "faça"
			b, err := json.Marshal(q)
			if err != nil {
				t.Fatalf("%s %s: %v", role, agent, err)
			}
			var back sessionzero.CharacterRequest
			if err := json.Unmarshal(b, &back); err != nil {
				t.Fatalf("%s %s: %v", role, agent, err)
			}
			if !reflect.DeepEqual(q, back) {
				t.Errorf("%s %s: the request changed on the wire:\n%+v\n%+v", role, agent, q, back)
			}
		}
	}
}
