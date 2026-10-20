package protocol_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/store/guild"
	"github.com/lucasndas/mestre/internal/store/machine"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
)

type opener func(ctx context.Context, path string) (*store.DB, error)

// The databases are migrated once, by TestMain, outside the time of every
// test (creating and migrating a file costs tens of milliseconds on a Windows
// runner); a test starts from a copy. The schema itself is tested in
// internal/store.
var guildTemplate, machineTemplate []byte

func TestMain(m *testing.M) {
	var err error
	if guildTemplate, machineTemplate, err = buildTemplates(); err != nil {
		fmt.Fprintln(os.Stderr, "protocol: templates:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func buildTemplates() (g, m []byte, err error) {
	dir, err := os.MkdirTemp("", "protocol-template")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)
	ctx := context.Background()
	gdb, err := guildtpl.Open(ctx, filepath.Join(dir, "mestre.db"), store.Options{StmtCache: guildtpl.StmtCache, Synchronous: "OFF"})
	if err != nil {
		return nil, nil, err
	}
	if err := gdb.Close(); err != nil {
		return nil, nil, err
	}
	mdb, err := machine.Open(ctx, filepath.Join(dir, "maquina.db"), store.Options{StmtCache: guildtpl.StmtCache, Synchronous: "OFF"})
	if err != nil {
		return nil, nil, err
	}
	if err := mdb.Close(); err != nil {
		return nil, nil, err
	}
	if g, err = os.ReadFile(filepath.Join(dir, "mestre.db")); err != nil {
		return nil, nil, err
	}
	m, err = os.ReadFile(filepath.Join(dir, "maquina.db"))
	return g, m, err
}

// seedDB writes a migrated database at path, if there is none, and opens it
// without flushes: nothing here is about durability.
func seedDB(ctx context.Context, path string, template []byte, open func(context.Context, string, store.Options) (*store.DB, error)) (*store.DB, error) {
	if _, err := os.Stat(path); err != nil {
		if err := os.WriteFile(path, template, 0o600); err != nil {
			return nil, err
		}
	}
	return open(ctx, path, store.Options{StmtCache: guildtpl.StmtCache, Synchronous: "OFF"})
}

// The behavior is tested on the machine database; the Guild database has the
// same tables from its own migration, which a smoke test checks.
var databases = map[string]opener{
	"machine database": func(ctx context.Context, path string) (*store.DB, error) {
		return seedDB(ctx, path, machineTemplate, machine.Open)
	},
}

func TestTheGuildDatabaseHasTheSameProtocolTables(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, err := seedDB(ctx, filepath.Join(t.TempDir(), "mestre.db"), guildTemplate, guild.Open)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := protocol.NewSQLStore(db)
	if err := s.Put(ctx, "tower1", envelope("01J9Z0000000000000000000Q1", protocol.TypeGuildConfig)); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Pending(ctx, "tower1"); err != nil || len(got) != 1 {
		t.Fatalf("Pending = %v, %v", got, err)
	}
	if fresh, _, err := s.Claim(ctx, "tower1", "x", "t", time.Time{}); err != nil || !fresh {
		t.Fatalf("Claim = %v, %v", fresh, err)
	}
}

func envelope(id, typ string) protocol.Envelope {
	return protocol.Envelope{ID: id, Type: typ, TS: protocol.Timestamp{Time: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)},
		Payload: []byte(`{"n":1}`), Ack: true}
}

