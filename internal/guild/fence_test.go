package guild_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/testkit/fakelock"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// advance moves the time of the bubble and lets everything settle.
func advance(d time.Duration) {
	_ = clock.Sleep(context.Background(), clock.Real{}, d)
	synctest.Wait()
}

// record is what went outside, in order.
type record struct {
	mu  sync.Mutex
	out []string
}

func (r *record) action(name string) func(context.Context) error {
	return func(context.Context) error {
		r.mu.Lock()
		r.out = append(r.out, name)
		r.mu.Unlock()
		return nil
	}
}

func (r *record) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.out)
}

// The fence of CA-CIDADELA-03, which closes in internal/app/githubfence_test.go with the
// comment of GitHub that waits in the queue and goes out once.
// The Citadel acts outside only with its epoch confirmed by the lock: nothing
// before the first check, the GitHub only with the lease of 60 s, the
// Telegram also with the lock unreachable, and nothing ever again once the
// lock shows a higher epoch (docs/guilda/cidadela-e-torres.md#época-e-fencing).
func TestTheFenceLetsTheCitadelActOutsideOnlyWithItsEpochConfirmed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		lock := fakelock.New(3, "a")
		lock.Hold()
		var higher uint64
		var discarded []string
		f := guild.NewFence(guild.FenceConfig{
			Lock: lock.For("a"), Clock: clock.Real{}, Epoch: func() uint64 { return 3 },
			OnHigher: func(n uint64) { higher = n }, OnDiscard: func(names []string) { discarded = names },
		})
		r := &record{}
		for _, a := range []struct {
			kind guild.External
			name string
		}{{guild.ExternalTelegram, "t1"}, {guild.ExternalGitHub, "g1"}} {
			if queued, err := f.Do(ctx, a.kind, a.name, r.action(a.name)); !queued || err != nil {
				t.Fatalf("%s at the start: queued %v, %v; want it kept", a.name, queued, err)
			}
		}
		done := make(chan error, 1)
		go func() { done <- f.Run(ctx) }()
		synctest.Wait()
		if got := r.list(); len(got) != 0 || f.State() != guild.FenceSuspended || f.Acting() {
			t.Fatalf("before the first check: %v went out, state %s", got, f.State())
		}

		// the first check confirms the epoch: the queue goes out, in order
		lock.Release()
		synctest.Wait()
		if got := r.list(); !slices.Equal(got, []string{"t1", "g1"}) || f.State() != guild.FenceConfirmed || !f.LeaseValid() {
			t.Fatalf("after the check: %v, state %s, lease %v", got, f.State(), f.LeaseValid())
		}

		// the lock out of reach: the lease of the check at 0 s holds until
		// 60 s; then the GitHub waits and the Telegram goes on
		lock.SetAllDown(true)
		advance(59 * time.Second)
		if err := f.Allow(guild.ExternalGitHub); err != nil {
			t.Fatalf("the GitHub at 59 s, with the lease of the last check: %v", err)
		}
		advance(time.Second)
		var fenced *guild.FencedError
		if err := f.Allow(guild.ExternalGitHub); !errors.As(err, &fenced) {
			t.Fatalf("the GitHub at 60 s without a confirmation: %v, want it fenced", err)
		}
		if f.Allow(guild.ExternalTelegram) != nil || !f.Acting() {
			t.Fatal("the Telegram stopped with the lock out of reach; it must go on")
		}
		_, _ = f.Do(ctx, guild.ExternalGitHub, "g2", r.action("g2"))
		_, _ = f.Do(ctx, guild.ExternalTelegram, "t2", r.action("t2"))
		if got := r.list(); !slices.Equal(got, []string{"t1", "g1", "t2"}) {
			t.Fatalf("with the lock down: %v; want t2 out and g2 kept", got)
		}

		// the lock back with the same epoch: g2 goes out once
		lock.SetAllDown(false)
		advance(30 * time.Second)
		advance(30 * time.Second)
		if got := r.list(); !slices.Equal(got, []string{"t1", "g1", "t2", "g2"}) {
			t.Fatalf("with the lock back: %v", got)
		}

		// another machine wins the epoch 4 while a check waits and the lease
		// runs out: the action of then is kept, and dropped at the demotion
		lock.Hold()
		advance(60 * time.Second)
		if queued, _ := f.Do(ctx, guild.ExternalGitHub, "g3", r.action("g3")); !queued {
			t.Fatal("a GitHub action without the lease went out")
		}
		lock.Set(4, "b")
		lock.Release()
		synctest.Wait()
		if f.State() != guild.FenceDemoted || higher != 4 {
			t.Fatalf("after the lock showed the epoch 4: state %s, told %d", f.State(), higher)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run = %v", err)
			}
		default:
			t.Error("Run goes on after the demotion")
		}
		if _, err := f.Do(ctx, guild.ExternalTelegram, "t3", r.action("t3")); !errors.Is(err, guild.ErrDemoted) {
			t.Errorf("an action after the demotion: %v, want ErrDemoted", err)
		}
		if got := r.list(); slices.Contains(got, "g3") || slices.Contains(got, "t3") {
			t.Errorf("the demoted Citadel acted: %v", got)
		}
		if !slices.Equal(discarded, []string{"g3"}) {
			t.Errorf("the queue dropped at the demotion: %v, want g3", discarded)
		}
	})
}

