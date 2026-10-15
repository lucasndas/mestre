//go:build full

package waits_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/waits"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	"github.com/lucasndas/mestre/pkg/clock"
)

type snapshot struct {
	waits []string
	queue map[string][]string
}

// shot describes the waits and the queues as the document says they must come
// back: the waits that are active (their kind, targets, reason and limits), and
// for each character the items that wait, in the order of delivery.
func shot(t *testing.T, w *world, chars ...characters.Character) snapshot {
	t.Helper()
	s := snapshot{queue: map[string][]string{}}
	active, err := w.svc.Store().ActiveWaits(w.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range active {
		var targets []string
		for _, tg := range a.Targets {
			targets = append(targets, tg.ID+":"+tg.Name)
		}
		s.waits = append(s.waits, strings.Join([]string{a.ID, string(a.Kind), string(a.Mode), a.Reason, strings.Join(targets, ","), a.TimeoutAt.String(), a.UntilAt.String()}, "|"))
	}
	for _, c := range chars {
		for _, it := range w.inboxOf(c) {
			s.queue[c.Name] = append(s.queue[c.Name], it.ID+"|"+string(it.Kind)+"|"+it.Text)
		}
	}
	return s
}

// CA-ESP-07 and CA-CIDADELA-13 (the combined scenario: a character mid-work, a
// wait and an outbox with items, all in the one database; the restart of the
// real machine is the MVP-12 of the heavy suite): the waits, the asks and the
// queues are in the database, and after a restart of the Citadel the character,
// the waits and the outbox come back equal; the
// wait for a time that came while it was down is satisfied, the ones whose
// limit passed are expired (with the notice), and the item that was being
// delivered goes back to the queue because no turn was recorded after it.
func TestWaitsAndQueuesComeBackEqualAfterTheCitadelRestartsAndAnOverdueTimeWaitIsSatisfied(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "mestre.db")
	if err := os.WriteFile(path, guildTemplate, 0o600); err != nil {
		t.Fatal(err)
	}
	open := func() *store.DB {
		t.Helper()
		db, err := guilddb.Open(ctx, path, store.Options{Synchronous: "FULL"})
		if err != nil {
			t.Fatal(err)
		}
		return db
	}
	db := open()
	if _, err := db.Writer().Exec(`INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES (?, 'lucas/meu-app', 'lucas/meu-app', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`, workspace); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(day0)
	w := newWorldOver(t, db, clk)
	master := w.dev("42-login", "Mestre")
	dev := w.dev("42-login", "Dev", master)
	rev := w.dev("42-login", "Revisor", master, dev)
	qa := w.dev("42-login", "QA", master, dev)
	// what is in the database when the process stops
	w.set(dev, characters.Busy)
	w.set(master, characters.Busy)
	if _, err := w.svc.WaitFor(ctx, waits.WaitRequest{Waiter: master, Names: []string{"Revisor", "QA"}}); err != nil { // 60 min
		t.Fatal(err)
	}
	if _, err := w.svc.WaitFor(ctx, waits.WaitRequest{Waiter: dev, Names: []string{"QA"}, Timeout: 10 * time.Minute}); err != nil { // runs out during the fall
		t.Fatal(err)
	}
	if _, err := w.svc.WaitSteps(ctx, waits.StepsRequest{Waiter: master, Quest: "q1", Steps: []waits.Target{{ID: "s1", Name: "dev"}, {ID: "s2", Name: "qa"}}}); err != nil { // 120 min
		t.Fatal(err)
	}
	if _, err := w.svc.WaitApproval(ctx, master, 7, time.Hour); err != nil {
		t.Fatal(err)
	}
	until, err := w.svc.WaitUntil(ctx, dev, day0.Add(20*time.Minute)) // comes during the fall
	if err != nil {
		t.Fatal(err)
	}
	later, err := w.svc.WaitUntil(ctx, qa, day0.Add(2*time.Hour)) // still ahead
	if err != nil {
		t.Fatal(err)
	}
	// an ask that nobody was still waiting for, and one whose call died with the process
	open1, err := w.svc.Ask(ctx, waits.AskRequest{From: dev, To: rev, Prompt: "revise", NoWait: true})
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := w.svc.Store().InsertAsk(ctx, waits.Ask{Workspace: workspace, Table: "42-login", Asker: dev.ID, Target: qa.ID, Blocking: true, CreatedAt: clk.Now()})
	if err != nil {
		t.Fatal(err)
	}
	// the queues: two items waiting for the busy master, and one that was being
	// typed into the Revisor when the process fell (its turn was never recorded)
	for _, text := range []string{"primeiro", "segundo"} {
		w.enqueue(inbox.NewItem{Target: master.ID, Kind: inbox.KindAgent, Sender: "Dev", Text: text})
	}
	inflight := w.all(rev)
	if len(inflight) != 1 || inflight[0].State != inbox.Delivering {
		t.Fatalf("the question of the ask should be being delivered: %+v", inflight)
	}
	// the outbox of the link, with items not yet acknowledged
	sent := protocol.NewSQLStore(db)
	var wantOut []string
	for _, id := range []string{"01J0000000000000000000000A", "01J0000000000000000000000B"} {
		env := protocol.Envelope{ID: id, Type: "terminal.open", MachineID: "m1", Epoch: 1, Payload: []byte(`{}`), Ack: true}
		if err := sent.Put(ctx, "m1", env); err != nil {
			t.Fatal(err)
		}
		wantOut = append(wantOut, id)
	}
	charsBefore := map[string]characters.Character{}
	for _, c := range []characters.Character{master, dev, rev, qa} {
		got, err := w.reg.Get(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		charsBefore[c.ID] = got
	}
	before := shot(t, w, master, dev, rev, qa)
	if len(before.waits) != 6 {
		t.Fatalf("active waits before = %d", len(before.waits))
	}
	// --- the process stops; the machine is off for 30 minutes
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	clk2 := clock.NewFake(day0.Add(30 * time.Minute))
	db2 := open()
	t.Cleanup(func() { db2.Close() })
	w2 := newWorldOver(t, db2, clk2)
	if err := w2.in.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w2.svc.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	// the time wait that came while it was down is satisfied, and its wake is in
	// the inbox of who waited; the wait whose limit passed expired, with the notice
	if got, _ := w2.svc.Store().GetWait(ctx, until.ID); got.State != waits.Satisfied {
		t.Fatalf("the wait for a time that came during the fall is %s", got.State)
	}
	if got, _ := w2.svc.Store().GetWait(ctx, later.ID); got.State != waits.Active {
		t.Fatalf("the wait for a later time is %s", got.State)
	}
	wakes, expired := 0, 0
	for _, it := range w2.all(dev) {
		if it.Kind == inbox.KindWake && strings.Contains(it.Text, "Chegou o horário") {
			wakes++
		}
		if it.Kind == inbox.KindWake && strings.Contains(it.Text, "estourou o prazo") {
			expired++
		}
	}
	if wakes != 1 || expired != 1 {
		t.Fatalf("the waiter got %d wakes of the time and %d notices of the limit, want 1 and 1: %+v", wakes, expired, w2.all(dev))
	}
	// the character mid-work is as it was, and the outbox keeps its items in order
	for id, was := range charsBefore {
		got, err := w2.reg.Get(ctx, id)
		if err != nil || got.State != was.State || got.Reason != was.Reason || got.Name != was.Name || got.TerminalID != was.TerminalID {
			t.Errorf("character %s after the restart = %+v, %v; was %+v", was.Name, got, err, was)
		}
	}
	pending, err := protocol.NewSQLStore(db2).Pending(ctx, "m1")
	if err != nil {
		t.Fatal(err)
	}
	var gotOut []string
	for _, e := range pending {
		gotOut = append(gotOut, e.ID)
	}
	if !slices.Equal(gotOut, wantOut) {
		t.Errorf("the outbox after the restart = %q, want %q", gotOut, wantOut)
	}
	// what is left comes back equal: the same waits with the same limits, and the
	// queues in the same order, nothing lost and nothing twice
	after := shot(t, w2, master, rev, qa)
	var kept []string
	for _, line := range before.waits {
		if !strings.HasPrefix(line, "w2|") && !strings.HasPrefix(line, "w5|") {
			kept = append(kept, line)
		}
	}
	if !slices.Equal(after.waits, kept) {
		t.Fatalf("waits after the restart:\n%q\nwant\n%q", after.waits, kept)
	}
	if !slices.Equal(after.queue["Mestre"], before.queue["Mestre"]) {
		t.Fatalf("the queue of the master changed: %q, was %q", after.queue["Mestre"], before.queue["Mestre"])
	}
	// the item that was being typed goes back to the queue (and is not typed
	// again while the Revisor works)
	if got := w2.all(rev); len(got) != 1 || got[0].State != inbox.Queued || got[0].ID != inflight[0].ID {
		t.Fatalf("the item that was being delivered is %+v", got)
	}
	if n := len(w2.deliv.typedTo(rev)); n != 0 {
		t.Fatalf("typed %d in the Revisor right after the restart: it is working", n)
	}
	// the asks: the one with a caller is no longer blocking, and both stay open
	for _, id := range []string{open1.ID, blocked.ID} {
		a, err := w2.svc.Store().GetAsk(ctx, id)
		if err != nil || a.State != waits.AskOpen || a.Blocking {
			t.Fatalf("ask %s after the restart: %+v, %v", id, a, err)
		}
	}
	// the timers are armed again: the wait of the master ends when its limit comes
	clk2.Advance(30*time.Minute + time.Second) // 60 min after the start
	if got, _ := w2.svc.Store().GetWait(ctx, "w1"); got.State != waits.Expired {
		t.Fatalf("the wait that had 60 minutes is %s at 60 minutes", got.State)
	}
	// and a second Recover changes nothing (a restart that falls again)
	count := len(w2.all(master)) + len(w2.all(dev))
	if err := w2.svc.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(w2.all(master)) + len(w2.all(dev)); n != count {
		t.Errorf("Recover again queued %d more items", n-count)
	}
	// the late answer of the ask that was open comes to the inbox of the asker, whole
	if back, err := w2.svc.Ask(ctx, waits.AskRequest{From: rev, To: dev, Prompt: "revisado: ok"}); err != nil || back.Status != waits.StatusDelivered {
		t.Fatalf("ask back after the restart = %+v, %v", back, err)
	}
	found := false
	for _, it := range w2.all(dev) {
		found = found || (it.Kind == inbox.KindAnswer && it.Refs.Ask == open1.ID && strings.Contains(it.Text, "revisado: ok"))
	}
	if !found {
		t.Errorf("the answer did not come to the inbox: %+v", w2.all(dev))
	}
}
