package github_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/telegram"
	"github.com/mestre-ia/mestre/internal/testkit/fakegithub"
	"github.com/mestre-ia/mestre/internal/testkit/fakenet"
	"github.com/mestre-ia/mestre/internal/testkit/faketelegram"
	"github.com/mestre-ia/mestre/pkg/clock"
)

const (
	lairRepo  = "lucas/mestre-guilda-abc12345"
	lairToken = "ghs_lock-token"
)

// lockRig is the GitHub falso with the repository of the lock and the ref of
// the epoch 3, which the machine "a" made.
type lockRig struct {
	gh *fakegithub.Server
	hc *http.Client
}

func newLockRig(t *testing.T, clk clock.Clock) *lockRig {
	t.Helper()
	gh, err := fakegithub.New(clk, t)
	if err != nil {
		t.Fatal(err)
	}
	gh.AddRepo("lucas", "mestre-guilda-abc12345")
	gh.IssueToken(lairToken, "grand_master")
	gh.AddRef(lairRepo, "refs/mestre/epoca-3", "machine a, epoch 3")
	return &lockRig{gh: gh, hc: &http.Client{Transport: fakenet.HandlerTransport(gh)}}
}

func (r *lockRig) lock(clk clock.Clock, hc *http.Client) *github.LairLock {
	if hc == nil {
		hc = r.hc
	}
	return &github.LairLock{
		Repo:    func(context.Context) (string, error) { return lairRepo, nil },
		Token:   func(context.Context) (string, error) { return lairToken, nil },
		GuildID: "01GUILD", BaseURL: "https://api.github.com", HTTP: hc, Clock: clk,
	}
}

// listings are the requests that listed the refs.
func (r *lockRig) listings() []fakegithub.Request {
	var out []fakegithub.Request
	for _, q := range r.gh.Requests() {
		if q.Method == http.MethodGet && strings.Contains(q.Path, "/git/matching-refs/") {
			out = append(out, q)
		}
	}
	return out
}

// between runs a hook after the first listing of the refs is answered and before
// the caller reads it: the other machine's claim lands in between.
type between struct {
	rt   http.RoundTripper
	once sync.Once
	hook func()
}

func (b *between) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := b.rt.RoundTrip(req)
	if err == nil && strings.Contains(req.URL.Path, "/git/matching-refs/") {
		b.once.Do(b.hook)
	}
	return resp, err
}

func advance(d time.Duration) {
	_ = clock.Sleep(context.Background(), clock.Real{}, d)
	synctest.Wait()
}

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

func (r *record) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.out)
}

// CA-TRAVA-01
func TestOnlyTheMachineNamedByTheRefWinsTheEpoch(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		clk := clock.Real{}
		rig := newLockRig(t, clk)
		ctx := context.Background()
		b := rig.lock(clk, nil)
		// c lists the refs (highest 3), then b makes its whole claim, then c goes on
		c := rig.lock(clk, &http.Client{Transport: &between{rt: fakenet.HandlerTransport(rig.gh), hook: func() {
			if won, err := b.Claim(ctx, 4, "b"); err != nil || !won {
				t.Errorf("b claim = %v, %v", won, err)
			}
		}}})
		won, err := c.Claim(ctx, 4, "c")
		if err != nil || won {
			t.Fatalf("c claim = %v, %v; want a defeat, not an error", won, err)
		}
		var refused int
		for _, q := range rig.gh.Requests() {
			if q.Method == http.MethodPost && strings.HasSuffix(q.Path, "/git/refs") && q.Status == http.StatusUnprocessableEntity {
				refused++
			}
		}
		if refused != 1 {
			t.Fatalf("%d creations refused with 422, want c's one", refused)
		}
		if n, err := b.Check(ctx); err != nil || n != 4 {
			t.Fatalf("the lock shows %d, %v; want 4", n, err)
		}
		// the loser steps down at its next check, with no action outside
		before := len(rig.gh.Actions())
		f := guild.NewFence(guild.FenceConfig{Lock: c, Clock: clk, Epoch: func() uint64 { return 3 }})
		go func() { _ = f.Run(ctx) }()
		synctest.Wait()
		if f.State() != guild.FenceDemoted {
			t.Fatalf("the loser is %s, want demoted", f.State())
		}
		if queued, err := f.Do(ctx, guild.ExternalGitHub, "comment", func(context.Context) error { return nil }); queued || !errors.Is(err, guild.ErrDemoted) {
			t.Fatalf("an action of the loser: queued %v, %v", queued, err)
		}
		if len(rig.gh.Actions()) != before {
			t.Fatal("the loser made a write on GitHub")
		}
	})
}

