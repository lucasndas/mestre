package app

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

// rulesPublishTimeout bounds one publication of the effective rules, which does
// not depend on the context of whoever made the change (a Telegram handler that
// ended, a hook that answered).
const rulesPublishTimeout = 30 * time.Second

// rulesRetryEvery is how often a publication that failed is tried again.
const rulesRetryEvery = 10 * time.Second

// rulesRepublisher keeps the effective rules in step with the decrees: a change
// publishes them at once, with a context of its own and a limit, and a
// publication that failed leaves a "dirty rules" flag that the loop of the
// period retries, so a revocation never depends on one best-effort send.
type rulesRepublisher struct {
	pub interface {
		PublishAll(ctx context.Context) error
	}
	clk   clock.Clock
	log   *slog.Logger
	dirty atomic.Bool
}

// Changed is told after a decree, or the level of a user, changed.
func (r *rulesRepublisher) Changed(ctx context.Context) { r.publish(context.WithoutCancel(ctx)) }

func (r *rulesRepublisher) publish(ctx context.Context) {
	r.dirty.Store(false) // a change after this point publishes by itself
	ctx, cancel := clock.WithTimeout(ctx, r.clk, rulesPublishTimeout)
	defer cancel()
	if err := r.pub.PublishAll(ctx); err != nil {
		r.dirty.Store(true)
		r.log.Warn("app: the effective rules were not sent after a change of the decrees; they are retried", "err", err)
	}
}

// Dirty says a publication is waiting to be retried.
func (r *rulesRepublisher) Dirty() bool { return r.dirty.Load() }

// Loop retries what failed until the period ends.
func (r *rulesRepublisher) Loop(ctx context.Context, every time.Duration) error {
	t := r.clk.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C():
			if r.dirty.Load() {
				r.publish(context.WithoutCancel(ctx))
			}
		}
	}
}
