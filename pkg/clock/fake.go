package clock

import (
	"sort"
	"sync"
	"time"
)

// Fake is a manually driven Clock for tests that need calendar dates
// (docs/qualidade/dubles.md). Time only moves with Advance or Set. Timers
// fire in deadline order; AfterFunc callbacks run synchronously inside
// Advance/Set, without the clock lock held, so they may use the clock.
type Fake struct {
	mu      sync.Mutex
	cond    *sync.Cond
	now     time.Time
	waiters []*fakeWaiter
	seq     uint64
}

var _ Clock = (*Fake)(nil)

// NewFake returns a Fake clock set to t.
func NewFake(t time.Time) *Fake {
	f := &Fake{now: t}
	f.cond = sync.NewCond(&f.mu)
	return f
}

type fakeWaiter struct {
	clock    *Fake
	deadline time.Time
	period   time.Duration // > 0 for tickers
	seq      uint64        // arming order breaks ties between equal deadlines
	ch       chan time.Time
	fn       func()
	active   bool
}

// Now returns the fake current time.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// NewTimer arms a one-shot timer.
func (f *Fake) NewTimer(d time.Duration) Timer {
	w := &fakeWaiter{clock: f, ch: make(chan time.Time, 1)}
	f.arm(w, d)
	return fakeTimer{w}
}

// AfterFunc arms a one-shot callback.
func (f *Fake) AfterFunc(d time.Duration, fn func()) Timer {
	w := &fakeWaiter{clock: f, fn: fn}
	f.arm(w, d)
	return fakeTimer{w}
}

// NewTicker arms a periodic ticker. It panics if d <= 0, like time.NewTicker.
func (f *Fake) NewTicker(d time.Duration) Ticker {
	if d <= 0 {
		panic("clock: non-positive interval for NewTicker")
	}
	w := &fakeWaiter{clock: f, ch: make(chan time.Time, 1), period: d}
	f.arm(w, d)
	return fakeTicker{w}
}

func (f *Fake) arm(w *fakeWaiter, d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.armLocked(w, d)
}

func (f *Fake) armLocked(w *fakeWaiter, d time.Duration) {
	f.seq++
	w.seq = f.seq
	w.deadline = f.now.Add(d)
	if !w.active {
		w.active = true
		f.waiters = append(f.waiters, w)
	}
	f.cond.Broadcast()
}

func (f *Fake) disarmLocked(w *fakeWaiter) bool {
	if !w.active {
		return false
	}
	w.active = false
	for i, x := range f.waiters {
		if x == w {
			f.waiters = append(f.waiters[:i], f.waiters[i+1:]...)
			break
		}
	}
	return true
}

// Advance moves the clock forward by d, firing every timer due on the way.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	target := f.now.Add(d)
	f.mu.Unlock()
	f.Set(target)
}

// Set moves the clock to t. Moving forward fires due timers in deadline
// order; moving backward fires nothing.
func (f *Fake) Set(t time.Time) {
	for {
		f.mu.Lock()
		w := f.nextDueLocked(t)
		if w == nil {
			f.now = t
			f.mu.Unlock()
			return
		}
		if w.deadline.After(f.now) {
			f.now = w.deadline
		}
		now := f.now
		fn := w.fn
		if w.period > 0 {
			w.deadline = w.deadline.Add(w.period)
		} else {
			f.disarmLocked(w)
		}
		if w.ch != nil {
			select {
			case w.ch <- now:
			default: // like time.Ticker, drop ticks nobody read
			}
		}
		f.mu.Unlock()
		if fn != nil {
			fn()
		}
	}
}

func (f *Fake) nextDueLocked(limit time.Time) *fakeWaiter {
	due := make([]*fakeWaiter, 0, len(f.waiters))
	for _, w := range f.waiters {
		if !w.deadline.After(limit) {
			due = append(due, w)
		}
	}
	if len(due) == 0 {
		return nil
	}
	sort.Slice(due, func(i, j int) bool {
		if !due[i].deadline.Equal(due[j].deadline) {
			return due[i].deadline.Before(due[j].deadline)
		}
		return due[i].seq < due[j].seq
	})
	return due[0]
}

// Armed returns how many timers, tickers and callbacks are armed.
func (f *Fake) Armed() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.waiters)
}

// BlockUntil waits until at least n timers are armed, so a test can advance
// the clock only after the code under test armed its timers.
func (f *Fake) BlockUntil(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for len(f.waiters) < n {
		f.cond.Wait()
	}
}

type fakeTimer struct{ w *fakeWaiter }

func (t fakeTimer) C() <-chan time.Time { return t.w.ch }

func (t fakeTimer) Stop() bool {
	f := t.w.clock
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.disarmLocked(t.w)
}

func (t fakeTimer) Reset(d time.Duration) bool {
	f := t.w.clock
	f.mu.Lock()
	defer f.mu.Unlock()
	was := t.w.active
	f.armLocked(t.w, d)
	return was
}

type fakeTicker struct{ w *fakeWaiter }

func (t fakeTicker) C() <-chan time.Time { return t.w.ch }

func (t fakeTicker) Stop() {
	f := t.w.clock
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disarmLocked(t.w)
}

func (t fakeTicker) Reset(d time.Duration) {
	if d <= 0 {
		panic("clock: non-positive interval for Ticker.Reset")
	}
	f := t.w.clock
	f.mu.Lock()
	defer f.mu.Unlock()
	t.w.period = d
	f.armLocked(t.w, d)
}
