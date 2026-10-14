package app

import (
	"context"
	"errors"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
)

type rulesFor map[string]*classes.Rules

func (r rulesFor) Rules(_ context.Context, agentID string) (*classes.Rules, error) {
	if v, ok := r[agentID]; ok {
		return v, nil
	}
	return nil, errors.New("no rules")
}

// The profile of a character is what this machine keeps: the cli.allow of
// its effective rules, as an ordinary character; one without rules allows
// nothing.
func TestTheProfileOfACharacterComesFromItsEffectiveRules(t *testing.T) {
	t.Parallel()
	src := rulesFor{"alice": {CLI: classes.CLIRules{Allow: []string{"issue comment"}}}}
	p := rulesProfiles{src: src}
	alice := p.Profile(t.Context(), "alice")
	if alice.AllowsCLI == nil || !alice.AllowsCLI("issue comment") || alice.AllowsCLI("issue close") {
		t.Fatalf("alice: %+v", alice)
	}
	if got := p.Profile(t.Context(), "stranger"); got.AllowsCLI != nil || got.Role != 0 {
		t.Fatalf("a character with no rules: %+v", got)
	}
}
