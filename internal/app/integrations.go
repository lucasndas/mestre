package app

import (
	"context"
	"log/slog"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/vault"
	"github.com/lucasndas/mestre/pkg/clock"
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