// The machine that won an epoch waits 60 s before any external action, and
// then confirms it in the lock (docs/guilda/trava-no-github.md#espera-do-promovido).
func TestThePromotedMachineWaitsSixtySecondsAndChecksTheLockBeforeActing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		lock := fakelock.New(4, "b")
		f := guild.NewFence(guild.FenceConfig{Lock: lock.For("b"), Clock: clock.Real{}, Epoch: func() uint64 { return 4 }, Promoted: true})
		go func() { _ = f.Run(ctx) }()
		r := &record{}
		_, _ = f.Do(ctx, guild.ExternalTelegram, "t1", r.action("t1"))
		advance(59 * time.Second)
		if got := r.list(); len(got) != 0 || f.State() != guild.FenceWaiting || lock.Checks("b") != 0 {
			t.Fatalf("at 59 s: %v out, state %s, %d checks", got, f.State(), lock.Checks("b"))
		}
		advance(time.Second)
		if got := r.list(); !slices.Equal(got, []string{"t1"}) || f.State() != guild.FenceConfirmed || lock.Checks("b") != 1 {
			t.Fatalf("at 60 s: %v out, state %s, %d checks", got, f.State(), lock.Checks("b"))
		}
	})
}

// The promoted machine acts only after a check confirmed its epoch: a first
// check that fails keeps it waiting, never in "GitHub fora"
// (docs/guilda/trava-no-github.md#espera-do-promovido).
func TestThePromotedMachineDoesNotActUntilACheckConfirmsItsEpoch(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		lock := fakelock.New(4, "b")
		lock.SetDown("b", true)
		f := guild.NewFence(guild.FenceConfig{Lock: lock.For("b"), Clock: clock.Real{}, Epoch: func() uint64 { return 4 }, Promoted: true})
		go func() { _ = f.Run(ctx) }()
		r := &record{}
		_, _ = f.Do(ctx, guild.ExternalTelegram, "t1", r.action("t1"))
		advance(60 * time.Second)
		advance(30 * time.Second)
		if got := r.list(); len(got) != 0 || f.State() != guild.FenceWaiting || f.Acting() || lock.Checks("b") != 2 {
			t.Fatalf("with the lock out of reach after the wait: %v out, state %s, %d checks", got, f.State(), lock.Checks("b"))
		}
		lock.SetDown("b", false)
		advance(30 * time.Second)
		if got := r.list(); !slices.Equal(got, []string{"t1"}) || f.State() != guild.FenceConfirmed {
			t.Fatalf("after the check confirmed: %v out, state %s", got, f.State())
		}
	})
}

