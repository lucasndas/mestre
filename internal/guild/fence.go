package guild

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

// The lease of the lock (docs/guilda/trava-no-github.md#aluguel).
const (
	// LeaseInterval is how often the Citadel checks the lock.
	LeaseInterval = 30 * time.Second
	// LeaseValidity is how long a confirmation is good for, from the moment
	// the check was sent.
	LeaseValidity = 60 * time.Second
	// PromotedWait is how long the machine that won an epoch waits before
	// any external action.
	PromotedWait = 60 * time.Second
)

// Lock is the witness of the epoch outside the Guild
// (docs/guilda/trava-no-github.md): the epoch N holds while the lock shows N
// as its highest, and only one machine wins the creation of N+1. The
// implementation over the GitHub repository of the lock arrives with
// milestone 5; the tests of milestone 2 use the fake of
// internal/testkit/fakelock.
type Lock interface {
	// Check reads the highest epoch the lock shows (one conditional listing
	// of the refs). An error is "GitHub fora": the check failed, which is
	// not the same as seeing a higher epoch.
	Check(ctx context.Context) (uint64, error)
	// Claim tries to create the epoch n for machineID (its own commit, the
	// creation of the ref, and the reading of the ref whatever the answer).
	// won is true only when the ref names this machine's commit; an error
	// means the machine did not win.
	Claim(ctx context.Context, n uint64, machineID string) (won bool, err error)
}

// External is a kind of action outside the Guild.
type External string

// The external actions the fence gates.
const (
	ExternalGitHub   External = "github"
	ExternalTelegram External = "telegram"
)

// FenceState is where the Citadel stands with the lock.
type FenceState string

// The states of the fence (docs/guilda/cidadela-e-torres.md#época-e-fencing).
const (
	// FenceSuspended: at the start, until the first check ends. No external
	// action; what would go out waits in the queue.
	FenceSuspended FenceState = "suspended"
	// FenceWaiting: the machine just won an epoch and waits PromotedWait.
	FenceWaiting FenceState = "waiting"
	// FenceConfirmed: the last check confirmed the epoch. The GitHub needs the
	// lease still valid as well.
	FenceConfirmed FenceState = "confirmed"
	// FenceLockDown: "GitHub fora", a check failed with the lease gone. The
	// Citadel stays in command and on the Telegram; the GitHub waits.
	FenceLockDown FenceState = "lock-down"
	// FenceNoLock: the Guild has no lock (no GitHub): the Telegram goes
	// without it, and there is no GitHub and no promotion.
	FenceNoLock FenceState = "no-lock"
	// FenceDemoted: a higher epoch was seen. Nothing goes out again; the
	// engine becomes a Tower.
	FenceDemoted FenceState = "demoted"
)

// FencedError is why an external action cannot go now.
type FencedError struct {
	State FenceState
	Kind  External
}

func (e *FencedError) Error() string {
	return fmt.Sprintf("guild: no %s action while the Citadel is %s", e.Kind, e.State)
}

// ErrDemoted is returned for an external action asked of a Citadel that saw
// a higher epoch: it is never made.
var ErrDemoted = errors.New("guild: the Citadel was demoted; the action is discarded")

// FenceConfig wires a Fence.
type FenceConfig struct {
	// Lock is the lock of the Guild; nil when the Guild has none.
	Lock  Lock
	Clock clock.Clock
	// Epoch is the epoch this Citadel holds.
	Epoch func() uint64
	// Interval, Validity and Wait default to LeaseInterval, LeaseValidity
	// and PromotedWait.
	Interval, Validity, Wait time.Duration
	// Promoted starts the fence in FenceWaiting: the machine won the epoch,
	// and the wait counts from Run. A Citadel that restarts during the wait
	// sets Wait to what was left of it (WaitLeft).
	Promoted bool
	// OnHigher is told the epoch the lock shows when it is higher than this
	// one: the engine demotes itself to a Tower.
	OnHigher func(epoch uint64)
	// OnChange is told every change of state (the external functions turn on
	// and off here, and the Owner is warned of "GitHub fora").
	OnChange func(FenceState)
	// OnDiscard receives the names of the queued actions dropped at the
	// demotion, for the audit.
	OnDiscard func(names []string)
	Logger    *slog.Logger
}

type held struct {
	kind External
	name string
	fn   func(ctx context.Context) error
}

// Fence keeps the Citadel from acting outside the Guild unless its epoch is
// confirmed: suspended at the start until the first check of the lock, the
// lease of 60 s for the GitHub, the wait of the promoted machine, and the
// demotion when the lock shows a higher epoch. What would go out meanwhile
// waits in a queue, in order.
type Fence struct {
	cfg   FenceConfig
	kick  chan struct{}
	flush chan struct{} // wakes the flusher

	mu     sync.Mutex
	state  FenceState
	sent   clock.Stamp // the last check that confirmed the epoch was sent then
	leased bool
	queue  []held
	err    error // the last failure of a check
}

