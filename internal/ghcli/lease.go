package ghcli

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"

	"github.com/mestre-ia/mestre/internal/localchan"
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

// leaseHeld is nil while the lease of the epoch is still valid. A command that
// takes long (the reads of the mirror, the freshness gate of the chain) asks it
// again right before the call that changes GitHub, so a lease that expired on
// the way does not let the write out while another Citadel may already act.
func (h *Handlers) leaseHeld() *localchan.Response {
	if h.Lease == nil {
		r := unavailable("the lease of the epoch is not set")
		return &r
	}
	if err := h.Lease.Allow(); err != nil {
		r := unavailable("the lease of the epoch expired while the command ran")
		return &r
	}
	return nil
}

func (h *Handlers) isLocked(ctx context.Context) bool { return h.Locked != nil && h.Locked(ctx) }

func lockedResponse() localchan.Response {
	return localchan.Fail("locked", "o Mestre está travado: nada é feito no GitHub até o Senhor da Guilda destravar", "o Senhor da Guilda destrava com /destravar")
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
		// attempt runs the command now, or leaves it in the queue: then later
		// gives, once, what it answered when it went out.
		// then is called with the answer of a command that was queued, once it
		// went out; nothing waits for it, so a queue that is dropped leaves nothing behind.
		attempt := func(ctx context.Context, then func(localchan.Response)) (resp localchan.Response, queuedNow bool) {
			var mu sync.Mutex
			var got *localchan.Response
			var handed bool
			ran := make(chan localchan.Response, 1)
			queued, err := h.Lease.Do(ctx, name, func(ctx context.Context) error {
				// it may have waited: the lock is looked at when it goes out
				r := lockedResponse()
				if !h.isLocked(ctx) {
					r = f(ctx, c)
				}
				mu.Lock()
				if handed {
					mu.Unlock()
					then(r)
					return nil
				}
				got = &r
				mu.Unlock()
				ran <- r
				return nil
			})
			switch {
			case err != nil:
				if errors.Is(err, context.Canceled) {
					return unavailable("the request was cancelled"), false
				}
				return unavailable("this Citadel does not act"), false
			case queued:
				mu.Lock()
				handed = true
				r := got
				mu.Unlock()
				if r != nil { // it went out before Do returned
					then(*r)
				}
				return localchan.Response{}, true
			}
			return <-ran, false
		}
		// what went out after the answer "queued" reaches the inbox of the caller,
		// with the op_id it was given
		report := func(resp localchan.Response) {
			if h.OnQueued != nil && c.Caller.Identified && plainOpID(c.Request.ID) {
				h.OnQueued(context.Background(), c.Request.ID, c.Caller.AgentID, c.Request.Cmd, resp)
			}
		}
		pending := localchan.OK(map[string]any{"queued": true, "op_id": c.Request.ID})
		// under the quota: it waits for the renewal and goes out once
		if reset, low := h.quotaLow(ctx, c); low {
			h.Clock.AfterFunc(max(reset.Sub(h.Clock.Now()), 0), func() {
				if resp, queued := attempt(context.Background(), report); !queued {
					report(resp)
				}
			})
			return pending
		}
		resp, queued := attempt(ctx, report)
		if !queued {
			return resp
		}
		return pending
	}
}
