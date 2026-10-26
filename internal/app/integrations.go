package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/telegram"
	"github.com/mestre-ia/mestre/internal/vault"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The integrations are the pieces of milestone 5 that talk to the world and to
// the people: the bot of the Telegram, the users and the flow of approval over
// them, the commands of GitHub. A Citadel opens them at the start of each
// period, over the main database of the period and its vault; they are put in
// the orchestration before it is built (the people of the chain of approval),
// get its commands in the local channel after it, and run while the fence lets
// the Citadel act.

// IntegrationEnv is what an integration needs from the period of the Citadel.
type IntegrationEnv struct {
	DB      *store.DB
	Vault   *vault.Vault
	Clock   clock.Clock
	Log     *slog.Logger
	Machine string
	// Masker hides the secrets in every message that leaves.
	Masker *logging.Masker
	// SetLocked gives the engine the lock of the period (/travar): its gate of
	// the CLI refuses what starts work while it holds. Nil takes it away.
	SetLocked func(f func(ctx context.Context) bool)
	// DecideJoin gives the answer of the Owner about the entry of a machine to
	// the Citadel of the period; GuildID is the id of the Guild.
	DecideJoin func(ctx context.Context, joinID string, d guild.Decision) error
	GuildID    string
	// Machines lists the machines of the Guild and its epoch, and SetExcluded
	// excludes one from the secrets or undoes it (/guilda, after the Owner's
	// PIN); both are of the acting Citadel. Nil: this machine cannot.
	Machines    func(ctx context.Context) ([]telegram.GuildMachine, uint64, error)
	SetExcluded func(ctx context.Context, machineID string, excluded bool) error
	// Pending lists the entry and promotion requests that wait for the Owner.
	Pending func(ctx context.Context) ([]telegram.GuildPending, error)
	// Transcribers are the Towers that transcribe now, from the sheets of the
	// Guild and the connections (nil: none; docs/telegram/anexos-e-transcricao.md).
	Transcribers func(ctx context.Context) Fleet
}

// GuildApprover is what an IntegrationRun may also be: the question to the Owner
// about the entry of a machine in the Guild (guild.Approver).
type GuildApprover interface {
	GuildApprover() guild.Approver
}

// GuildLocker is what an IntegrationRun may also be: the lock of the epoch over
// the repository of the Guild in GitHub, nil while that repository is not made
// (the Guild has no promotion until it is).
type GuildLocker interface {
	GuildLock(ctx context.Context) (guild.Lock, error)
}

// GuildReconciler is what an IntegrationRun may also be: the reconciliation with
// GitHub that a promoted Citadel makes before it acts outside
// (docs/guilda/cidadela-e-torres.md#github-depois-da-troca).
type GuildReconciler interface {
	GuildReconcile(orc *Orchestration, anchor func(ctx context.Context) (time.Time, error)) func(ctx context.Context) error
}

// reconcilerOf is the reconciliation for FenceConfig.Reconcile: nil when the
// period has no orchestration or the integration reconciles nothing.
func reconcilerOf(integ IntegrationRun, orc *Orchestration, anchor func(ctx context.Context) (time.Time, error)) func(ctx context.Context) error {
	g, ok := integ.(GuildReconciler)
	if !ok || orc == nil {
		return nil
	}
	return g.GuildReconcile(orc, anchor)
}

// Integration opens the integrations of a period. The zero Deps has none.
type Integration interface {
	Open(ctx context.Context, env IntegrationEnv) (IntegrationRun, error)
}

// IntegrationRun is an integration open for one period.
type IntegrationRun interface {
	// Extend puts in the options the pieces the orchestration is made with.
	Extend(o *OrchestrationOptions)
	// Attach puts the commands in the local channel and gives the functions the
	// fence turns on and off.
	Attach(orc *Orchestration, fwd *localchan.LocalForwarder) []ExternalFunc
	// BindFence gives the fence of the period, made after the integration.
	BindFence(f *guild.Fence)
	// FenceChanged is told every change of the state of the fence (the notice to
	// the Owner when GitHub goes out and when it is back).
	FenceChanged(s guild.FenceState)
	// Close ends what the integration holds.
	Close()
}