// CA-TRAVA-02
func TestTheLeaseIsRenewedByAConditionalListingEvery30SecondsAndNothingElseLists(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		clk := clock.Real{}
		rig := newLockRig(t, clk)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f := guild.NewFence(guild.FenceConfig{Lock: rig.lock(clk, nil), Clock: clk, Epoch: func() uint64 { return 3 }})
		go func() { _ = f.Run(ctx) }()
		synctest.Wait()
		if f.State() != guild.FenceConfirmed || len(rig.listings()) != 1 {
			t.Fatalf("after the first check: %s, %d listings", f.State(), len(rig.listings()))
		}
		advance(20 * time.Second)
		r := &record{}
		for i := range 10 {
			if queued, err := f.Do(ctx, guild.ExternalGitHub, "a", r.action(string(rune('a'+i)))); queued || err != nil {
				t.Fatalf("action %d: queued %v, %v", i, queued, err)
			}
		}
		if r.count() != 10 || len(rig.listings()) != 1 {
			t.Fatalf("%d actions out, %d listings; want ten actions and no listing of their own", r.count(), len(rig.listings()))
		}
		used := rig.gh.Used(lairToken)
		advance(10 * time.Second)
		ls := rig.listings()
		if len(ls) != 2 || !ls[1].IfNoneMatch || ls[1].Status != http.StatusNotModified {
			t.Fatalf("listings = %+v; want the second conditional and answered 304", ls)
		}
		if rig.gh.Used(lairToken) != used {
			t.Fatal("the 304 spent quota")
		}
		// the lease runs 60 s from the sending of the check at 30 s: with GitHub
		// out, no later check renews it, so it is valid at 59 s and gone at 60 s
		rig.gh.DownGit(http.StatusServiceUnavailable)
		advance(59 * time.Second)
		if !f.LeaseValid() {
			t.Fatal("the lease is gone 59 s after the check")
		}
		advance(time.Second)
		if f.LeaseValid() {
			t.Fatal("the lease is still valid 60 s after the check that renewed it")
		}
	})
	// the wall clock that jumped (the machine slept) ends the lease
	t.Run("the machine slept", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
		rig := newLockRig(t, clk)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		changed := make(chan guild.FenceState, 4)
		f := guild.NewFence(guild.FenceConfig{Lock: rig.lock(clk, nil), Clock: clk, Epoch: func() uint64 { return 3 }, OnChange: func(s guild.FenceState) { changed <- s }})
		done := make(chan struct{})
		go func() { defer close(done); _ = f.Run(ctx) }()
		if s := <-changed; s != guild.FenceConfirmed {
			t.Fatalf("first check: %s", s)
		}
		clk.SkipWall(10 * time.Minute)
		if f.LeaseValid() {
			t.Fatal("the lease survived the wall clock jumping 10 min")
		}
		cancel()
		<-done
	})
}

