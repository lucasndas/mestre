package protocol_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mestre-ia/mestre/internal/protocol"
)

func statusMsg(term string) protocol.Outgoing {
	return protocol.Outgoing{Type: protocol.TypeTerminalStatus, Payload: protocol.TerminalStatus{TerminalID: term, State: "idle"}}
}

// A handler that takes minutes, with thousands of messages behind it, does
// not make a live connection look dead: the read loop never waits for it, so
// the pongs (seen only while the connection is read, in the transport as in
// the double) and the heartbeats keep coming.
func TestASlowHandlerDoesNotMakeALiveConnectionLookDead(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		release := make(chan struct{})
		var ran atomic.Int32
		w.cfgC.Router.HandleFunc(protocol.TypeTerminalStatus, func(context.Context, *protocol.Message) (*protocol.Reply, error) {
			<-release
			ran.Add(1)
			return nil, nil
		})
		var lost, ends atomic.Int32
		w.cfgC.Hooks.HeartbeatLost = func(protocol.Peer) { lost.Add(1) }
		w.cfgC.Hooks.OnDisconnect = func(*protocol.Session, error) { ends.Add(1) }
		w.cfgT.Hooks.OnDisconnect = func(*protocol.Session, error) { ends.Add(1) }
		w.cfgT.Heartbeat = func(context.Context) (protocol.MachineHeartbeat, error) { return protocol.MachineHeartbeat{}, nil }
		w.start()
		defer w.stop()
		released := false
		defer func() {
			if !released {
				close(release)
			}
		}()
		settle()
		const n = 5000 // more than the 4096 messages the queue used to hold
		for range n {
			if _, err := w.client.Link().Send(t.Context(), statusMsg("t1")); err != nil {
				t.Fatal(err)
			}
		}
		advance(3 * time.Minute)
		if ends.Load() != 0 || lost.Load() != 0 {
			t.Fatalf("with a slow handler: %d ends of the connection, %d heartbeats lost", ends.Load(), lost.Load())
		}
		released = true
		close(release)
		settle()
		if ran.Load() != n {
			t.Fatalf("ran %d of %d", ran.Load(), n)
		}
	})
}

// A peer that sends more than the receiver can hold while it is behind is
// closed with 4008; it sends again what waits for an ack, and everything runs
// once.
func TestAPeerThatSendsMoreThanTheReceiverHoldsIsClosedAndNothingRunsTwice(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		w.cfgC.InboundLimit = 64 << 10
		release := make(chan struct{})
		var ran atomic.Int32
		w.cfgC.Router.HandleFunc(protocol.TypeTerminalStatus, func(context.Context, *protocol.Message) (*protocol.Reply, error) {
			<-release
			ran.Add(1)
			return nil, nil
		})
		var overloaded, dropped atomic.Int32
		w.cfgC.Hooks.OnDisconnect = func(_ *protocol.Session, cause error) {
			if errors.Is(cause, protocol.ErrOverloaded) {
				overloaded.Add(1)
			}
		}
		w.cfgT.Hooks.OnDisconnect = func(*protocol.Session, error) { dropped.Add(1) }
		w.start()
		defer w.stop()
		released := false
		defer func() {
			if !released {
				close(release)
			}
		}()
		settle()
		const n = 1000
		for range n {
			if _, err := w.client.Link().Send(t.Context(), statusMsg("t1")); err != nil {
				t.Fatal(err)
			}
		}
		settle()
		// the handler is stuck, so the Citadel waits (up to 5 s) for what it
		// accepted to run before it closes
		advance(5 * time.Second)
		if dropped.Load() == 0 {
			t.Fatal("the Citadel held more than its limit")
		}
		// every round takes what fits and is closed again, with the backoff
		// growing, until the outbox of the Tower is empty
		released = true
		close(release)
		advance(30 * time.Minute)
		if overloaded.Load() == 0 {
			t.Fatal("no connection was closed for holding too much")
		}
		if ran.Load() != n || w.partsC.Store.Claims() != n {
			t.Fatalf("ran %d, claimed %d, want %d each", ran.Load(), w.partsC.Store.Claims(), n)
		}
		if left := w.partsT.Store.Waiting(protocol.CitadelKey); left != 0 {
			t.Fatalf("the outbox of the Tower still has %d", left)
		}
	})
}

// A receiver that closes a peer for sending too much first runs what it had
// accepted and answers it, however the goroutines are scheduled: a peer that
// sends the same flood after every reconnection moves forward on each round,
// instead of finding the receiver closed before the first message ran (and
// getting, on the next round, the same messages first, as duplicates).
func TestAnOverloadedRoundRunsAndAnswersWhatItAccepted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		w.cfgC.InboundLimit = 64 << 10
		release := make(chan struct{})
		var ran, overloaded atomic.Int32
		w.cfgC.Router.HandleFunc(protocol.TypeTerminalStatus, func(context.Context, *protocol.Message) (*protocol.Reply, error) {
			<-release
			ran.Add(1)
			return nil, nil
		})
		w.cfgC.Hooks.OnDisconnect = func(_ *protocol.Session, cause error) {
			if errors.Is(cause, protocol.ErrOverloaded) {
				overloaded.Add(1)
			}
		}
		w.start()
		defer w.stop()
		released := false
		defer func() {
			if !released {
				close(release)
			}
		}()
		settle()
		const n = 1000
		for range n {
			if _, err := w.client.Link().Send(t.Context(), statusMsg("t1")); err != nil {
				t.Fatal(err)
			}
		}
		settle() // the queues are full and the handler holds the first message
		if overloaded.Load() != 0 {
			t.Fatal("the connection closed before what it accepted ran")
		}
		released = true
		close(release)
		settle() // the first round only: the Tower has not reconnected yet
		if overloaded.Load() != 1 {
			t.Fatalf("%d connections closed for holding too much, want 1", overloaded.Load())
		}
		// the 64 KiB hold at least 64 messages of at most 1 KiB
		if ran.Load() < 64 || int(ran.Load()) != w.partsC.Store.Claims() {
			t.Fatalf("ran %d, claimed %d: the round did not run what it accepted", ran.Load(), w.partsC.Store.Claims())
		}
		if left := w.partsT.Store.Waiting(protocol.CitadelKey); left != n-int(ran.Load()) {
			t.Fatalf("the outbox of the Tower has %d, want %d: what ran was not answered before the close", left, n-int(ran.Load()))
		}
	})
}

