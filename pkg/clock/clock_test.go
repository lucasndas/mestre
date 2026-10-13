package clock_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
	_ "time/tzdata" // America/Sao_Paulo without depending on the host

	"github.com/lucasndas/mestre/pkg/clock"
)

var epoch = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

func TestFakeTimerFiresOnAdvance(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(epoch)
	tm := c.NewTimer(10 * time.Second)
	c.Advance(9 * time.Second)
	select {
	case <-tm.C():
		t.Fatal("timer fired early")
	default:
	}
	c.Advance(time.Second)
	select {
	case got := <-tm.C():
		if !got.Equal(epoch.Add(10 * time.Second)) {
			t.Fatalf("fired at %v", got)
		}
	default:
		t.Fatal("timer did not fire")
	}
	if c.Armed() != 0 {
		t.Fatalf("armed = %d after firing", c.Armed())
	}
}

func TestFakeStopAndReset(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(epoch)
	tm := c.NewTimer(time.Second)
	if !tm.Stop() {
		t.Fatal("Stop of armed timer returned false")
	}
	if tm.Stop() {
		t.Fatal("second Stop returned true")
	}
	if tm.Reset(2 * time.Second) {
		t.Fatal("Reset of stopped timer returned true")
	}
	c.Advance(2 * time.Second)
	select {
	case <-tm.C():
	default:
		t.Fatal("reset timer did not fire")
	}
}

// Like the real clock since Go 1.23, no stale tick is delivered after Stop
// or Reset returns.
func TestFakeStopAndResetDiscardUnreadTick(t *testing.T) {
	t.Parallel()
	empty := func(t *testing.T, ch <-chan time.Time, what string) {
		t.Helper()
		select {
		case v := <-ch:
			t.Fatalf("%s: stale tick %v", what, v)
		default:
		}
	}
	t.Run("timer Stop", func(t *testing.T) {
		t.Parallel()
		c := clock.NewFake(epoch)
		tm := c.NewTimer(time.Second)
		c.Advance(time.Second) // fires, nobody reads
		if !tm.Stop() {
			t.Fatal("Stop of a timer with an unread tick returned false")
		}
		empty(t, tm.C(), "after Stop")
	})
	t.Run("timer Reset", func(t *testing.T) {
		t.Parallel()
		c := clock.NewFake(epoch)
		tm := c.NewTimer(time.Second)
		c.Advance(time.Second)
		if !tm.Reset(time.Minute) {
			t.Fatal("Reset of a timer with an unread tick returned false")
		}
		empty(t, tm.C(), "after Reset")
		c.Advance(time.Minute)
		select {
		case <-tm.C():
		default:
			t.Fatal("reset timer did not fire")
		}
	})
	t.Run("ticker Stop", func(t *testing.T) {
		t.Parallel()
		c := clock.NewFake(epoch)
		tk := c.NewTicker(time.Second)
		c.Advance(time.Second)
		tk.Stop()
		empty(t, tk.C(), "after Stop")
	})
	t.Run("ticker Reset", func(t *testing.T) {
		t.Parallel()
		c := clock.NewFake(epoch)
		tk := c.NewTicker(time.Second)
		c.Advance(time.Second)
		tk.Reset(time.Minute)
		empty(t, tk.C(), "after Reset")
		c.Advance(time.Minute)
		select {
		case <-tk.C():
		default:
			t.Fatal("reset ticker did not tick")
		}
	})
}

func TestFakeTickerFiresEveryPeriodAndDropsUnread(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(epoch)
	tk := c.NewTicker(time.Second)
	defer tk.Stop()
	c.Advance(3 * time.Second)
	select {
	case got := <-tk.C():
		if !got.Equal(epoch.Add(time.Second)) {
			t.Fatalf("first tick at %v", got)
		}
	default:
		t.Fatal("ticker did not tick")
	}
	select {
	case <-tk.C():
		t.Fatal("ticker buffered more than one tick")
	default:
	}
	c.Advance(time.Second)
	if got := <-tk.C(); !got.Equal(epoch.Add(4 * time.Second)) {
		t.Fatalf("next tick at %v", got)
	}
}

func TestFakeAfterFuncOrderAndNow(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(epoch)
	var order []string
	c.AfterFunc(2*time.Second, func() { order = append(order, "b@"+c.Now().Sub(epoch).String()) })
	c.AfterFunc(time.Second, func() { order = append(order, "a@"+c.Now().Sub(epoch).String()) })
	c.Advance(5 * time.Second)
	if len(order) != 2 || order[0] != "a@1s" || order[1] != "b@2s" {
		t.Fatalf("order = %v", order)
	}
	if got := clock.Since(c, epoch); got != 5*time.Second {
		t.Fatalf("Since = %v", got)
	}
}