func TestTheOutboxKeepsOrderAndSurvivesReopeningTheDatabase(t *testing.T) {
	t.Parallel()
	for name, open := range databases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			path := filepath.Join(t.TempDir(), "db")
			db, err := open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			s := protocol.NewSQLStore(db)
			ids := []string{"01J9Z0000000000000000000Q1", "01J9Z0000000000000000000Q2", "01J9Z0000000000000000000Q3"}
			for _, id := range ids {
				if err := s.Put(ctx, "citadel", envelope(id, protocol.TypeMachineSheet)); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Put(ctx, "citadel", envelope(ids[0], protocol.TypeMachineSheet)); err != nil { // the same id again
				t.Fatal(err)
			}
			if err := s.Put(ctx, "other", envelope("01J9Z0000000000000000000Q4", protocol.TypeMachineSheet)); err != nil {
				t.Fatal(err)
			}
			if err := s.Done(ctx, "citadel", ids[1]); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			db, err = open(ctx, path) // the machine restarted
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			got, err := protocol.NewSQLStore(db).Pending(ctx, "citadel")
			if err != nil || len(got) != 2 || got[0].ID != ids[0] || got[1].ID != ids[2] {
				t.Fatalf("Pending = %v, %v; want %s and %s in order", got, err, ids[0], ids[2])
			}
			if got[0].Type != protocol.TypeMachineSheet || !got[0].Ack || string(got[0].Payload) != `{"n":1}` {
				t.Fatalf("envelope = %+v", got[0])
			}
		})
	}
}

func TestTheProcessedCommandsSurviveReopeningAndAreForgottenAfterTheTTL(t *testing.T) {
	t.Parallel()
	for name, open := range databases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			path := filepath.Join(t.TempDir(), "db")
			db, err := open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			s := protocol.NewSQLStore(db)
			t0 := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
			fresh, prev, err := s.Claim(ctx, "tower1", "id1", "terminal.input", t0)
			if err != nil || !fresh || prev != nil {
				t.Fatalf("first claim = %v, %q, %v", fresh, prev, err)
			}
			fresh, prev, err = s.Claim(ctx, "tower1", "id1", "terminal.input", t0)
			if err != nil || fresh || prev != nil {
				t.Fatalf("a claim that did not finish = %v, %q, %v; want taken, no ack yet", fresh, prev, err)
			}
			if err := s.Finish(ctx, "tower1", "id1", []byte(`{"status":"ok"}`)); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.Claim(ctx, "tower1", "id2", "terminal.input", t0.Add(23*time.Hour)); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.Claim(ctx, "tower2", "id1", "terminal.input", t0); err != nil { // another sender, the same id
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			db, err = open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			s = protocol.NewSQLStore(db)
			fresh, prev, err = s.Claim(ctx, "tower1", "id1", "terminal.input", t0.Add(time.Hour))
			if err != nil || fresh || string(prev) != `{"status":"ok"}` {
				t.Fatalf("after a restart = %v, %q, %v; want the same answer as before", fresh, prev, err)
			}
			if err := s.Release(ctx, "tower2", "id1"); err != nil {
				t.Fatal(err)
			}
			if fresh, _, _ := s.Claim(ctx, "tower2", "id1", "terminal.input", t0); !fresh {
				t.Fatal("a released claim must run again")
			}
			// 23 h after t0, below a floor above every id: only tower1/id1 is
			// older than that (id2 is at 23 h, and tower2 is another sender)
			n, err := s.Prune(ctx, "tower1", t0.Add(23*time.Hour), "zz")
			if err != nil || n != 1 {
				t.Fatalf("Prune = %d, %v; want 1", n, err)
			}
			// the floor of tower2 is its id1: the sender may still send it again
			if n, err := s.Prune(ctx, "tower2", t0.Add(23*time.Hour), "id1"); err != nil || n != 0 {
				t.Fatalf("Prune below the floor = %d, %v; want 0", n, err)
			}
			if fresh, _, _ := s.Claim(ctx, "tower2", "id1", "terminal.input", t0.Add(25*time.Hour)); fresh {
				t.Fatal("an id at the floor of its sender was forgotten")
			}
			if fresh, _, _ := s.Claim(ctx, "tower1", "id1", "terminal.input", t0.Add(25*time.Hour)); !fresh {
				t.Fatal("an id older than the TTL is forgotten")
			}
			if fresh, _, _ := s.Claim(ctx, "tower1", "id2", "terminal.input", t0.Add(25*time.Hour)); fresh {
				t.Fatal("an id newer than the TTL was forgotten")
			}
		})
	}
}

