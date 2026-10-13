package protocol

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// DefaultInboundLimit bounds the bytes that wait, on one connection, for the
// goroutines that run them: messages for their handlers, acks for the
// outbox, parts for the disk. The read loop never waits for them, so the
// pongs of the transport (read only while the connection is read) keep
// coming however slow a handler is; a peer that sends more than this while
// the receiver is behind is closed with 4008 and sends it again after
// reconnecting (docs/conectividade/protocolo.md#multiplexação-e-prioridade).
const DefaultInboundLimit = 64 << 20

// inboundOverhead is what an item costs besides its bytes, so a flood of
// tiny messages is bounded too.
const inboundOverhead = 256

// ErrOverloaded is the cause of a session closed with 4008: the peer sent
// more than the receiver could run in time.
var ErrOverloaded = errors.New("protocol: the peer is sending faster than the messages run")

// inbound is a queue that the read loop fills without ever waiting and one
// goroutine empties in order. Its items count against a budget of bytes
// shared by every queue of the session.
type inbound[T any] struct {
	budget *atomic.Int64
	limit  int64

	mu    sync.Mutex
	items []inboundItem[T]
	wake  chan struct{}
}

type inboundItem[T any] struct {
	v T
	n int64
}

func newInbound[T any](budget *atomic.Int64, limit int64) *inbound[T] {
	return &inbound[T]{budget: budget, limit: limit, wake: make(chan struct{}, 1)}
}

// push queues v, which weighs size bytes. It returns false, and queues
// nothing, when the budget of the session would go over its limit.
func (q *inbound[T]) push(v T, size int) bool {
	n := int64(size) + inboundOverhead
	if q.budget.Add(n) > q.limit {
		q.budget.Add(-n)
		return false
	}
	q.mu.Lock()
	q.items = append(q.items, inboundItem[T]{v: v, n: n})
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return true
}

// run hands every item to f, in order, until ctx is done; what is still
// queued then is dropped (the peer sends again what needs an answer).
func (q *inbound[T]) run(ctx context.Context, f func(T)) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		q.mu.Lock()
		if len(q.items) == 0 {
			q.mu.Unlock()
			select {
			case <-q.wake:
				continue
			case <-ctx.Done():
				return nil
			}
		}
		it := q.items[0]
		var zero inboundItem[T]
		q.items[0] = zero
		q.items = q.items[1:]
		q.mu.Unlock()
		f(it.v)
		q.budget.Add(-it.n)
	}
}

// drain hands to f what is still queued, once nothing is pushed any more:
// an answer that arrived just before the close is not lost.
func (q *inbound[T]) drain(f func(T)) {
	q.mu.Lock()
	items := q.items
	q.items = nil
	q.mu.Unlock()
	for _, it := range items {
		f(it.v)
		q.budget.Add(-it.n)
	}
}