// CA-TRAVA-03
func TestAHigherEpochSuspendsTheCitadelAtOnceAndNothingGoesOutAfterIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		clk := clock.Real{}
		rig := newLockRig(t, clk)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var higher uint64
		f := guild.NewFence(guild.FenceConfig{Lock: rig.lock(clk, nil), Clock: clk, Epoch: func() uint64 { return 3 }, OnHigher: func(n uint64) { higher = n }})
		go func() { _ = f.Run(ctx) }()
		synctest.Wait()
		rig.gh.AddRef(lairRepo, "refs/mestre/epoca-4", "machine b, epoch 4")
		advance(30 * time.Second)
		if f.State() != guild.FenceDemoted || higher != 4 {
			t.Fatalf("state %s, higher %d; want demoted by 4", f.State(), higher)
		}
		calls := len(rig.gh.Requests())
		for _, k := range []guild.External{guild.ExternalGitHub, guild.ExternalTelegram} {
			if err := f.Allow(k); !errors.Is(err, guild.ErrDemoted) {
				t.Fatalf("%s after the demotion: %v", k, err)
			}
		}
		advance(5 * time.Minute)
		if len(rig.gh.Requests()) != calls {
			t.Fatal("a call to GitHub after the demotion")
		}
	})
}

// CA-TRAVA-05
func TestWithGitHubOutTheCitadelKeepsTheTelegramQueuesTheGitHubAndTellsTheOwnerOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		clk := clock.Real{}
		rig := newLockRig(t, clk)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var told []string
		var mu sync.Mutex
		var f *guild.Fence
		notice := &guild.DownNotice{Clock: clk, Queued: func() int { return f.Queued() }, LastError: func() error { return f.LastError() },
			Tell: func(_ context.Context, text string) { mu.Lock(); told = append(told, text); mu.Unlock() }}
		f = guild.NewFence(guild.FenceConfig{Lock: rig.lock(clk, nil), Clock: clk, Epoch: func() uint64 { return 3 }, OnChange: notice.Changed})
		go func() { _ = f.Run(ctx) }()
		synctest.Wait()
		rig.gh.DownGit(http.StatusServiceUnavailable)
		advance(61 * time.Second) // the lease of the last check is gone
		r := &record{}
		telegram, err := f.Do(ctx, guild.ExternalTelegram, "reply", r.action("telegram reply"))
		github, err2 := f.Do(ctx, guild.ExternalGitHub, "comment", r.action("github comment"))
		if telegram || err != nil || !github || err2 != nil {
			t.Fatalf("telegram queued %v (%v), github queued %v (%v)", telegram, err, github, err2)
		}
		advance(60 * time.Second) // more failed checks: still one notice
		mu.Lock()
		down := len(told)
		mu.Unlock()
		if down != 1 || !notice.Down() || !strings.Contains(told[0], "GitHub fora") || !strings.Contains(told[0], "503") {
			t.Fatalf("notices = %q", told)
		}
		if r.count() != 1 {
			t.Fatalf("%d actions out while GitHub is out, want the Telegram reply only", r.count())
		}
		rig.gh.UpGit()
		advance(30 * time.Second)
		mu.Lock()
		defer mu.Unlock()
		if len(told) != 2 || !strings.Contains(told[1], "GitHub de volta") || r.count() != 2 || notice.Down() {
			t.Fatalf("after it came back: notices %q, %d actions out", told, r.count())
		}
	})
}

