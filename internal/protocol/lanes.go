package protocol

import (
	"context"
	"errors"
	"sync"
)

// Defaults of the writer (docs/conectividade/protocolo.md#multiplexação-e-prioridade).
const (
	// DataBudget is the most data the writer sends between two looks at the
	// control queue. A frame bigger than it (a part of a transfer) goes
	// whole, alone in its turn.
	DataBudget = 64 << 10
	// DataQueueLimit bounds the data waiting to be written; a producer that
	// goes over it waits, so a noisy terminal does not fill the memory.
	DataQueueLimit = 1 << 20
)

// ErrSessionClosed is returned when a frame is queued after the connection
// ended.
var ErrSessionClosed = errors.New("protocol: the connection is closed")

// errLocalClose ends the writer after it sent the close frame.
var errLocalClose = errors.New("protocol: closed locally")

type outItem struct {
	frame  Frame
	close  bool
	code   int
	reason string
}

// lanes is the writer of one connection: a control queue, always emptied
// first, and a data queue, of which at most DataBudget bytes go out between
// two looks at the control queue. So a terminal that prints without pause
// does not delay a heartbeat or a command.
type lanes struct {
	budget int
	limit  int

	mu        sync.Mutex
	cond      *sync.Cond
	ctrl      []outItem
	data      []Frame
	dataBytes int
	err       error // set when the writer stops
}

func newLanes() *lanes {
	l := &lanes{budget: DataBudget, limit: DataQueueLimit}
	l.cond = sync.NewCond(&l.mu)
	return l
}

func (l *lanes) wake() {
	l.mu.Lock()
	l.cond.Broadcast()
	l.mu.Unlock()
}

// pushControl queues a control frame.
func (l *lanes) pushControl(f Frame) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return ErrSessionClosed
	}
	l.ctrl = append(l.ctrl, outItem{frame: f})
	l.cond.Broadcast()
	return nil
}

// pushClose queues the close of the connection after everything queued in
// the control lane before it.
func (l *lanes) pushClose(code int, reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return
	}
	l.ctrl = append(l.ctrl, outItem{close: true, code: code, reason: reason})
	l.cond.Broadcast()
}

// pushData queues a data frame, waiting while the queue is over its limit.
func (l *lanes) pushData(ctx context.Context, f Frame) error {
	stop := context.AfterFunc(ctx, l.wake)
	defer stop()
	l.mu.Lock()
	defer l.mu.Unlock()
	for l.dataBytes >= l.limit && l.err == nil && ctx.Err() == nil {
		l.cond.Wait()
	}
	if l.err != nil {
		return ErrSessionClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	l.data = append(l.data, f)
	l.dataBytes += len(f.Data)
	l.cond.Broadcast()
	return nil
}

// stop makes the writer return and every producer fail.
func (l *lanes) stop(err error) {
	l.mu.Lock()
	if l.err == nil {
		l.err = err
	}
	l.cond.Broadcast()
	l.mu.Unlock()
}

// run writes to conn until stop, an error, or a queued close; it returns the
// cause.
func (l *lanes) run(ctx context.Context, conn Conn) error {
	defer context.AfterFunc(ctx, l.wake)()
	for {
		l.mu.Lock()
		for len(l.ctrl) == 0 && len(l.data) == 0 && l.err == nil && ctx.Err() == nil {
			l.cond.Wait()
		}
		if l.err != nil {
			err := l.err
			l.mu.Unlock()
			return err
		}
		if err := ctx.Err(); err != nil {
			l.mu.Unlock()
			l.stop(err)
			return err
		}
		var batch []outItem
		if len(l.ctrl) > 0 {
			batch, l.ctrl = l.ctrl, nil
		} else {
			sent := 0
			for len(l.data) > 0 && (sent == 0 || sent+len(l.data[0].Data) <= l.budget) {
				f := l.data[0]
				l.data = l.data[1:]
				sent += len(f.Data)
				batch = append(batch, outItem{frame: f})
			}
			l.dataBytes -= sent
			l.cond.Broadcast()
		}
		l.mu.Unlock()

		for _, it := range batch {
			if it.close {
				_ = conn.Close(it.code, it.reason)
				l.stop(errLocalClose)
				return errLocalClose
			}
			if err := conn.Write(ctx, it.frame.Kind, it.frame.Data); err != nil {
				l.stop(err)
				return err
			}
		}
	}
}
