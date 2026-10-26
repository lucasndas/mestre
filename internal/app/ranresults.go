package app

import (
	"context"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// A call that may wait in the outbox (`done`) first goes to the Citadel to
// run now; when its answer is lost on the way back, the Tower tells the CLI
// "queued" and sends the same message again from the outbox. The Citadel
// already ran it, so the second one is a duplicate and runs nothing; the
// result of the first run is what goes to the inbox of the caller
// (OnQueuedResult), once. ranCalls keeps those results, by sender and id, for
// that.
const (
	// ranKeep is how long the result of a run is kept for a duplicate.
	ranKeep = time.Hour
	// ranMax bounds the results kept; the oldest go first.
	ranMax = 1024
)

type ranCall struct {
	at                      time.Time
	done, wanted, delivered bool
	result                  QueuedResult
}

type ranCalls struct {
	clk     clock.Clock
	deliver func(QueuedResult)

	mu    sync.Mutex
	calls map[string]*ranCall
	order []string
}

func newRanCalls(clk clock.Clock, deliver func(QueuedResult)) *ranCalls {
	return &ranCalls{clk: clk, deliver: deliver, calls: map[string]*ranCall{}}
}

func ranKey(sender, id string) string { return sender + "\x00" + id }

// started notes a run of a call that may be queued.
func (r *ranCalls) started(sender, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.clk.Now()
	for len(r.order) > 0 {
		old, ok := r.calls[r.order[0]]
		if ok && len(r.order) < ranMax && now.Sub(old.at) < ranKeep {
			break
		}
		delete(r.calls, r.order[0])
		r.order = r.order[1:]
	}
	k := ranKey(sender, id)
	if _, ok := r.calls[k]; !ok {
		r.calls[k] = &ranCall{at: now}
		r.order = append(r.order, k)
	}
}

// finished keeps the result of the run, and delivers it when the same call
// already came back from the outbox.
func (r *ranCalls) finished(sender, id string, res QueuedResult) {
	r.mu.Lock()
	c, ok := r.calls[ranKey(sender, id)]
	if !ok {
		r.mu.Unlock()
		return
	}
	c.done, c.result = true, res
	send := c.wanted && !c.delivered
	c.delivered = c.delivered || send
	r.mu.Unlock()
	if send {
		r.deliver(res)
	}
}

// duplicate is the same message again: when it is the call that ran, its
// result goes to the inbox now, or as soon as the run ends. It never blocks
// the session that received it.
func (r *ranCalls) duplicate(sender, id string) {
	r.mu.Lock()
	c, ok := r.calls[ranKey(sender, id)]
	if !ok {
		r.mu.Unlock()
		return
	}
	c.wanted = true
	send := c.done && !c.delivered
	c.delivered = c.delivered || send
	res := c.result
	r.mu.Unlock()
	if send {
		go r.deliver(res)
	}
}

// dupWatch is the memory of the processed messages of the Citadel, telling
// ranCalls about every cli.call that arrives again.
type dupWatch struct {
	protocol.Processed
	onDup func(sender, id string)
}

// Claim implements protocol.Processed.
func (d dupWatch) Claim(ctx context.Context, sender, id, typ string, now time.Time) (bool, []byte, error) {
	fresh, prev, err := d.Processed.Claim(ctx, sender, id, typ, now)
	if err == nil && !fresh && typ == protocol.TypeCLICall {
		d.onDup(sender, id)
	}
	return fresh, prev, err
}
