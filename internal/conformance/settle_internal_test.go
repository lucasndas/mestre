package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

type liveFake struct {
	screen string
	ended  chan struct{}
}

func (l liveFake) Screen(context.Context) (string, error) { return l.screen, nil }
func (l liveFake) Ended() <-chan struct{}                 { return l.ended }

// startSettle runs settle in the background; the test releases it with a
// stable screen or with the clock.
func startSettle(c *character) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		c.settle(context.Background(), liveFake{screen: "live", ended: make(chan struct{})})
		close(done)
	}()
	return done
}

func newSettleCharacter(clk *clock.Fake, screen string) *character {
	return &character{ru: &run{r: &Runner{Clock: clk}}, changed: make(chan struct{}), screen: screen, ready: true}
}

// A ready screen from before the end of the turn (the message echoed, the
// input box empty) is not the screen of the answer: after the end of the
// turn, the next stable screen is (conformance round 3, Antigravity).
func TestSettleTakesTheStableScreenAfterTheEndOfTheTurn(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Unix(0, 0))
	c := newSettleCharacter(clk, "> question\n>")
	done := startSettle(c)
	clk.BlockUntil(1)
	c.mu.Lock()
	c.screen = "> question\nthe answer\n>"
	c.stables++
	close(c.changed)
	c.changed = make(chan struct{})
	c.mu.Unlock()
	<-done
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.screen != "> question\nthe answer\n>" {
		t.Errorf("screen = %q, want the stable screen after the end", c.screen)
	}
}

// When the screen was already final and does not change, the live screen is
// taken after settleLimit.
func TestSettleFallsBackToTheLiveScreen(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Unix(0, 0))
	c := newSettleCharacter(clk, "stale")
	done := startSettle(c)
	clk.BlockUntil(1)
	clk.Advance(settleLimit)
	<-done
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.screen != "live" {
		t.Errorf("screen = %q, want the live screen", c.screen)
	}
}

// A change that is not a stable screen (a hook call) does not release it.
func TestSettleIgnoresChangesThatAreNotStableScreens(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Unix(0, 0))
	c := newSettleCharacter(clk, "stale")
	done := startSettle(c)
	clk.BlockUntil(1)
	c.mu.Lock()
	close(c.changed)
	c.changed = make(chan struct{})
	c.mu.Unlock()
	clk.Advance(settleLimit - time.Millisecond)
	select {
	case <-done:
		t.Fatal("settle returned before the limit on a change that is not a stable screen")
	default:
	}
	clk.Advance(time.Millisecond)
	<-done
}