// NewFence returns the fence of a Citadel that is starting (or, with
// Promoted, of the machine that just won the epoch). Run drives it.
func NewFence(cfg FenceConfig) *Fence {
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Interval <= 0 {
		cfg.Interval = LeaseInterval
	}
	if cfg.Validity <= 0 {
		cfg.Validity = LeaseValidity
	}
	if cfg.Wait <= 0 {
		cfg.Wait = PromotedWait
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	st := FenceSuspended
	switch {
	case cfg.Lock == nil:
		st = FenceNoLock
	case cfg.Promoted:
		st = FenceWaiting
	}
	return &Fence{cfg: cfg, kick: make(chan struct{}, 1), flush: make(chan struct{}, 1), state: st}
}

// State is the state of the fence now.
func (f *Fence) State() FenceState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

// LastError is the failure of the last check, nil after one that worked.
func (f *Fence) LastError() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

// Acting says whether the Citadel acts outside now (at least on the
// Telegram): the external functions are on.
func (f *Fence) Acting() bool { return acting(f.State()) }

func acting(s FenceState) bool {
	return s == FenceConfirmed || s == FenceLockDown || s == FenceNoLock
}

// leaseValid: the lease runs out by the first of the two clocks to pass the
// validity, the monotonic one and the wall one, because the monotonic clock of
// Linux does not count the time the machine slept. A clock that went back
// does not lengthen it either.
func (f *Fence) leaseValidLocked() bool {
	if !f.leased {
		return false
	}
	wall, mono := f.sent.Since(f.cfg.Clock)
	v := f.cfg.Validity
	return wall < v && mono < v && wall >= 0 && mono >= 0
}

// LeaseValid says whether a GitHub call may start now.
func (f *Fence) LeaseValid() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.leaseValidLocked()
}

// Allow says whether an external action of this kind may go now.
func (f *Fence) Allow(kind External) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.allowLocked(kind)
}

func (f *Fence) allowLocked(kind External) error {
	if f.state == FenceDemoted {
		return ErrDemoted
	}
	if !acting(f.state) {
		return &FencedError{State: f.state, Kind: kind}
	}
	if kind == ExternalGitHub && !f.leaseValidLocked() {
		st := f.state
		if st == FenceConfirmed {
			st = FenceLockDown
		}
		return &FencedError{State: st, Kind: kind}
	}
	return nil
}

// Do runs an external action now, if the fence allows it, or keeps it in the
// queue, in order, for when it does. queued says which: a queued action runs
// later on the flusher of Run, with the context of Run, never on the caller.
// A demoted Citadel runs and keeps nothing (ErrDemoted).
func (f *Fence) Do(ctx context.Context, kind External, name string, fn func(ctx context.Context) error) (queued bool, err error) {
	f.mu.Lock()
	err = f.allowLocked(kind)
	if errors.Is(err, ErrDemoted) {
		f.mu.Unlock()
		return false, err
	}
	if err != nil || f.waitingLocked(kind) { // the order of each kind holds
		f.queue = append(f.queue, held{kind: kind, name: name, fn: fn})
		f.mu.Unlock()
		f.wakeFlusher()
		return true, nil
	}
	f.mu.Unlock()
	return false, fn(ctx)
}

func (f *Fence) wakeFlusher() {
	select {
	case f.flush <- struct{}{}:
	default:
	}
}

// Queued is how many actions wait.
func (f *Fence) Queued() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.queue)
}

// Demote stops every external action at once, for good: the engine saw a
// higher epoch (the protocol, the discovery or the lock) and becomes a Tower
// before anything else goes out. The queue is discarded and reported. It
// says whether this call demoted it (false if it already was).
func (f *Fence) Demote() bool {
	f.mu.Lock()
	if f.state == FenceDemoted {
		f.mu.Unlock()
		return false
	}
	f.state = FenceDemoted
	dropped := f.queue
	f.queue = nil
	f.mu.Unlock()
	f.changed(FenceDemoted)
	if len(dropped) > 0 && f.cfg.OnDiscard != nil {
		names := make([]string, len(dropped))
		for i, h := range dropped {
			names[i] = h.name
		}
		f.cfg.OnDiscard(names)
	}
	return true
}

// CheckNow asks for a check outside the cycle of 30 s (the 409 of the
// Telegram, docs/guilda/trava-no-github.md#o-409-do-telegram).
func (f *Fence) CheckNow() {
	select {
	case f.kick <- struct{}{}:
	default:
	}
}

