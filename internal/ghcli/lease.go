package ghcli

import (
	"context"
	"errors"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"

	"github.com/lucasndas/mestre/internal/localchan"
)

// Lease is the lease of the epoch over GitHub (docs/guilda/trava-no-github.md#aluguel):
// every call to GitHub starts only while it is valid, and the engine does no
// check of its own. With it expired, what changes data waits in the queue of the
// Citadel and goes out once, in order, when it is back; a reading is refused.
type Lease interface {
	// Allow is nil when GitHub may be called now.
	Allow() error
	// Do runs fn now, if the lease allows it, or queues it for when it does.
	Do(ctx context.Context, name string, fn func(ctx context.Context) error) (queued bool, err error)
}

// Quota says whether the App of a character is under 10% of its primary quota,
// and when it renews.
type Quota interface {
	Low(ctx context.Context, c characters.Character) (reset time.Time, low bool)
}

// quotaLow is the renewal time when the App of who asks is under its quota.
func (h *Handlers) quotaLow(ctx context.Context, c *localchan.Call) (time.Time, bool) {
	if h.Quota == nil || !c.Caller.Identified {
		return time.Time{}, false
	}
	ch, err := h.Characters.Get(ctx, c.Caller.AgentID)
	if err != nil {
		return time.Time{}, false
	}
	return h.Quota.Low(ctx, ch)
}

func (h *Handlers) isLocked(ctx context.Context) bool { return h.Locked != nil && h.Locked(ctx) }

func lockedResponse() localchan.Response {
	return localchan.Fail("locked", "o Mestre está travado: nada é feito no GitHub até o Dono destravar", "o Dono destrava com /destravar")
}

type handler func(ctx context.Context, c *localchan.Call) localchan.Response

func unavailable(why string) localchan.Response {
	return localchan.Fail("github_unavailable", "GitHub can not be called now: "+why, "try again later")
}

// read serves a command that only reads: refused while the lease is not valid.
func (h *Handlers) read(f handler) handler {
	return func(ctx context.Context, c *localchan.Call) localchan.Response {
		if h.isLocked(ctx) {
			return lockedResponse()
		}
		if h.Lease == nil {
			return unavailable("the lease of the epoch is not set")
		}
		if err := h.Lease.Allow(); err != nil {
			return unavailable("the lease of the epoch is not valid")
		}
		if reset, low := h.quotaLow(ctx, c); low {
			return localchan.Fail("github_unavailable", "the quota of GitHub is almost over; it renews at "+reset.UTC().Format("15:04")+" UTC", "try again after "+reset.UTC().Format("15:04")+" UTC")
		}
		return f(ctx, c)
	}
}

// write serves a command that changes data: with the lease valid it runs now;
// otherwise it waits in the queue of the Citadel and the answer is queued. The
// operation carries its marker (the id of the request), so what goes out later
// does not double what an earlier sending made.
func (h *Handlers) write(name string, f handler) handler {
	return func(ctx context.Context, c *localchan.Call) localchan.Response {
		if h.isLocked(ctx) {
			return lockedResponse()
		}
		if h.Lease == nil {
			return unavailable("the lease of the epoch is not set")
		}
		run := func(ctx context.Context) localchan.Response {
			var resp localchan.Response
			queued, err := h.Lease.Do(ctx, name, func(ctx context.Context) error {
				// it may have waited: the lock is looked at when it goes out
				if h.isLocked(ctx) {
					resp = lockedResponse()
					return nil
				}
				resp = f(ctx, c)
				return nil
			})
			switch {
			case err != nil:
				if errors.Is(err, context.Canceled) {
					return unavailable("the request was cancelled")
				}
				return unavailable("this Citadel does not act")
			case queued:
				return localchan.OK(map[string]any{"queued": true})
			}
			return resp
		}
		// under the quota: it waits for the renewal and goes out once
		if reset, low := h.quotaLow(ctx, c); low {
			h.Clock.AfterFunc(max(reset.Sub(h.Clock.Now()), 0), func() { run(context.Background()) })
			return localchan.OK(map[string]any{"queued": true})
		}
		return run(ctx)
	}
}
