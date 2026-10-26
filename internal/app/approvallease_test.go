package app

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
)

type countingSink struct{ n atomic.Int32 }

func (c *countingSink) Decided(context.Context, chain.Result) error { c.n.Add(1); return nil }

// An approval a person gives reaches GitHub (a review, a merge, a ruleset) only
// under the lease of the epoch: with the fence in lock-down the sink is not
// called, the decision waits in the queue of the fence, and it is applied once
// when the same epoch is confirmed again (docs/guilda/trava-no-github.md#aluguel).
func TestAnApprovalOfAPersonReachesGitHubOnlyUnderTheLease(t *testing.T) {
	t.Parallel()
	f := newFencedGitHub(t, false)
	f.state(guild.FenceConfirmed)
	sink := &countingSink{}
	late := f.run.lateSink(func() chain.Sink { return sink })
	res := chain.Result{Request: chain.Request{ID: 42}, Approved: true}

	// under the lease: applied now
	if err := late.Decided(f.ctx, res); err != nil || sink.n.Load() != 1 {
		t.Fatalf("with the lease: err %v, %d applications; want one", err, sink.n.Load())
	}

	// the lease is gone: nothing goes out, the decision waits
	f.lock.SetAllDown(true)
	f.clk.Advance(2 * guild.LeaseValidity)
	f.state(guild.FenceLockDown)
	if err := late.Decided(f.ctx, res); err != nil {
		t.Fatalf("a decision under lock-down: %v; want it kept, not an error", err)
	}
	if sink.n.Load() != 1 || f.fence.Queued() != 1 {
		t.Fatalf("lock-down: %d applications, %d queued; want the approval held and not applied", sink.n.Load(), f.fence.Queued())
	}

	// the same epoch is confirmed again: it goes out once
	f.lock.SetAllDown(false)
	f.clk.Advance(guild.LeaseInterval)
	f.state(guild.FenceConfirmed)
	f.until("the queued approval to be applied", func() bool { return sink.n.Load() == 2 })
	f.clk.Advance(2 * guild.LeaseInterval)
	if sink.n.Load() != 2 || f.fence.Queued() != 0 {
		t.Errorf("after the lease is back: %d applications, %d queued; want the approval applied once", sink.n.Load(), f.fence.Queued())
	}
}