// A Citadel that restarts soon after the claim waits the whole wait again,
// even if the wall clock stepped forward meanwhile; only one that restarts
// long after the wait (and its margin) waits nothing.
func TestARestartDuringThePromotedWaitWaitsTheWholeWaitAndNeverMore(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))
	claimed := clk.Now()
	clk.Advance(20 * time.Second)
	if got := guild.WaitLeft(clk, claimed, 0); got != guild.PromotedWait {
		t.Errorf("20 s after the claim: %v, want the whole wait", got)
	}
	// the clock stepped forward by 3 minutes while the Citadel was down: the
	// restart still came inside the wait and its margin
	clk.Advance(3 * time.Minute)
	if got := guild.WaitLeft(clk, claimed, 0); got != guild.PromotedWait {
		t.Errorf("after a forward step of the clock: %v, want the whole wait", got)
	}
	if got := guild.WaitLeft(clk, claimed.Add(time.Hour), 0); got != guild.PromotedWait {
		t.Errorf("a claim in the future (the clock went back): %v, want the whole wait", got)
	}
	clk.Advance(time.Hour)
	if got := guild.WaitLeft(clk, claimed, 0); got != 0 {
		t.Errorf("an hour after the claim: %v, want none", got)
	}
}

// The lease runs out by the first of the two clocks to pass 60 s: the wall
// clock that jumped (the machine slept, which the monotonic clock does not
// count) or the monotonic one (the date set back).
func TestTheLeaseRunsOutByTheFirstOfTheTwoClocks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		move func(c *clock.Fake)
	}{
		{"the machine slept 10 min", func(c *clock.Fake) { c.SkipWall(10 * time.Minute) }},
		{"the date went back 10 min and 61 s passed", func(c *clock.Fake) {
			c.Set(c.Now().Add(-10 * time.Minute))
			c.Advance(61 * time.Second)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			clk := clock.NewFake(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))
			lock := fakelock.New(3, "a")
			changed := make(chan guild.FenceState, 8)
			f := guild.NewFence(guild.FenceConfig{Lock: lock.For("a"), Clock: clk, Epoch: func() uint64 { return 3 },
				OnChange: func(s guild.FenceState) { changed <- s }})
			done := make(chan struct{})
			go func() { defer close(done); _ = f.Run(ctx) }()
			if s := <-changed; s != guild.FenceConfirmed {
				t.Fatalf("the first check: %s", s)
			}
			clk.Advance(20 * time.Second)
			if !f.LeaseValid() {
				t.Fatal("the lease is gone 20 s after the check")
			}
			tc.move(clk)
			if f.LeaseValid() || f.Allow(guild.ExternalGitHub) == nil {
				t.Fatal("the lease still holds")
			}
			cancel()
			<-done
		})
	}
}

// A slow queued action never holds the checks of the lock back: the queue
// has a goroutine of its own.
func TestASlowQueuedActionDoesNotHoldTheChecksOfTheLock(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		lock := fakelock.New(3, "a")
		lock.Hold()
		f := guild.NewFence(guild.FenceConfig{Lock: lock.For("a"), Clock: clock.Real{}, Epoch: func() uint64 { return 3 }})
		release := make(chan struct{})
		_, _ = f.Do(ctx, guild.ExternalTelegram, "slow", func(context.Context) error { <-release; return nil })
		go func() { _ = f.Run(ctx) }()
		synctest.Wait()
		lock.Release()
		synctest.Wait() // the first check confirms; the slow action starts and blocks
		advance(30 * time.Second)
		advance(30 * time.Second)
		if n := lock.Checks("a"); n != 3 {
			t.Errorf("%d checks of the lock while a queued action ran, want 3", n)
		}
		if !f.LeaseValid() {
			t.Error("the lease ran out while a queued action ran")
		}
		close(release)
	})
}