// CA-TRAVA-06
func TestThePromotedMachineWaits60SecondsBeforeAnyActionAndThenChecksTheLock(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		clk := clock.Real{}
		rig := newLockRig(t, clk)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		l := rig.lock(clk, nil)
		if won, err := l.Claim(ctx, 4, "b"); err != nil || !won {
			t.Fatalf("claim = %v, %v", won, err)
		}
		claimed := len(rig.gh.Requests())
		// the steps after the wait, in the order they happen: the check of the
		// lock (a listing of the refs), the reconciliation, and the first read of
		// the bot (the Telegram turned on by the fence)
		var mu sync.Mutex
		var steps []string
		step := func(s string) { mu.Lock(); steps = append(steps, s); mu.Unlock() }
		reconcileFails := true
		// the Telegram double: the first read of the bot is a getUpdates that reaches it
		tg, err := faketelegram.New(clk, t, "123456:fake-bot-token")
		if err != nil {
			t.Fatal(err)
		}
		bot := &telegram.Client{Token: "123456:fake-bot-token", HTTP: &http.Client{Transport: fakenet.HandlerTransport(tg)}}
		var f *guild.Fence
		f = guild.NewFence(guild.FenceConfig{Lock: l, Clock: clk, Epoch: func() uint64 { return 4 }, Promoted: true,
			Reconcile: func(context.Context) error {
				step(fmt.Sprintf("lock checked: %d listings", len(rig.listings())))
				if f.Allow(guild.ExternalTelegram) == nil || f.Allow(guild.ExternalGitHub) == nil {
					t.Error("the fence allowed an action while it reconciled")
				}
				step("reconcile")
				if reconcileFails {
					reconcileFails = false
					return errors.New("github: 502")
				}
				return nil
			},
			OnChange: func(s guild.FenceState) {
				if s == guild.FenceConfirmed {
					// the bot asks the gate of the fence before each call to the Telegram
					if !f.Gate(guild.ExternalTelegram).Allowed(ctx) {
						t.Error("the gate of the Telegram is closed when the fence confirmed")
					}
					if _, err := bot.Call(ctx, "getUpdates", map[string]any{"timeout": 0}); err != nil {
						t.Errorf("getUpdates: %v", err)
					}
					step(fmt.Sprintf("bot read: %d getUpdates", tg.Calls("getUpdates")))
				}
			}})
		go func() { _ = f.Run(ctx) }()
		synctest.Wait()
		advance(59 * time.Second)
		for _, k := range []guild.External{guild.ExternalGitHub, guild.ExternalTelegram} {
			if err := f.Allow(k); err == nil {
				t.Fatalf("%s allowed at 59 s", k)
			}
		}
		if len(rig.gh.Requests()) != claimed {
			t.Fatal("a request to GitHub during the wait")
		}
		mu.Lock()
		if len(steps) != 0 {
			t.Fatalf("steps during the wait: %v", steps)
		}
		mu.Unlock()
		advance(time.Second)
		// at 60 s: the lock is checked and the reconciliation runs (and fails
		// here): the Telegram and the GitHub stay off, the fence keeps waiting
		if len(rig.gh.Requests()) != claimed+1 || f.State() != guild.FenceWaiting || f.Allow(guild.ExternalTelegram) == nil || f.Allow(guild.ExternalGitHub) == nil {
			t.Fatalf("at 60 s with the reconciliation failing: %d requests (was %d), state %s", len(rig.gh.Requests()), claimed, f.State())
		}
		if n := tg.Calls("getUpdates") + tg.LongPolls(); n != 0 {
			t.Fatalf("the Telegram was called %d times before the reconciliation worked", n)
		}
		// the next check (30 s later) reconciles again and only then the bot is read
		advance(30 * time.Second)
		if f.State() != guild.FenceConfirmed || f.Allow(guild.ExternalTelegram) != nil || f.Allow(guild.ExternalGitHub) != nil {
			t.Fatalf("after the reconciliation worked: state %s", f.State())
		}
		mu.Lock()
		defer mu.Unlock()
		// the listing of the claim is the first one
		want := []string{"lock checked: 2 listings", "reconcile", "lock checked: 3 listings", "reconcile", "bot read: 1 getUpdates"}
		if strings.Join(steps, "|") != strings.Join(want, "|") {
			t.Fatalf("steps = %q, want %q", steps, want)
		}
	})
}

func TestTheClaimIsOnlyForTheNextEpochAndStartsFromTheDefaultBranch(t *testing.T) {
	t.Parallel()
	clk := clock.Real{}
	rig := newLockRig(t, clk)
	rig.gh.SetDefaultBranch(lairRepo, "trunk")
	l := rig.lock(clk, nil)
	ctx := context.Background()
	if won, err := l.Claim(ctx, 5, "b"); err != nil || won {
		t.Fatalf("a claim that skips an epoch = %v, %v; want a defeat", won, err)
	}
	if won, err := l.Claim(ctx, 3, "b"); err != nil || won {
		t.Fatalf("a claim of an epoch already there = %v, %v; want a defeat", won, err)
	}
	if won, err := l.Claim(ctx, 4, "b"); err != nil || !won {
		t.Fatalf("the next epoch on a repository whose branch is trunk = %v, %v", won, err)
	}
}

