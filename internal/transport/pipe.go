package transport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
)

// Pipe returns two connected in-memory Conns, for tests of the layers above
// (the protocol session) that do not need TLS. It keeps the contract of
// Conn: whole messages, MaxMessageSize, close codes (Close on one end makes
// the other's Read return a *CloseError) and Done. a is the Peer seen by
// the first end, b the one seen by the second.
func Pipe(a, b Peer) (Conn, Conn) {
	q1, q2 := newPipeQueue(), newPipeQueue()
	e1 := &pipeEnd{peer: a, in: q1, out: q2, done: make(chan struct{})}
	e2 := &pipeEnd{peer: b, in: q2, out: q1, done: make(chan struct{})}
	e1.other, e2.other = e2, e1
	return e1, e2
}

type pipeMsg struct {
	typ MessageType
	p   []byte
}

type pipeQueue struct {
	mu     sync.Mutex
	msgs   []pipeMsg
	end    error // set when no more messages will come
	notify chan struct{}
}

func newPipeQueue() *pipeQueue { return &pipeQueue{notify: make(chan struct{}, 1)} }

func (q *pipeQueue) wake() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

func (q *pipeQueue) push(m pipeMsg) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.end != nil {
		return fmt.Errorf("transport: write on a closed pipe: %w", io.ErrClosedPipe)
	}
	q.msgs = append(q.msgs, m)
	q.wake()
	return nil
}

func (q *pipeQueue) stop(err error) {
	q.mu.Lock()
	if q.end == nil {
		q.end = err
	}
	q.mu.Unlock()
	q.wake()
}

type pipeEnd struct {
	peer    Peer
	in, out *pipeQueue
	other   *pipeEnd
	once    sync.Once
	done    chan struct{}
}

func (e *pipeEnd) Peer() Peer            { return e.peer }
func (e *pipeEnd) Done() <-chan struct{} { return e.done }
func (e *pipeEnd) finish()               { e.once.Do(func() { close(e.done) }) }

func (e *pipeEnd) Read(ctx context.Context) (MessageType, []byte, error) {
	for {
		e.in.mu.Lock()
		if len(e.in.msgs) > 0 {
			m := e.in.msgs[0]
			e.in.msgs = e.in.msgs[1:]
			e.in.mu.Unlock()
			return m.typ, m.p, nil
		}
		end := e.in.end
		e.in.mu.Unlock()
		if end != nil {
			e.finish()
			return 0, nil, end
		}
		select {
		case <-e.in.notify:
		case <-ctx.Done():
			// As on the WebSocket: a canceled read drops the connection.
			_ = e.CloseNow()
			return 0, nil, fmt.Errorf("transport: %w", ctx.Err())
		}
	}
}

func (e *pipeEnd) Write(_ context.Context, typ MessageType, p []byte) error {
	if len(p) > MaxMessageSize {
		return fmt.Errorf("%w: %d bytes, limit %d", ErrTooLarge, len(p), MaxMessageSize)
	}
	return e.out.push(pipeMsg{typ: typ, p: bytes.Clone(p)})
}

func (e *pipeEnd) Close(code StatusCode, reason string) error {
	ce := &CloseError{Code: code, Reason: reason}
	e.out.stop(ce)
	e.in.stop(ce)
	e.finish()
	return nil
}

func (e *pipeEnd) CloseNow() error {
	err := fmt.Errorf("transport: connection dropped: %w", io.ErrUnexpectedEOF)
	e.out.stop(err)
	e.in.stop(err)
	e.finish()
	return nil
}