// What the Tower sent but never saw confirmed is still there after the machine
// restarts, and goes out, with the same id, on the first connection.
func TestTheOutboxOfATowerSurvivesARestartAndGoesOutOnTheNextConnection(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), "maquina.db")
		w := newWorld(t, opts{})

		// first life of the Tower: it queues an event while the Citadel is away
		db, err := seedDB(ctx, path, machineTemplate, machine.Open)
		if err != nil {
			t.Fatal(err)
		}
		w.cfgT.Outbox = protocol.NewSQLStore(db)
		w.cfgT.Processed = protocol.NewSQLStore(db)
		first, err := protocol.NewClient(w.cfgT, w.dialer())
		if err != nil {
			t.Fatal(err)
		}
		id, err := first.Link().Send(ctx, protocol.Outgoing{Type: protocol.TypeTerminalStatus, Payload: protocol.TerminalStatus{TerminalID: "t1", State: "idle"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil { // the machine goes down
			t.Fatal(err)
		}

		// second life: a new process over the same database
		db, err = seedDB(ctx, path, machineTemplate, machine.Open)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		w.cfgT.Outbox = protocol.NewSQLStore(db)
		w.cfgT.Processed = protocol.NewSQLStore(db)
		got := make(chan string, 4)
		w.cfgC.Router.HandleFunc(protocol.TypeTerminalStatus, func(_ context.Context, m *protocol.Message) (*protocol.Reply, error) {
			got <- m.Env.ID
			return nil, nil
		})
		w.start()
		defer w.stop()
		settle()
		select {
		case g := <-got:
			if g != id {
				t.Fatalf("the Citadel got %s, want the id the Tower queued before it restarted: %s", g, id)
			}
		default:
			t.Fatal("the queued event was lost in the restart")
		}
		pending, _ := protocol.NewSQLStore(db).Pending(ctx, protocol.CitadelKey)
		if len(pending) != 0 {
			t.Fatalf("outbox still has %d after the ack", len(pending))
		}
	})
}

// A Citadel that restarted still knows which commands it ran: the repeat gets
// "duplicate" and nothing runs twice.
func TestTheProcessedCommandsOfACitadelSurviveItsRestart(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		path := filepath.Join(t.TempDir(), "mestre.db")
		db, err := seedDB(ctx, path, guildTemplate, guild.Open)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		w := newWorld(t, opts{})
		w.cfgC.Outbox = protocol.NewSQLStore(db)
		w.cfgC.Processed = protocol.NewSQLStore(db)
		runs := 0
		w.cfgC.Router.HandleFunc(protocol.TypeTerminalStatus, func(context.Context, *protocol.Message) (*protocol.Reply, error) {
			runs++
			return nil, nil
		})
		w.start()
		defer w.stop()
		settle()

		// the Citadel runs the event, but the ack is lost: the Tower keeps it
		w.dropAcks.Store(false)
		w.citadelDropAcks.Store(true)
		if _, err := w.client.Link().Send(ctx, protocol.Outgoing{Type: protocol.TypeTerminalStatus, Payload: protocol.TerminalStatus{TerminalID: "t1", State: "idle"}}); err != nil {
			t.Fatal(err)
		}
		settle()
		if runs != 1 || w.partsT.Store.Waiting(protocol.CitadelKey) != 1 {
			t.Fatalf("runs = %d, waiting = %d", runs, w.partsT.Store.Waiting(protocol.CitadelKey))
		}

		// the Citadel restarts over the same database; the Tower reconnects and sends again
		w.citadelDropAcks.Store(false)
		w.restartCitadel()
		advance(2 * time.Minute)
		if runs != 1 {
			t.Fatalf("the event ran %d times", runs)
		}
		if n := w.partsT.Store.Waiting(protocol.CitadelKey); n != 0 {
			t.Fatalf("the Tower still waits for %d acks", n)
		}
	})
}
