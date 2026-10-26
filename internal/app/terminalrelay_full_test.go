//go:build full

package app

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/protocol"
)

// CA-CANVAS-04
// A real Citadel and a real Tower with a terminal of the fake agent: a browser that
// opens the terminal, a second that opens it later (the first one reconnecting), and
// the first one again after the connection of the Tower falls and comes back during
// the output, each gets, byte for byte, the buffer of the mestre-pty of the Tower (read
// by offsets, as terminal.replay reads it), and what the terminal prints after it, with
// no byte repeated and none missing. The comparison is in Go, on the bytes the panel
// would put in the socket.
func TestTheReplayTheCitadelRelaysIsTheBufferOfTheMachineByteForByte(t *testing.T) {
	t.Parallel()
	x := newChannelRig(t)
	ctx := t.Context()
	c := x.character(t, "c1", "Dev-1", characters.Starting)
	if err := x.startOnTower(ctx, c); err != nil {
		t.Fatal(err)
	}
	x.r.waitFor("the character idle", func() bool { return x.state(t, c.ID) == characters.Idle })
	rel := newEngineRelay(x.a.e)
	x.a.e.relay.Store(rel)
	defer x.a.e.relay.Store(nil)

	say := func(i int) {
		t.Helper()
		it, err := x.orc.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: "meu-app", Target: c.ID, Kind: inbox.KindAgent, Sender: "Mestre", Text: fmt.Sprintf("passo MARCA%02dZ", i)})
		if err != nil {
			t.Fatal(err)
		}
		x.r.waitFor("the message to be typed and its turn to end", func() bool {
			_ = x.orc.Inbox.Pump(ctx, c.ID)
			cur, err := x.orc.Inbox.Store().Get(ctx, it.ID)
			return err == nil && cur.State == inbox.Delivered && x.state(t, c.ID) == characters.Idle && strings.Contains(x.towerOutput(t, c.ID), fmt.Sprintf("MARCA%02dZ", i))
		})
	}
	say(1)

	// equal waits for the browser to hold the whole buffer of the Tower and compares
	equal := func(name string, b *collector) {
		t.Helper()
		want := []byte(x.towerOutput(t, c.ID))
		b.until(t, func() bool { return len(b.data) >= len(want) })
		b.mu.Lock()
		defer b.mu.Unlock()
		if !bytes.Equal(b.data, want) || b.first != 0 || b.jumps != 0 {
			t.Fatalf("%s: the browser got %d bytes from %d with %d jumps; the buffer of the machine has %d", name, len(b.data), b.first, b.jumps, len(want))
		}
	}

	first := newCollector()
	stop1, err := rel.Follow(ctx, c.TerminalID, 0, first)
	if err != nil {
		t.Fatal(err)
	}
	defer stop1()
	first.until(t, func() bool { return len(first.replayed) == 1 })
	equal("the replay", first)

	// a second browser opens the same terminal: its replay is the same buffer
	second := newCollector()
	stop2, err := rel.Follow(ctx, c.TerminalID, 0, second)
	if err != nil {
		t.Fatal(err)
	}
	defer stop2()
	equal("the second browser", second)

	// the output that comes after reaches both live
	say(2)
	equal("the live output of the first", first)
	equal("the live output of the second", second)

	// the connection of the Tower falls during the output and comes back: the stream asks
	// again from where it was, and the browser has the buffer again, whole
	x.a.e.srvp.Load().CloseAll(protocol.CloseShuttingDown, "the test cuts the connection")
	x.r.waitFor("the connection to be back", func() bool { return x.b.connected(1) })
	say(3)
	equal("after the cut, the first", first)
	equal("after the cut, the second", second)
	if len(first.signals) < 2 || first.signals[0] || !first.signals[len(first.signals)-1] {
		t.Fatalf("the browser was not told of the signal that went and came back: %v", first.signals)
	}

	// a browser that comes back later (a fresh stream) gets the same bytes again
	third := newCollector()
	stop3, err := rel.Follow(ctx, c.TerminalID, 0, third)
	if err != nil {
		t.Fatal(err)
	}
	defer stop3()
	equal("the browser that came back", third)
}
