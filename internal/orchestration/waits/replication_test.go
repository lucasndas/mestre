package waits_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/scrolls"
	"github.com/lucasndas/mestre/internal/orchestration/waits"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
)

// newTables are the tables of the inbox, the waits and the scrolls: the
// Citadel writes them and every Tower keeps the same rows.
var newTables = []string{"inbox_messages", "outbound_messages", "waits", "asks", "wait_cycles", "notes", "tomes", "note_counters"}

func dump(t *testing.T, db *store.DB, tables ...string) string {
	t.Helper()
	var b strings.Builder
	for _, tb := range tables {
		rows, err := db.Reader().QueryContext(context.Background(), `SELECT * FROM `+tb+` ORDER BY 1, 2`)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		fmt.Fprintf(&b, "== %s\n", tb)
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "%v\n", vals)
		}
		rows.Close()
	}
	return b.String()
}

func ship(t *testing.T, from, to *store.DB) {
	t.Helper()
	ctx := context.Background()
	have, err := to.LastLSN(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := from.LogSince(ctx, have, 1<<20, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := to.ApplyLog(ctx, rows); err != nil {
		t.Fatal(err)
	}
}

// Replication of the new tables: what the Citadel writes in them reaches a
// Tower, row by row, by the change log, both in the full copy that a Tower
// starts from and in the changes that follow.
func TestTheTablesOfTheInboxTheWaitsAndTheScrollsReplicateToATower(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ctx := w.ctx
	// the Tower starts from a full copy of the Citadel, taken before anything is written
	snap := filepath.Join(t.TempDir(), "snap.db")
	lsn, err := w.db.Snapshot(ctx, snap, store.SnapshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	tower, err := guilddb.Open(ctx, filepath.Join(t.TempDir(), "tower.db"), store.Options{Replica: true, Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	defer tower.Close()
	if err := tower.Replace(ctx, snap, lsn); err != nil {
		t.Fatal(err)
	}
	// ... and then the Citadel works with every one of the tables
	master := w.add(characters.RoleTableMaster, "42-login", characters.NameTableMaster)
	a := w.dev("42-login", "A", master)
	b := w.dev("42-login", "B", a, master)
	gm := w.gm()
	w.set(a, characters.Busy)
	w.enqueue(inbox.NewItem{Target: a.ID, Kind: inbox.KindAgent, Sender: "B", Text: "olá",
		Origin: nil, Refs: inbox.Refs{Table: "42-login"}, Attachments: []string{"/w/.mestre/anexos/x.png"}})
	w.enqueue(inbox.NewItem{Target: gm.ID, Kind: inbox.KindUser, Sender: "Lucas", Text: "oi", Origin: &inbox.Origin{Channel: inbox.ChannelTelegram, User: "lucas", UserName: "Lucas", Level: characters.LevelOwner}})
	if _, err := w.in.Reply(ctx, inbox.ReplyRequest{From: gm, Text: "resposta"}); err != nil { // no channel: kept
		t.Fatal(err)
	}
	if _, err := w.svc.WaitFor(ctx, waits.WaitRequest{Waiter: a, Names: []string{"B"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.WaitFor(ctx, waits.WaitRequest{Waiter: b, Names: []string{"A"}}); err != nil { // a deadlock: wait_cycles
		t.Fatal(err)
	}
	if _, err := w.svc.Ask(ctx, waits.AskRequest{From: b, To: master, Prompt: "x", NoWait: true}); err != nil {
		t.Fatal(err)
	}
	sc, err := scrolls.NewService(scrolls.Options{Store: &scrolls.Store{DB: w.db}, Clock: w.clk, Registry: w.reg, Principal: func(ws string) string { return filepath.Join(t.TempDir(), ws) }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sc.Create(ctx, scrolls.CreateRequest{Workspace: workspace, Table: "42-login", Actor: scrolls.Engine, Content: "# Plano", Stack: "Docs"}); err != nil {
		t.Fatal(err)
	}
	for _, tb := range newTables {
		if n := strings.Count(dump(t, w.db, tb), "\n"); n < 2 {
			t.Fatalf("the test left %s empty", tb)
		}
	}
	ship(t, w.db, tower)
	if got, want := dump(t, tower, newTables...), dump(t, w.db, newTables...); got != want {
		t.Fatalf("the Tower differs from the Citadel:\n%s\nwant\n%s", got, want)
	}
	// the changes that follow travel too: a wait that ends, an item delivered
	w.done(b, "pronto")
	w.set(a, characters.Idle)
	ship(t, w.db, tower)
	if got, want := dump(t, tower, newTables...), dump(t, w.db, newTables...); got != want {
		t.Fatalf("the Tower differs from the Citadel after the changes:\n%s\nwant\n%s", got, want)
	}
}