// Run checks the lock: after the wait of a promoted machine, at once, and
// then every Interval, until ctx ends or a higher epoch is seen. The queued
// actions go out on a flusher of their own, so a slow action never holds a
// check of the lock back.
func (f *Fence) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	defer wg.Wait()
	fctx, stop := context.WithCancel(ctx)
	defer stop()
	wg.Go(func() { f.flusher(fctx) })
	f.wakeFlusher()
	if f.cfg.Lock == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	if f.State() == FenceWaiting {
		if err := clock.Sleep(ctx, f.cfg.Clock, f.cfg.Wait); err != nil {
			return err
		}
	}
	t := f.cfg.Clock.NewTicker(f.cfg.Interval)
	defer t.Stop()
	for {
		if f.check(ctx) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C():
		case <-f.kick:
		}
	}
}

// WaitMargin is how long after the wait a restart still counts as inside it:
// a wall clock that stepped forward between the claim and the restart makes
// the time since the claim look longer than it was, and the wait must not
// shrink because of that.
const WaitMargin = 5 * time.Minute

// WaitLeft is what a promoted machine that won the epoch at claimed waits
// when its Citadel restarts: the whole wait if the restart came before the
// wait and WaitMargin passed since the claim (or the clock went back, so the
// time since the claim cannot be trusted), and nothing after that. It is
// never more than the wait.
func WaitLeft(clk clock.Clock, claimed time.Time, wait time.Duration) time.Duration {
	if wait <= 0 {
		wait = PromotedWait
	}
	if clk.Now().Sub(claimed) < wait+WaitMargin {
		return wait
	}
	return 0
}

// check makes one check of the lock and reports whether the fence is done
// (demoted).
func (f *Fence) check(ctx context.Context) bool {
	sent := clock.StampOf(f.cfg.Clock)
	n, err := f.cfg.Lock.Check(ctx)
	if ctx.Err() != nil {
		return false
	}
	epoch := f.cfg.Epoch()
	f.mu.Lock()
	if f.state == FenceDemoted {
		f.mu.Unlock()
		return true
	}
	if err == nil && n > epoch {
		f.mu.Unlock()
		f.cfg.Logger.Warn("guild: the lock shows a higher epoch; the Citadel steps down", "epoch", epoch, "lock", n)
		f.Demote()
		if f.cfg.OnHigher != nil {
			f.cfg.OnHigher(n)
		}
		return true
	}
	before := f.state
	if err == nil && n == epoch {
		f.sent, f.leased, f.err = sent, true, nil
		f.state = FenceConfirmed
	} else {
		if err == nil {
			err = fmt.Errorf("guild: the lock shows epoch %d, not %d", n, epoch)
		}
		f.err = err
		// A promoted machine acts only after a check confirmed its epoch
		// (docs/guilda/trava-no-github.md#espera-do-promovido): a failure
		// keeps it waiting. A Citadel that starts goes on as "GitHub fora".
		if f.state != FenceWaiting && !f.leaseValidLocked() {
			f.state = FenceLockDown
		}
		f.cfg.Logger.Warn("guild: the check of the lock failed", "err", err, "state", f.state)
	}
	after := f.state
	f.mu.Unlock()
	if after != before {
		f.changed(after)
	}
	f.wakeFlusher()
	return false
}

func (f *Fence) waitingLocked(kind External) bool {
	for _, h := range f.queue {
		if h.kind == kind {
			return true
		}
	}
	return false
}

// flusher is the one goroutine that runs the queue: it wakes when an action
// is queued and after every check.
func (f *Fence) flusher(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-f.flush:
			f.flushNow(ctx)
		}
	}
}

// flushNow runs, in order, the queued actions the fence allows now. Each kind
// keeps its order: an action that must wait for the lease holds the actions
// of its kind behind it, not those of the other kind.
func (f *Fence) flushNow(ctx context.Context) {
	for ctx.Err() == nil {
		f.mu.Lock()
		next := -1
		blocked := map[External]bool{}
		for i, h := range f.queue {
			if blocked[h.kind] {
				continue
			}
			if f.allowLocked(h.kind) == nil {
				next = i
				break
			}
			blocked[h.kind] = true
		}
		if next < 0 {
			f.mu.Unlock()
			return
		}
		h := f.queue[next]
		f.queue = append(f.queue[:next:next], f.queue[next+1:]...)
		f.mu.Unlock()
		if err := h.fn(ctx); err != nil {
			f.cfg.Logger.Warn("guild: a queued external action failed", "action", h.name, "err", err)
		}
	}
}

func (f *Fence) changed(s FenceState) {
	if f.cfg.OnChange != nil {
		f.cfg.OnChange(s)
	}
}