func TestFakeBlockUntilWaitsForArmedTimers(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(epoch)
	done := make(chan error, 1)
	go func() { done <- clock.Sleep(context.Background(), c, time.Minute) }()
	c.BlockUntil(1)
	c.Advance(time.Minute)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWithTimeoutUsesTheClock(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(epoch)
	ctx, cancel := clock.WithTimeout(context.Background(), c, 30*time.Second)
	defer cancel()
	c.Advance(29 * time.Second)
	if ctx.Err() != nil {
		t.Fatal("context done too early")
	}
	if dl, ok := ctx.Deadline(); !ok || !dl.Equal(epoch.Add(30*time.Second)) {
		t.Fatalf("Deadline = %v, %v", dl, ok)
	}
	c.Advance(time.Second)
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("Err = %v, want DeadlineExceeded", ctx.Err())
	}
	if !errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
		t.Fatalf("cause = %v", context.Cause(ctx))
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("Done not closed at the deadline")
	}
	child, stop := context.WithCancel(ctx)
	defer stop()
	if !errors.Is(child.Err(), context.DeadlineExceeded) {
		t.Fatalf("derived context Err = %v", child.Err())
	}
}

func TestWithTimeoutCancelAndParent(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(epoch)
	ctx, cancel := clock.WithTimeout(context.Background(), c, time.Minute)
	child, stop := context.WithCancel(ctx)
	defer stop()
	cancel()
	if ctx.Err() != context.Canceled || context.Cause(ctx) != context.Canceled {
		t.Fatalf("after cancel: Err = %v, cause = %v", ctx.Err(), context.Cause(ctx))
	}
	<-ctx.Done()
	<-child.Done()
	if c.Armed() != 0 {
		t.Fatalf("armed = %d after cancel", c.Armed())
	}

	// A parent with an earlier deadline wins, as in context.WithTimeout.
	early, cancelEarly := clock.WithTimeout(context.Background(), c, time.Second)
	defer cancelEarly()
	late, cancelLate := clock.WithTimeout(early, c, time.Hour)
	defer cancelLate()
	if dl, _ := late.Deadline(); !dl.Equal(epoch.Add(time.Second)) {
		t.Fatalf("Deadline = %v, want the parent's", dl)
	}
	c.Advance(time.Second)
	<-late.Done()
	if !errors.Is(late.Err(), context.DeadlineExceeded) {
		t.Fatalf("Err = %v", late.Err())
	}

	// A non-positive timeout is already expired.
	zero, cancelZero := clock.WithTimeout(context.Background(), c, 0)
	defer cancelZero()
	if !errors.Is(zero.Err(), context.DeadlineExceeded) {
		t.Fatalf("zero timeout Err = %v", zero.Err())
	}
}

func TestRealClockInsideSynctestBubble(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var c clock.Clock = clock.Real{}
		start := c.Now()
		if err := clock.Sleep(t.Context(), c, time.Hour); err != nil {
			t.Fatal(err)
		}
		if got := clock.Since(c, start); got != time.Hour {
			t.Fatalf("elapsed = %v", got)
		}
	})
}

// CA-DUBLES-02
func TestDailyTriggerAtTwoAMSaoPauloFiresOnce(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	c := clock.NewFake(time.Date(2026, 10, 12, 1, 59, 0, 0, loc))
	nextAt := func(now time.Time) time.Time {
		n := now.In(loc)
		at := time.Date(n.Year(), n.Month(), n.Day(), 2, 0, 0, 0, loc)
		if !at.After(n) {
			at = time.Date(n.Year(), n.Month(), n.Day()+1, 2, 0, 0, 0, loc)
		}
		return at
	}
	var fired []time.Time
	var arm func()
	arm = func() {
		c.AfterFunc(nextAt(c.Now()).Sub(c.Now()), func() {
			fired = append(fired, c.Now().In(loc))
			arm()
		})
	}
	arm()

	c.Advance(time.Minute)

	if len(fired) != 1 {
		t.Fatalf("fired %d times, want 1", len(fired))
	}
	if h, m := fired[0].Hour(), fired[0].Minute(); h != 2 || m != 0 {
		t.Fatalf("fired at %v, want 02:00", fired[0])
	}
}