// Without a lock (no GitHub in the Guild) the Citadel acts on the Telegram,
// never on the GitHub, and there is no promotion.
func TestWithoutALockTheCitadelActsOnTheTelegramOnlyAndNobodyIsPromoted(t *testing.T) {
	t.Parallel()
	f := guild.NewFence(guild.FenceConfig{Epoch: func() uint64 { return 1 }})
	if f.State() != guild.FenceNoLock || f.Allow(guild.ExternalTelegram) != nil || f.Allow(guild.ExternalGitHub) == nil {
		t.Fatalf("state %s, telegram %v, github %v", f.State(), f.Allow(guild.ExternalTelegram), f.Allow(guild.ExternalGitHub))
	}
	if _, err := guild.ClaimEpoch(t.Context(), nil, 1, "b"); !errors.Is(err, guild.ErrNoLock) {
		t.Fatalf("ClaimEpoch without a lock = %v", err)
	}
}

// Only one machine wins the next epoch; the lock out of reach is no
// promotion (docs/guilda/trava-no-github.md#promoção-pela-trava).
func TestOnlyOneMachineWinsTheNextEpochInTheLock(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	lock := fakelock.New(3, "a")
	n, err := guild.ClaimEpoch(ctx, lock.For("b"), 3, "b")
	if err != nil || n != 4 || lock.Owner(4) != "b" {
		t.Fatalf("b claims: %d, %v (owner %q)", n, err, lock.Owner(4))
	}
	lock.Set(5, "")
	lock.SetDown("c", true)
	if _, err := guild.ClaimEpoch(ctx, lock.For("c"), 3, "c"); err == nil || errors.Is(err, guild.ErrLostLock) {
		t.Fatalf("c with the lock out of reach: %v, want a failure that is not a loss", err)
	}
	lock.SetDown("c", false)
	// c reads 5 and claims 6, and b creates 6 between the two
	race := racing{Lock: lock.For("c"), before: func() { lock.Set(lock.Highest()+1, "b") }}
	if _, err := guild.ClaimEpoch(ctx, race, 3, "c"); !errors.Is(err, guild.ErrLostLock) || lock.Owner(6) != "b" {
		t.Fatalf("c after b: %v (owner %q), want ErrLostLock", err, lock.Owner(6))
	}
}

// racing lets another machine act between the check and the claim.
type racing struct {
	guild.Lock
	before func()
}

func (r racing) Claim(ctx context.Context, n uint64, id string) (bool, error) {
	r.before()
	return r.Lock.Claim(ctx, n, id)
}

type promoApprover struct {
	mu        sync.Mutex
	n         int
	withdrawn []string
	answer    func(id string)
	asked     chan string
}

func (a *promoApprover) AskPromotion(_ context.Context, _ guild.PromotionRequest) (string, error) {
	a.mu.Lock()
	a.n++
	id, answer := "p"+string(rune('0'+a.n)), a.answer
	a.mu.Unlock()
	if answer != nil {
		answer(id)
	}
	a.asked <- id
	return id, nil
}

func (a *promoApprover) WithdrawPromotion(_ context.Context, id string) {
	a.mu.Lock()
	a.withdrawn = append(a.withdrawn, id)
	a.mu.Unlock()
}

func (a *promoApprover) set(f func(string)) {
	a.mu.Lock()
	a.answer = f
	a.mu.Unlock()
}

