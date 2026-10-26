package rituals

import (
	"context"
	"fmt"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/providers"
)

// Decider is the decision of permissions of a character (*classes.Policy): the
// effective rules of the character decide the event.
type Decider interface {
	Decide(ctx context.Context, ev providers.Event) classes.Decision
}

// PolicyPermissions decides the pre-run of a ritual exactly as the command of
// the target itself: the same Policy that answers its hooks gets a shell event
// of the target, so the fixed rules, the class, the ceiling and the decrees
// all hold. Only allow lets it through; a command that would ask for approval
// is refused (a pre-run has nobody to answer), and so is every doubt.
type PolicyPermissions struct{ Policy Decider }

// Allow implements Permissions.
func (p PolicyPermissions) Allow(ctx context.Context, c characters.Character, command string) (Decision, error) {
	if p.Policy == nil {
		return Decision{Reason: "sem a decisão de permissões"}, nil
	}
	ev := providers.Event{
		Version: providers.EventVersion, Provider: providers.ClaudeCode, NativeEvent: "PreToolUse",
		Event: providers.EventPreTool, AgentID: c.ID, TableID: c.Table, Cwd: c.Dir,
		Tool: &providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: command},
	}
	d := p.Policy.Decide(ctx, ev)
	switch d.Verdict {
	case classes.Allow:
		return Decision{Allow: true}, nil
	case classes.Ask:
		return Decision{Reason: fmt.Sprintf("pediria aprovação (%s)", d.Reason)}, nil
	}
	return Decision{Reason: d.Reason}, nil
}
