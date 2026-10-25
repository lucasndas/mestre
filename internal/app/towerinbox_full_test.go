//go:build full

package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/providers"
)

// hookOnTower gives the Runtime of the Tower an event of the hook engine for a
// character, as the hook of its agent would.
func (x *channelRig) hookOnTower(c characters.Character, kind providers.EventKind) {
	x.b.e.rt.rt.OnHook(providers.Event{Version: providers.EventVersion, Provider: providers.ClaudeCode, Event: kind,
		AgentID: c.ID, TableID: c.Table, SessionID: "sess-" + c.ID, TurnID: "t1"}, hook.Answer{})
}

// CA-PROTO-08
// A terminal of a Tower that became busy answers the inbox.deliver of the
// Citadel, over the live connection, with an ack ok that carries the
// inbox.result busy; nothing is typed, and the item goes back to the queue of
// the Citadel, which delivers it when the turn ends.
func TestAnInboxDeliverToATerminalThatBecameBusyIsAckedOKWithBusyAndTheItemGoesBack(t *testing.T) {
	t.Parallel()
	x := newChannelRig(t)
	ctx := t.Context()
	c := x.character(t, "c1", "Dev-1", characters.Starting)
	if err := x.startOnTower(ctx, c); err != nil {
		t.Fatal(err)
	}
	x.r.waitFor("the character idle", func() bool { return x.state(t, c.ID) == characters.Idle })

	// a turn begins on the Tower; the Citadel hears it and is then made to believe the
	// character is free (the report of the turn is on its way, which is the case of the criterion)
	x.hookOnTower(c, providers.EventTurnStart)
	x.r.waitFor("the Tower to be busy", func() bool {
		st, ok := x.b.e.rt.rt.State(c.ID)
		return ok && st.State == characters.Busy
	})
	x.r.waitFor("the Citadel to see it busy", func() bool { return x.state(t, c.ID) == characters.Busy })
	if _, err := orcDB(t, x.a).Writer().ExecContext(ctx, `UPDATE characters SET state = 'idle' WHERE id = ?`, c.ID); err != nil {
		t.Fatal(err)
	}

	// the command itself: the ack is ok and its result is the inbox.result busy
	link := x.a.e.srvp.Load().Link(x.b.id)
	res, err := link.Call(ctx, protocol.Outgoing{Type: protocol.TypeInboxDeliver, Transient: true,
		Payload: protocol.InboxDeliver{TerminalID: c.TerminalID, ItemID: "m77", Text: "direto", Header: "[Mestre · agent · de Dev · m77]"}})
	if err != nil || res.Status != protocol.AckOK {
		t.Fatalf("the ack of the inbox.deliver to a busy terminal = %+v, %v; want ok", res, err)
	}
	var got protocol.InboxResult
	if err := json.Unmarshal(res.Result, &got); err != nil || got.Status != protocol.InboxBusy || got.ItemID != "m77" {
		t.Fatalf("the result in the ack = %s (%v); want the inbox.result busy of m77", res.Result, err)
	}

	// the item of the inbox: delivered by the Citadel to that busy terminal, it comes back queued
	it, err := x.orc.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: "meu-app", Target: c.ID, Kind: inbox.KindAgent, Sender: "Mestre", Text: "faca-depois-do-turno"})
	if err != nil {
		t.Fatal(err)
	}
	// the Citadel did try: the Tower took an inbox.deliver of this very item and
	// answered it busy (the queue is the initial state, so only this shows the
	// way back was by the busy of the Tower)
	var ack []byte
	if err := x.b.e.mdb.Reader().QueryRowContext(ctx, `SELECT ack FROM processed_commands WHERE type = ? AND CAST(ack AS TEXT) LIKE ?`,
		protocol.TypeInboxDeliver, `%"`+it.ID+`"%`).Scan(&ack); err != nil {
		t.Fatalf("the Tower has no inbox.deliver of %s in what it processed: %v", it.ID, err)
	}
	if !strings.Contains(string(ack), `"busy"`) || strings.Contains(string(ack), `"delivered"`) {
		t.Errorf("the Tower answered the delivery of %s with %s; want busy", it.ID, ack)
	}
	if cur, err := x.orc.Inbox.Store().Get(ctx, it.ID); err != nil || cur.State != inbox.Queued {
		t.Fatalf("the item delivered to a busy terminal is %+v (%v); want it back in the queue", cur, err)
	}
	if pending, err := x.orc.Inbox.List(ctx, c.ID); err != nil || len(pending) != 1 || pending[0].ID != it.ID {
		t.Fatalf("the queue of the Citadel = %+v (%v); want the item", pending, err)
	}
	scr, err := x.b.e.rt.rt.Screen(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(scr.Text, "faca-depois-do-turno") || strings.Contains(scr.Text, "direto") {
		t.Errorf("something was typed in the busy terminal:\n%s", scr.Text)
	}

	// the Citadel catches up with the Tower (the report of the turn arrives), the turn ends,
	// the Tower reports idle and the Citadel delivers the item
	if _, err := orcDB(t, x.a).Writer().ExecContext(ctx, `UPDATE characters SET state = 'busy' WHERE id = ?`, c.ID); err != nil {
		t.Fatal(err)
	}
	x.hookOnTower(c, providers.EventTurnEnd)
	x.r.waitFor("the item to be typed on the Tower", func() bool {
		s, err := x.b.e.rt.rt.Screen(ctx, c.ID)
		return err == nil && strings.Contains(s.Text, "faca-depois-do-turno")
	})
}