// Part of CA-CIDADELA-05 (fecha no M7; falta: a Torre condutora, o PIN e os
// botões do Telegram reduzido).
// A promotion needs the Owner's yes; a second request cancels the first.
func TestAPromotionWaitsForTheOwnerAndASecondRequestCancelsTheFirst(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	a := &promoApprover{asked: make(chan string, 8)}
	p := guild.NewPromoter(a)
	yes := func(id string) { _ = p.Decide(id, guild.Decision{Accept: true, ApprovedBy: "dono"}) }
	a.set(yes)
	id, by, err := p.Ask(ctx, guild.PromotionRequest{MachineID: "b"})
	if err != nil || id != "p1" || by != "dono" {
		t.Fatalf("approved: %q %q %v", id, by, err)
	}
	a.set(func(id string) { _ = p.Decide(id, guild.Decision{Accept: false}) })
	if _, _, err := p.Ask(ctx, guild.PromotionRequest{MachineID: "b"}); !errors.Is(err, guild.ErrRefusedByOwner) {
		t.Fatalf("refused: %v", err)
	}
	<-a.asked
	<-a.asked
	a.set(nil)
	first := make(chan error, 1)
	go func() {
		_, _, err := p.Ask(ctx, guild.PromotionRequest{MachineID: "b"})
		first <- err
	}()
	if got := <-a.asked; got != "p3" {
		t.Fatalf("the third question is %s", got)
	}
	a.set(yes)
	if _, _, err := p.Ask(ctx, guild.PromotionRequest{MachineID: "c"}); err != nil {
		t.Fatalf("the second request: %v", err)
	}
	if err := <-first; !errors.Is(err, guild.ErrRefusedByOwner) {
		t.Fatalf("the first request after the second: %v, want it cancelled", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !slices.Contains(a.withdrawn, "p3") {
		t.Errorf("the first question was not withdrawn: %v", a.withdrawn)
	}
}

// Part of CA-CIDADELA-05 (fecha no M7; falta: a Torre condutora, o PIN e os
// botões do Telegram reduzido).
// Only the answer to the exact question counts: another id, given while the
// question waits or while it is being put, changes nothing.
func TestOnlyTheAnswerOfTheExactQuestionPromotes(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	a := &promoApprover{asked: make(chan string, 8)}
	p := guild.NewPromoter(a)
	// while the question is put, a wrong id and then a no for it
	a.set(func(id string) {
		_ = p.Decide("other", guild.Decision{Accept: true, ApprovedBy: "x"})
	})
	res := make(chan error, 1)
	go func() {
		_, _, err := p.Ask(ctx, guild.PromotionRequest{MachineID: "b"})
		res <- err
	}()
	id := <-a.asked
	if err := p.Decide(id+"x", guild.Decision{Accept: true}); !errors.Is(err, guild.ErrNotPending) {
		t.Fatalf("an answer for another id: %v, want ErrNotPending", err)
	}
	select {
	case err := <-res:
		t.Fatalf("the request ended with an answer for another id: %v", err)
	default:
	}
	if err := p.Decide(id, guild.Decision{Accept: true, ApprovedBy: "dono"}); err != nil {
		t.Fatal(err)
	}
	if err := <-res; err != nil {
		t.Fatalf("the exact answer: %v", err)
	}
}

// Part of CA-CIDADELA-05 (fecha no M7; falta: o diagnóstico e os botões do
// Telegram reduzido).
// The Owner hears of the Citadel away after 15 min and every hour after, and
// not again once it is back (docs/guilda/cidadela-e-torres.md#recuperação-e-aviso-ao-senhor-da-guilda).
func TestTheOwnerIsWarnedOfTheCitadelAwayAfterFifteenMinutesAndEveryHour(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var (
			mu      sync.Mutex
			notices []int
			back    int
		)
		seen := func() ([]int, int) {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(notices), back
		}
		w := guild.NewDownWatch(guild.DownWatchConfig{Clock: clock.Real{},
			OnDown: func(_ time.Time, n int) { mu.Lock(); notices = append(notices, n); mu.Unlock() },
			OnBack: func(time.Time) { mu.Lock(); back++; mu.Unlock() }})
		w.Lost()
		advance(15*time.Minute - time.Second)
		if got, _ := seen(); len(got) != 0 {
			t.Fatalf("a notice before 15 min: %v", got)
		}
		advance(time.Second)
		advance(time.Hour)
		if got, _ := seen(); !slices.Equal(got, []int{1, 2}) {
			t.Fatalf("notices = %v, want the first at 15 min and another 1 h later", got)
		}
		w.Back()
		advance(2 * time.Hour)
		if got, b := seen(); len(got) != 2 || b != 1 {
			t.Fatalf("after the Citadel came back: notices %v, back %d", got, b)
		}
	})
}
