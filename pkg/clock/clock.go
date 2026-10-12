// Package clock is the only package allowed to read the wall clock or arm
// timers (docs/qualidade/testes.md, docs/qualidade/dubles.md). Everything
// else receives a Clock by parameter, so tests can use the Fake clock or run
// the Real clock inside a testing/synctest bubble.
package clock

import (
	"context"
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
func WithTimeout(ctx context.Context, c Clock, d time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(ctx)
	t := c.AfterFunc(d, func() { cancel(context.DeadlineExceeded) })
	return ctx, func() {
		t.Stop()
		cancel(context.Canceled)
	}
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
