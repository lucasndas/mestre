package chain_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
)

// fakeStallPeople is the Telegram of the warning of a table that spins.
type fakeStallPeople struct {
	mu   sync.Mutex
	told []chain.StallNotice
	// fail makes the delivery fail (the bot is not up yet).
	fail bool
}

func (f *fakeStallPeople) StalledTable(_ context.Context, n chain.StallNotice) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("the bot is not running")
	}
	f.told = append(f.told, n)
	return nil
}

func (f *fakeStallPeople) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.told)
}

// fakeQueuePeople is the Telegram of the warning of a queue that accumulates.
type fakeQueuePeople struct {
	mu   sync.Mutex
	told []chain.QueueNotice
}

func (f *fakeQueuePeople) QueueAccumulating(_ context.Context, n chain.QueueNotice) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.told = append(f.told, n)
	return nil
}

func (f *fakeQueuePeople) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.told)
}

// stallItems lists the warnings of a stalled table the Grão-Mestre got.
func (w *world) stallItems() []inbox.Item {
	w.t.Helper()
	var out []inbox.Item
	for _, it := range w.itemsOf(w.gm, inbox.KindSystem) {
		if strings.Contains(it.Text, "sem entregar nada") {
			out = append(out, it)
		}
	}
	return out
}

// CA-SUPCAD-06
func TestATableThatSpinsWarnsTheGrandMasterAndThePeopleOnceAndTheButtonPausesItWithTheAudit(t *testing.T) {
	t.Parallel()
	w := newSup(t)
	other, _, _ := w.otherTable()
	w.clk.Advance(3*time.Hour - time.Minute)
	w.tick()
	if len(w.stallItems()) != 0 || w.stallers.count() != 0 {
		t.Fatal("warned before 3 h without a delivery")
	}
	w.clk.Advance(time.Minute)
	w.tick()
	items := w.stallItems()
	if len(items) == 0 || w.stallers.count() == 0 {
		t.Fatalf("after 3 h: the Grão-Mestre got %d warnings, the people %d", len(items), w.stallers.count())
	}
	first, told := len(items), w.stallers.count()
	w.tick()
	if len(w.stallItems()) != first || w.stallers.count() != told {
		t.Fatal("the same quiet was warned twice")
	}
	// the Approver touches the button of the table
	var n chain.StallNotice
	for _, x := range w.stallers.told {
		if x.Table == w.table.ID {
			n = x
		}
	}
	if n.Table == "" || !strings.Contains(n.Text, w.table.ID) {
		t.Fatalf("the people were not told of %s: %+v", w.table.ID, w.stallers.told)
	}
	if _, err := w.supervisor.PauseStalled(w.ctx, chain.PauseRequest{Workspace: n.Workspace, Table: n.Table, Reason: "gira", By: person(characters.LevelObserver)}); err == nil {
		t.Fatal("an Observer paused the table")
	}
	if p, _ := w.orc.Tables.Paused(w.ctx, w.slug, w.table.ID); p {
		t.Fatal("the refused button paused the table")
	}
	changed, err := w.supervisor.PauseStalled(w.ctx, chain.PauseRequest{Workspace: n.Workspace, Table: n.Table, Reason: "gira em vão", By: person(characters.LevelApprover)})
	if err != nil || !changed {
		t.Fatalf("the Approver's button = %v, %v", changed, err)
	}
	cur, _ := w.orc.Tables.Get(w.ctx, w.slug, w.table.ID)
	if !cur.Paused || cur.PausedBy != "user:u-approver" || cur.PausedReason != "gira em vão" {
		t.Errorf("table after the button = paused %v by %q: %q", cur.Paused, cur.PausedBy, cur.PausedReason)
	}
	log, err := w.supervisor.PauseAudit(w.ctx, w.slug, w.table.ID)
	if err != nil || len(log) != 1 || log[0].Action != "pause_table" || log[0].By != "user:u-approver" || log[0].Reason != "gira em vão" {
		t.Errorf("audit = %+v, %v", log, err)
	}
	if again, err := w.supervisor.PauseStalled(w.ctx, chain.PauseRequest{Workspace: n.Workspace, Table: n.Table, Reason: "de novo", By: person(characters.LevelApprover)}); err != nil || again {
		t.Errorf("the second touch = %v, %v; want no effect", again, err)
	}
	// a delivery after the warning: the count starts again, and a new warning
	// comes only after 3 h more
	if err := w.orc.Tables.Resume(w.ctx, w.slug, other.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.orc.Tables.Delivered(w.ctx, w.slug, other.ID); err != nil {
		t.Fatal(err)
	}
	before := w.stallers.count()
	w.clk.Advance(3*time.Hour - time.Minute)
	w.tick()
	if w.stallers.count() != before {
		t.Error("warned about the table that delivered, before 3 h more")
	}
	w.clk.Advance(time.Minute)
	w.tick()
	if w.stallers.count() == before {
		t.Error("no new warning after 3 h more without a delivery")
	}
}

// Part of CA-SUPCAD-06: if the people could not be told (the bot is not up), nothing is
// marked and the Grão-Mestre is not told yet: the next round tries again and each gets
// the warning once.
func TestTheWarningOfAStalledTableIsTriedAgainWhenThePeopleCouldNotBeTold(t *testing.T) {
	t.Parallel()
	w := newSup(t)
	w.stallers.fail = true
	w.clk.Advance(3 * time.Hour)
	w.tick()
	if len(w.stallItems()) != 0 || w.stallers.count() != 0 {
		t.Fatal("the warning went out although the people could not be told")
	}
	w.stallers.mu.Lock()
	w.stallers.fail = false
	w.stallers.mu.Unlock()
	w.tick()
	w.tick()
	if len(w.stallItems()) != 1 || w.stallers.count() != 1 {
		t.Fatalf("after the bot came up: %d items for the Grão-Mestre, %d messages to the people; want 1 and 1", len(w.stallItems()), w.stallers.count())
	}
}
