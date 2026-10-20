//go:build full

package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/pkg/clock"
)

// towerOutput reads the whole output of the terminal of a character of the Tower,
// from the offset 0, through a connection of its own to the mestre-pty (what
// terminal.replay asks of it), and fails if a stretch of it is missing: the
// stretches come one after the other, the first from 0 and none lost.
func (x *channelRig) towerOutput(t *testing.T, id string) string {
	t.Helper()
	ctx := t.Context()
	rec, err := x.b.e.rt.host.Manager().Record(ctx, "t-"+id)
	if err != nil {
		t.Fatal(err)
	}
	cli, err := terminal.Dial(ctx, clock.Real{}, rec.SocketPath, rec.Secret, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	var out strings.Builder
	var next uint64
	for {
		r, err := cli.Replay(ctx, next)
		if err != nil {
			t.Fatal(err)
		}
		if r.Offset != next || r.Lost != 0 {
			t.Fatalf("the output has a hole: asked from %d, got a stretch from %d with %d lost", next, r.Offset, r.Lost)
		}
		out.Write(r.Data)
		next = r.Offset + uint64(len(r.Data))
		if next >= r.End || len(r.Data) == 0 {
			return out.String()
		}
	}
}

// CA-PROTO-06
// A real Citadel and a real Tower on the same machine: the connection falls and comes back
// during a step (the character receives message after message, and each one is a turn of
// the fake agent), and the output of the terminal goes on without a hole and no command
// is typed twice: every message of the inbox appears in the output as many times as one
// that was never near a cut, once each and in order, and one that is sent again with the
// same id is not typed again. The output is read from the mestre-pty of the Tower, by
// offsets, as terminal.replay does: the connection carries commands and reports, and
// the output never depended on it (the stream to the browser is the panel's, CA-PROTO-09).
func TestTheConnectionFallingAndComingBackDuringAStepLeavesNoHoleInTheOutputAndRepeatsNoCommand(t *testing.T) {
	t.Parallel()
	x := newChannelRig(t)
	ctx := t.Context()
	c := x.character(t, "c1", "Dev-1", characters.Starting)
	if err := x.startOnTower(ctx, c); err != nil {
		t.Fatal(err)
	}
	x.r.waitFor("the character idle", func() bool { return x.state(t, c.ID) == characters.Idle })

	tag := func(i int) string { return fmt.Sprintf("MARCA%02dZ", i) }
	deliver := func(i int, cut bool) inbox.Item {
		t.Helper()
		it, err := x.orc.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: "meu-app", Target: c.ID, Kind: inbox.KindAgent, Sender: "Mestre", Text: "passo " + tag(i)})
		if err != nil {
			t.Fatal(err)
		}
		if cut {
			x.a.e.srvp.Load().CloseAll(protocol.CloseShuttingDown, "the test cuts the connection during the step")
		}
		return it
	}
	// settle waits for the message to have come out on the Tower, for its turn to end and for
	// the Citadel to see the character free; a message the cut kept in the queue is tried
	// again when the connection is back, as the next change of state does.
	settle := func(i int, it inbox.Item) {
		t.Helper()
		x.r.waitFor("the connection to be back", func() bool { return x.b.connected(1) })
		x.r.waitFor("the message "+tag(i)+" to be typed and its turn to end", func() bool {
			_ = x.orc.Inbox.Pump(ctx, c.ID)
			cur, err := x.orc.Inbox.Store().Get(ctx, it.ID)
			return err == nil && cur.State == inbox.Delivered && x.state(t, c.ID) == characters.Idle && strings.Count(x.towerOutput(t, c.ID), tag(i)) > 0
		})
	}

	// the first one is the measure: no cut anywhere near it
	settle(1, deliver(1, false))
	per := strings.Count(x.towerOutput(t, c.ID), tag(1))
	if per == 0 {
		t.Fatal("the message does not show in the output of the terminal")
	}

	items := []inbox.Item{}
	for i := 2; i <= 5; i++ {
		it := deliver(i, true) // the connection falls right as the command goes
		items = append(items, it)
		settle(i, it)
	}

	out := x.towerOutput(t, c.ID) // fails on a hole
	last := -1
	for i := 1; i <= 5; i++ {
		if n := strings.Count(out, tag(i)); n != per {
			t.Errorf("%s shows %d times in the output, want %d (once, as the message that was never near a cut)", tag(i), n, per)
		}
		at := strings.Index(out, tag(i))
		if at < last {
			t.Errorf("%s came out before the message that went first", tag(i))
		}
		last = at
	}

	// a command sent again with the id it already had (what a Citadel that lost the ack would do)
	// is answered as done and is not typed again
	for _, it := range items[:2] {
		res, err := x.a.e.srvp.Load().Link(x.b.id).Call(ctx, protocol.Outgoing{Type: protocol.TypeInboxDeliver, Transient: true,
			Payload: protocol.InboxDeliver{TerminalID: c.TerminalID, ItemID: it.ID, Text: it.Body(), Header: it.Header()}})
		if err != nil || res.Status != protocol.AckOK {
			t.Fatalf("sending %s again: %+v, %v", it.ID, res, err)
		}
	}
	x.r.waitFor("the Tower to have nothing more to say", func() bool { return x.state(t, c.ID) == characters.Idle })
	after := x.towerOutput(t, c.ID)
	for i := 1; i <= 5; i++ {
		if n := strings.Count(after, tag(i)); n != per {
			t.Errorf("after the commands were sent again %s shows %d times, want %d", tag(i), n, per)
		}
	}
}
