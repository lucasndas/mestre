package ptyd_test

import (
	"context"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/terminal/ptyd"
	"github.com/lucasndas/mestre/internal/terminal/ptywire"
	"github.com/lucasndas/mestre/internal/testkit/fakepty"
	"github.com/lucasndas/mestre/pkg/clock"
)

type sessionEnv struct {
	clk    *clock.Fake
	runner *fakepty.Runner
	sess   *ptyd.Session
	events chan ptywire.Msg
}

func newSession(t *testing.T) *sessionEnv {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))
	e := &sessionEnv{clk: clk, runner: &fakepty.Runner{Clock: clk}, events: make(chan ptywire.Msg, 16)}
	e.sess = ptyd.NewSession(ptyd.SessionConfig{
		Runner: e.runner, Clock: clk,
		OnEvent: func(m ptywire.Msg) { e.events <- m },
	})
	t.Cleanup(e.sess.Close)
	if _, err := e.sess.Start(t.Context(), ptyd.StartSpec{Path: "agent"}); err != nil {
		t.Fatal(err)
	}
	return e
}

// print makes the fake agent print s and waits until the emulator took it.
func (e *sessionEnv) print(t *testing.T, s string) {
	t.Helper()
	if _, err := e.runner.Last().Process().Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	// The pump moves the bytes into the buffer asynchronously: wait for
	// them, then for the emulator.
	want := e.sess.Buffer().End() + uint64(len(s))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	for e.sess.Buffer().End() < want {
		if _, err := e.sess.Buffer().Next(ctx, e.sess.Buffer().End(), 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.sess.WaitProcessed(ctx, want); err != nil {
		t.Fatal(err)
	}
}

// CA-TERM-03
func TestScreenIsStableOnceAfter500msWithoutChange(t *testing.T) {
	t.Parallel()
	e := newSession(t)

	// A screen that changes every 300 ms, so it is never quiet for 500 ms.
	for i := range 4 {
		e.print(t, "frame "+string(rune('A'+i))+"\r\n")
		e.clk.BlockUntil(1) // the stability timer was re-armed
		e.clk.Advance(300 * time.Millisecond)
	}
	if n := len(e.events); n != 0 {
		t.Fatalf("%d events while the screen kept changing, want none", n)
	}
	// It stops: 300 ms of the last silence are gone, 200 ms more is not enough...
	e.clk.Advance(199 * time.Millisecond)
	if n := len(e.events); n != 0 {
		t.Fatalf("stable after 499 ms of silence")
	}
	// ...and the 500th millisecond makes it stable, exactly once.
	e.clk.Advance(1 * time.Millisecond)
	if n := len(e.events); n != 1 {
		t.Fatalf("%d events after 500 ms of silence, want 1", n)
	}
	if ev := <-e.events; ev.T != ptywire.TypeStable {
		t.Fatalf("event = %+v, want stable", ev)
	}
	e.clk.Advance(time.Hour)
	if n := len(e.events); n != 0 {
		t.Fatalf("stable announced again without a change")
	}
	if !e.sess.State().Stable {
		t.Fatal("state does not say stable")
	}

	// A change makes it unstable and the next quiet period announces again.
	e.print(t, "more\r\n")
	if e.sess.State().Stable {
		t.Fatal("still stable after a change")
	}
	e.clk.BlockUntil(1)
	e.clk.Advance(500 * time.Millisecond)
	if n := len(e.events); n != 1 {
		t.Fatalf("%d events after the second quiet period, want 1", n)
	}
}

func TestExitIsAnnouncedWithTheCodeAfterTheLastOutput(t *testing.T) {
	t.Parallel()
	e := newSession(t)
	e.print(t, "bye\r\n")
	// Drain the stable event the output caused, if the timer ran.
	e.clk.BlockUntil(1)

	e.runner.Last().Exit(7)
	e.clk.BlockUntil(2) // the stability timer and the drain wait
	e.clk.Advance(ptyd.DefaultDrainAfterExit)

	var exit ptywire.Msg
	for ev := range e.events {
		if ev.T == ptywire.TypeExit {
			exit = ev
			break
		}
	}
	if exit.ExitCode == nil || *exit.ExitCode != 7 || exit.End != uint64(len("bye\r\n")) {
		t.Fatalf("exit = %+v, want code 7 at the end of the output", exit)
	}
	if !e.sess.Exited() {
		t.Fatal("session does not say exited")
	}
}
