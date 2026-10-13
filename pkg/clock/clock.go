// Package clock is the only package allowed to read the wall clock or arm
// timers (docs/qualidade/testes.md, docs/qualidade/dubles.md). Everything
// else receives a Clock by parameter, so tests can use the Fake clock or run
// the Real clock inside a testing/synctest bubble.
package clock

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Clock is the injectable source of time.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
	NewTicker(d time.Duration) Ticker
	// AfterFunc calls f once d has elapsed. The returned Timer has a nil C.
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer mirrors *time.Timer.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
	Reset(d time.Duration) bool
}

// Ticker mirrors *time.Ticker.
type Ticker interface {
	C() <-chan time.Time
	Stop()
	Reset(d time.Duration)
}

// Since returns the time elapsed since t according to c.
func Since(c Clock, t time.Time) time.Duration { return c.Now().Sub(t) }

// Sleep blocks for d according to c, or until ctx is done.
func Sleep(ctx context.Context, c Clock, d time.Duration) error {
	t := c.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WithTimeout is context.WithTimeout driven by c instead of the wall clock.
// Like the standard one, the context reports context.DeadlineExceeded from
// Err once d has elapsed on c (and as its cause), and Deadline is the time of
// c d from now, or the parent's deadline when that is earlier. Cancelling
// through the returned function reports context.Canceled.
func WithTimeout(parent context.Context, c Clock, d time.Duration) (context.Context, context.CancelFunc) {
	inner, cancelCause := context.WithCancelCause(parent)
	ctx := &timeoutCtx{Context: inner, deadline: c.Now().Add(d), done: make(chan struct{})}
	if pd, ok := parent.Deadline(); ok && pd.Before(ctx.deadline) {
		ctx.deadline = pd
	}
	// The wrapper has its own Done channel, so contexts derived from it ask
	// it for Err and get DeadlineExceeded; inner stays the source of Value
	// and of the cause.
	stopAfter := context.AfterFunc(inner, ctx.finish)
	expire := func() {
		cancelCause(context.DeadlineExceeded)
		ctx.finish()
	}
	if d <= 0 {
		expire()
		return ctx, func() {}
	}
	t := c.AfterFunc(d, expire)
	return ctx, func() {
		t.Stop()
		stopAfter()
		cancelCause(context.Canceled)
		ctx.finish()
	}
}

type timeoutCtx struct {
	context.Context // cancellable parent: Value, and the cause for context.Cause
	deadline        time.Time
	done            chan struct{}
	once            sync.Once
}

func (t *timeoutCtx) finish() { t.once.Do(func() { close(t.done) }) }

func (t *timeoutCtx) Deadline() (time.Time, bool) { return t.deadline, true }

func (t *timeoutCtx) Done() <-chan struct{} { return t.done }

func (t *timeoutCtx) Err() error {
	err := t.Context.Err()
	if err != nil && errors.Is(context.Cause(t.Context), context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return err
}

// Real is the Clock backed by package time.
type Real struct{}

var _ Clock = Real{}

func (Real) Now() time.Time { return time.Now() }

func (Real) NewTimer(d time.Duration) Timer { return realTimer{time.NewTimer(d)} }

func (Real) NewTicker(d time.Duration) Ticker { return realTicker{time.NewTicker(d)} }

func (Real) AfterFunc(d time.Duration, f func()) Timer { return realTimer{time.AfterFunc(d, f)} }

type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time        { return r.t.C }
func (r realTimer) Stop() bool                 { return r.t.Stop() }
func (r realTimer) Reset(d time.Duration) bool { return r.t.Reset(d) }

type realTicker struct{ t *time.Ticker }

func (r realTicker) C() <-chan time.Time   { return r.t.C }
func (r realTicker) Stop()                 { r.t.Stop() }
func (r realTicker) Reset(d time.Duration) { r.t.Reset(d) }