// A connection that drops right after it opened is a failure: the delay
// keeps doubling instead of starting over at 1 s.
func TestAConnectionThatDropsAtOnceDoesNotResetTheBackoff(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		w.cfgC.Hooks.OnReady = func(s *protocol.Session) { s.Close(protocol.CloseShuttingDown, "going away") }
		w.start()
		defer w.stop()
		advance(10 * time.Second)
		g := w.gaps()
		if len(g) < 3 || g[0] != time.Second || g[1] != 2*time.Second || g[2] != 4*time.Second {
			t.Fatalf("gaps = %v, want 1 s, 2 s, 4 s", g)
		}
	})
}

// The receiver keeps the id of a command while its sender is away, however
// long: the sender may still send it again, and it must not run twice.
func TestAnIdIsKeptWhileItsSenderIsAwayForMoreThanADay(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		ty := &typist{}
		w.cfgT.Router.Handle(protocol.TypeTerminalInput, ty)
		w.start()
		defer w.stop()
		settle()
		w.dropAcks.Store(true) // the command runs, its ack is lost
		if _, err := w.toTower().Send(t.Context(), input("once\n")); err != nil {
			t.Fatal(err)
		}
		settle()
		w.net.Partition(towerHost, citadelID)
		advance(25 * time.Hour)
		w.dropAcks.Store(false)
		w.net.Heal(towerHost, citadelID)
		advance(2 * time.Minute)
		if got := ty.list(); len(got) != 1 {
			t.Fatalf("typed = %q: the command ran again after a day away", got)
		}
		if n := w.partsC.Store.Waiting(towerID); n != 0 {
			t.Fatalf("the outbox of the Citadel still has %d", n)
		}
	})
}

// On a connection that lasts, the ids go once they are a day old and below
// the floor the sender keeps sending: the memory does not grow forever.
func TestTheIdsBelowTheFloorOfTheSenderAreForgottenAfterADay(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		quiet := protocol.Timing{Ping: 48 * time.Hour, PongTimeout: 48 * time.Hour, Heartbeat: 48 * time.Hour, HeartbeatLost: 72 * time.Hour}
		w.cfgC.Timing, w.cfgT.Timing = quiet, quiet
		w.cfgT.Router.Handle(protocol.TypeTerminalInput, &typist{})
		w.start()
		defer w.stop()
		settle()
		if r, err := w.toTower().Call(t.Context(), input("x")); err != nil || r.Status != protocol.AckOK {
			t.Fatalf("Call = %+v, %v", r, err)
		}
		advance(23 * time.Hour)
		if n := w.partsT.Store.Remembered(protocol.CitadelKey); n != 1 {
			t.Fatalf("remembered %d ids after 23 hours, want 1", n)
		}
		advance(3 * time.Hour)
		if n := w.partsT.Store.Remembered(protocol.CitadelKey); n != 0 {
			t.Fatalf("remembered %d ids after 26 hours, want 0", n)
		}
	})
}

// On a connection that lasts more than a day, an id the sender still waits
// on (its ack was lost) is at the floor and is kept: when the sender sends it
// again, after the next reconnection, it does not run twice.
func TestAnIdTheSenderStillWaitsOnIsKeptOnAConnectionThatLastsADay(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		quiet := protocol.Timing{Ping: 48 * time.Hour, PongTimeout: 48 * time.Hour, Heartbeat: 48 * time.Hour, HeartbeatLost: 72 * time.Hour}
		w.cfgC.Timing, w.cfgT.Timing = quiet, quiet
		ty := &typist{}
		w.cfgT.Router.Handle(protocol.TypeTerminalInput, ty)
		w.start()
		defer w.stop()
		settle()
		w.dropAcks.Store(true) // the command runs, its ack is lost
		if _, err := w.toTower().Send(t.Context(), input("once\n")); err != nil {
			t.Fatal(err)
		}
		advance(26 * time.Hour)
		if n := w.partsT.Store.Remembered(protocol.CitadelKey); n != 1 {
			t.Fatalf("remembered %d ids after 26 hours, want the one still waited on", n)
		}
		w.dropAcks.Store(false)
		w.net.Cut(towerHost, citadelID)
		advance(time.Minute)
		if got := ty.list(); len(got) != 1 {
			t.Fatalf("typed = %q: the command ran again", got)
		}
		if n := w.partsC.Store.Waiting(towerID); n != 0 {
			t.Fatalf("the outbox of the Citadel still has %d", n)
		}
	})
}
