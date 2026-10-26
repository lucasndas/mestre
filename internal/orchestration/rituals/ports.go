package rituals

import (
	"context"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/orchestration/quests"
)

// Inbox is the inbox of the characters (*inbox.Service): the delivery of a
// prompt and the return of a firing to its owner.
type Inbox interface {
	Enqueue(ctx context.Context, n inbox.NewItem) (inbox.Item, error)
	// ByKey finds the item queued with the idempotency key: a firing found in
	// its delivery after a restart is delivered once.
	ByKey(ctx context.Context, key string) (inbox.Item, bool, error)
}

// Quests is what the rituals need of the quests (*quests.Service).
type Quests interface {
	Get(ctx context.Context, workspace, table, nameOrID string) (quests.Quest, error)
	Run(ctx context.Context, id, input string) (quests.Quest, error)
}

// Pauses says whether a table, or the workspace (table ""), is paused
// (*tables.Service): a firing waits, and may expire.
type Pauses interface {
	Paused(ctx context.Context, workspace, table string) (bool, error)
}

// Terminals types in the terminal of a character on its machine
// (characters.MachineSet; milestone 5 reaches the Towers).
type Terminals interface {
	Input(ctx context.Context, machine, character string, data []byte) error
}

// PreRunRequest is a pre-run: the command runs on the machine and in the
// folder of the target, without interaction.
type PreRunRequest struct {
	Machine, Dir, Command string
	// Character is the target whose permissions decided the command.
	Character string
	Timeout   time.Duration
	// MaxOutput is the most that is kept of the standard output; the rest is
	// dropped.
	MaxOutput int
}

// PreRunResult is what came of a pre-run.
type PreRunResult struct {
	// Output is the standard output, up to MaxOutput bytes.
	Output string
	// ExitCode is the exit code (0 is success); Stderr the tail of the error
	// output; TimedOut is true when the time ran out.
	ExitCode int
	Stderr   string
	TimedOut bool
}

// PreRunner runs the pre-run on the machine of the target. The engine of the
// machine implements it (milestone 5 brings the Towers); nil refuses every
// firing with a pre-run, since it cannot run it.
type PreRunner interface {
	Run(ctx context.Context, r PreRunRequest) (PreRunResult, error)
}

// Decision is the verdict of the permissions on the command of a pre-run.
type Decision struct {
	Allow bool
	// Reason is why it was refused (the text the target would have seen).
	Reason string
}

// Permissions decides a command of a pre-run like a command of the target's own
// (docs/classes/permissoes.md): the same rules, the same denials. A command
// that would ask for approval is refused, since a pre-run has nobody to
// answer. Nil refuses every pre-run: the decision fails closed.
type Permissions interface {
	Allow(ctx context.Context, target characters.Character, command string) (Decision, error)
}

// Zones gives the time zone of a workspace (the Guild's, by default).
type Zones interface {
	Zone(ctx context.Context, workspace string) *time.Location
}

// Approvals brings the explicit request of a user that deleting a ritual needs
// when a character asks (milestone 5): it answers with the number of the
// request of approval.
type Approvals interface {
	RequestDelete(ctx context.Context, by Actor, r Ritual) (request int64, err error)
}

// DeleteGate is what an Approvals may also be: it spends the approval of the
// deletion if there is one for the ritual in the state it is in now, or leaves
// the request open and says which.
type DeleteGate interface {
	DeleteAllowed(ctx context.Context, by Actor, r Ritual) (granted bool, request int64, err error)
}
