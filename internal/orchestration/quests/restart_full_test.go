//go:build full

package quests_test

import (
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/waits"
)

// CA-CIDADELA-13 (the combined scenario): an instance stopped in the middle of
// the work, with a character, a wait and the outbox with items; when the
// process restarts, the three come back to their state. (The machine really
// restarted is MVP-12, in the heavy suite.)
func TestACitadelStoppedInTheMiddleOfTheWorkBringsBackTheCharacterTheWaitAndTheOutbox(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev Claude", "Dev")
	rev := w.recruit("Dev Claude", "Revisor")
	w.machines.Report(w.ctx, dev.ID, characters.Busy)
	if r := w.call(w.master.ID, "connect", nil, "Dev", "Revisor"); !r.OK {
		t.Fatalf("connect: %+v", r.Error)
	}
	wt, err := w.orc.Waits.WaitFor(w.ctx, waits.WaitRequest{Waiter: w.char("Dev"), Names: []string{"Revisor"}, Timeout: time.Hour})
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	for _, text := range []string{"primeira resposta", "segunda resposta"} {
		if _, err := w.orc.Inbox.Store().AddOutbound(w.ctx, inbox.Outbound{Workspace: w.slug, Message: "m1", Channel: inbox.ChannelTelegram, User: "lucas", Text: text, CreatedAt: w.clk.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	type snap struct {
		state characters.State
		wait  waits.State
		out   []string
	}
	take := func() snap {
		t.Helper()
		var s snap
		s.state = w.get(dev.ID).State
		got, err := w.orc.Waits.Store().GetWait(w.ctx, wt.ID)
		if err != nil {
			t.Fatal(err)
		}
		s.wait = got.State
		outs, err := w.orc.Inbox.Store().PendingOutbound(w.ctx, w.slug, inbox.ChannelTelegram)
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range outs {
			s.out = append(s.out, o.Text)
		}
		return s
	}
	before := take()
	if before.state != characters.Busy || before.wait != waits.Active || len(before.out) != 2 {
		t.Fatalf("before: %+v", before)
	}
	_ = rev

	w.restart()

	after := take()
	if after.state != before.state || after.wait != before.wait || len(after.out) != 2 || after.out[0] != "primeira resposta" || after.out[1] != "segunda resposta" {
		t.Fatalf("after the restart: %+v; want %+v", after, before)
	}
	if c := w.get(dev.ID); c.Name != "Dev" || c.Table != w.table.ID {
		t.Errorf("the character after the restart: %+v", c)
	}
}
