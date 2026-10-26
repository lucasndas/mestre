package fakechannel

import (
	"context"
	"sync"

	"github.com/mestre-ia/mestre/internal/approvals"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// Chain keeps the requests in memory and decides as the chain does for a
// person: it closes the request (in the database too) and withdraws the
// question through the flow.
type Chain struct {
	mu       sync.Mutex
	Flow     *approvals.Flow
	reqs     map[int64]*chain.Request
	decided  []chain.PersonDecision
	invalid  []int64
	failNext error
	clk      clock.Clock
	db       *store.DB
}

var _ approvals.Chain = (*Chain)(nil)

// NewChain returns an empty chain over the database of the test.
func NewChain(db *store.DB, clk clock.Clock) *Chain {
	return &Chain{reqs: map[int64]*chain.Request{}, clk: clk, db: db}
}

// Add puts an open request in the chain and in the database.
func (c *Chain) Add(ctx context.Context, r chain.Request) error {
	c.mu.Lock()
	c.reqs[r.ID] = &r
	c.mu.Unlock()
	_, err := c.db.Writer().ExecContext(ctx, `INSERT INTO approval_requests (id, workspace, type, state, level_kind, body, created_at) VALUES (?, ?, ?, 'open', ?, '{}', 1)`, r.ID, r.Workspace, string(r.Type), string(r.Level.Kind))
	return err
}

// Get implements approvals.Chain.
func (c *Chain) Get(_ context.Context, id int64) (chain.Request, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.reqs[id]
	if !ok {
		return chain.Request{}, chain.ErrNoRequest
	}
	return *r, nil
}

func (c *Chain) close(id int64, state chain.State, by string) {
	c.mu.Lock()
	r := c.reqs[id]
	r.State, r.ClosedAt = state, c.clk.Now()
	r.Decision = &chain.Closing{State: state, By: by}
	c.mu.Unlock()
	if _, err := c.db.Writer().ExecContext(context.Background(), `UPDATE approval_requests SET state = ? WHERE id = ?`, string(state), id); err != nil {
		panic(err)
	}
	_ = c.Flow.Withdraw(context.Background(), id, string(state))
}

// FailNext makes the next decision fail with err.
func (c *Chain) FailNext(err error) {
	c.mu.Lock()
	c.failNext = err
	c.mu.Unlock()
}

// DecideByPerson implements approvals.Chain.
func (c *Chain) DecideByPerson(_ context.Context, d chain.PersonDecision) (chain.Outcome, error) {
	c.mu.Lock()
	if c.failNext != nil {
		err := c.failNext
		c.failNext = nil
		c.mu.Unlock()
		return chain.Outcome{}, err
	}
	c.decided = append(c.decided, d)
	c.mu.Unlock()
	if d.Answer == chain.AnswerAskBack {
		return chain.Outcome{Request: d.Request, State: chain.Open, Applied: "asked_back"}, nil
	}
	st := chain.Denied
	if d.Answer.Approves() || d.Answer == chain.AnswerDelegate {
		st = chain.Approved
	}
	c.close(d.Request, st, "user:"+d.User)
	return chain.Outcome{Request: d.Request, State: st}, nil
}

// Invalidate implements approvals.Chain.
func (c *Chain) Invalidate(_ context.Context, id int64, _ string) error {
	c.mu.Lock()
	c.invalid = append(c.invalid, id)
	c.mu.Unlock()
	c.close(id, chain.Invalidated, "")
	return nil
}

// Decisions are the decisions taken so far.
func (c *Chain) Decisions() []chain.PersonDecision {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]chain.PersonDecision(nil), c.decided...)
}

// State is the state of a request now.
func (c *Chain) State(id int64) chain.State {
	r, _ := c.Get(context.Background(), id)
	return r.State
}
