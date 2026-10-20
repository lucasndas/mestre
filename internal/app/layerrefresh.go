package app

import (
	"context"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/sessionzero"
)

// The Mestre layer is brought up to date at the start of each turn
// (docs/sessao-zero/personagem.md#atualização-a-cada-turno): a decree that
// reaches the character, the extra instructions of the users and its class are
// read again from the Citadel of the period, the MESTRE.md is rewritten and the
// hook delivers only the sections that changed. Never in the middle of a turn.

// layerRefresher is the session zero of the machine as far as the update goes
// (sessionzero.Character).
type layerRefresher interface {
	Refresh(ctx context.Context, agentID string, d sessionzero.Dynamic) (sessionzero.RefreshReport, error)
}

// refreshingLayers wraps the session zero of the machine so that its hook
// contexts bring the layer up to date before they deliver it. A Preparer that
// cannot refresh (a test double) is returned as it is.
func (e *Engine) refreshingLayers(p characters.Preparer) characters.Preparer {
	r, ok := p.(layerRefresher)
	if !ok {
		return p
	}
	return refreshingPrep{Preparer: p, r: r, e: e}
}

type refreshingPrep struct {
	characters.Preparer
	r layerRefresher
	e *Engine
}

// Contexts implements characters.Preparer.
func (p refreshingPrep) Contexts() hook.Contexts {
	return &refreshingContexts{inner: p.Preparer.Contexts(), r: p.r, e: p.e}
}

type refreshingContexts struct {
	inner hook.Contexts
	r     layerRefresher
	e     *Engine
	// locks serialize the updates of one character, so an older reading never
	// overwrites a newer one.
	locks sync.Map
}

// refreshTimeout is how long the update may hold the start of a turn.
const refreshTimeout = 2 * time.Second

// Context implements hook.Contexts. A Citadel that cannot say what changed (no
// period that acts, a database that does not answer) leaves the layer as it
// is: the turn is never held for it, and the next turn tries again.
func (c *refreshingContexts) Context(ctx context.Context, ev providers.Event) (string, error) {
	if ev.Event == providers.EventTurnStart && ev.AgentID != "" {
		if p := c.e.period.Load(); p != nil && p.layers != nil {
			mu, _ := c.locks.LoadOrStore(ev.AgentID, &sync.Mutex{})
			mu.(*sync.Mutex).Lock()
			rctx, cancel := context.WithTimeout(ctx, refreshTimeout)
			d, err := p.layers(rctx, ev.AgentID)
			if err == nil {
				_, err = c.r.Refresh(rctx, ev.AgentID, d)
			}
			cancel()
			mu.(*sync.Mutex).Unlock()
			if err != nil {
				c.e.log.Warn("app: the Mestre layer was not brought up to date at the start of the turn", "id", ev.AgentID, "err", err)
			}
		}
	}
	return c.inner.Context(ctx, ev)
}