func TestAClaimWhoseAnswerWasLostStillWinsByReadingTheRef(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		clk := clock.Real{}
		rig := newLockRig(t, clk)
		rig.gh.LoseNextRefAnswer()
		won, err := rig.lock(clk, nil).Claim(context.Background(), 4, "b")
		if err != nil || !won {
			t.Fatalf("claim = %v, %v; want the win the ref shows", won, err)
		}
	})
}

func TestTheLockListsEveryPageAndNeverTrustsTheETagOfTheFirstOne(t *testing.T) {
	t.Parallel()
	clk := clock.Real{}
	rig := newLockRig(t, clk)
	rig.gh.SetPageSize(2)
	for n := 4; n <= 6; n++ {
		rig.gh.AddRef(lairRepo, "refs/mestre/epoca-"+string(rune('0'+n)), "epoch")
	}
	l := rig.lock(clk, nil)
	ctx := context.Background()
	if n, err := l.Check(ctx); err != nil || n != 6 {
		t.Fatalf("check = %d, %v; want 6 over two pages", n, err)
	}
	rig.gh.AddRef(lairRepo, "refs/mestre/epoca-7", "epoch")
	if n, err := l.Check(ctx); err != nil || n != 7 {
		t.Fatalf("check = %d, %v; want 7 (a new ref must not hide behind the 304 of the first page)", n, err)
	}
}

// The reconciliation of a promoted machine has the time of a lease, by the clock
// of the fence, and a demotion cancels it (Part of CA-TRAVA-06).
func TestTheReconciliationEndsAtTheLeaseAndIsCancelledByTheDemotion(t *testing.T) {
	t.Parallel()
	run := func(t *testing.T, stop func(f *guild.Fence)) (f *guild.Fence, ended error) {
		t.Helper()
		clk := clock.Real{}
		rig := newLockRig(t, clk)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		l := rig.lock(clk, nil)
		if won, err := l.Claim(ctx, 4, "b"); err != nil || !won {
			t.Fatalf("claim = %v, %v", won, err)
		}
		done := make(chan error, 1)
		started := make(chan struct{})
		var once sync.Once
		f = guild.NewFence(guild.FenceConfig{Lock: l, Clock: clk, Epoch: func() uint64 { return 4 }, Promoted: true,
			Reconcile: func(rctx context.Context) error {
				once.Do(func() { close(started) })
				<-rctx.Done() // a reconciliation that never ends by itself
				select {
				case done <- rctx.Err():
				default: // a later try, after the first one ended
				}
				return rctx.Err()
			}})
		go func() { _ = f.Run(ctx) }()
		advance(guild.PromotedWait)
		<-started
		stop(f)
		synctest.Wait()
		select {
		case err := <-done:
			return f, err
		default:
			t.Fatal("the reconciliation was not ended")
			return f, nil
		}
	}
	t.Run("the lease", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f, err := run(t, func(*guild.Fence) { advance(guild.LeaseValidity) })
			if !errors.Is(err, context.DeadlineExceeded) || f.State() != guild.FenceWaiting {
				t.Fatalf("ended with %v, fence %s; want the deadline and the fence still waiting", err, f.State())
			}
		})
	})
	t.Run("the demotion", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f, err := run(t, func(f *guild.Fence) { f.Demote() })
			if !errors.Is(err, context.Canceled) || f.State() != guild.FenceDemoted {
				t.Fatalf("ended with %v, fence %s; want it cancelled and the fence demoted", err, f.State())
			}
		})
	})
}
