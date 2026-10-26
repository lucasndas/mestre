package protocol_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/testkit/protorig"
)

// The outbox and the processed ids of the Citadel live in the database of
// the Guild, which is replicated: a promoted Citadel sends again, with the
// same ids, what the old one left without an ack. The Tower keeps the ids for
// "the Citadel", not for one machine, so it knows them.
func TestACommandSentAgainByAPromotedCitadelIsNotRunAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		ty := &typist{}
		w.cfgT.Router.Handle(protocol.TypeTerminalInput, ty)
		w.start()
		defer w.stop()
		settle()
		w.dropAcks.Store(true) // the command runs, its ack is lost
		if _, err := w.toTower().Send(t.Context(), input("deploy\n")); err != nil {
			t.Fatal(err)
		}
		settle()
		if got := ty.list(); len(got) != 1 {
			t.Fatalf("typed = %q, want one run", got)
		}

		// The old Citadel goes away; another machine is promoted, on epoch 2,
		// with a copy of its database.
		w.cancelServer()
		w.wgServer.Wait()
		copied := w.partsC.Store.Clone()
		w.cfgC, w.partsC = protorig.Config(w.clk, "citadel2", 2, "citadel2")
		w.cfgC.Outbox, w.cfgC.Processed, w.partsC.Store = copied, copied, copied
		w.dropAcks.Store(false)
		w.restartCitadel()
		advance(2 * time.Minute)

		if got := w.partsT.Epochs.Followed(); len(got) != 1 || got[0] != 2 {
			t.Fatalf("the Tower followed %v, want the epoch of the promoted Citadel", got)
		}
		if got := ty.list(); len(got) != 1 {
			t.Fatalf("typed = %q: the command ran again under the new Citadel", got)
		}
		if n := copied.Waiting(towerID); n != 0 {
			t.Fatalf("the outbox of the promoted Citadel still has %d", n)
		}
		if n := w.partsT.Store.Remembered(protocol.CitadelKey); n != 1 {
			t.Fatalf("the Tower remembers %d ids of the Citadel, want 1", n)
		}
	})
}

// A message read on epoch 1 waits behind a slow handler; meanwhile the Tower
// learns epoch 2. When its turn comes it is refused (stale_epoch, 4006) and
// does not run.
func TestAMessageOfAnOldEpochThatWaitedBehindASlowHandlerIsRefused(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		release := make(chan struct{})
		var mu sync.Mutex
		var ran []string
		w.cfgT.Router.HandleFunc(protocol.TypeTerminalInput, func(_ context.Context, m *protocol.Message) (*protocol.Reply, error) {
			in := m.Payload.(*protocol.TerminalInput)
			if in.Data == "slow" {
				<-release
			}
			mu.Lock()
			ran = append(ran, in.Data)
			mu.Unlock()
			return nil, nil
		})
		ended := make(chan error, 8)
		w.cfgC.Hooks.OnDisconnect = func(_ *protocol.Session, cause error) { ended <- cause }
		w.start()
		defer w.stop()
		settle()
		if _, err := w.toTower().Send(t.Context(), input("slow")); err != nil {
			t.Fatal(err)
		}
		settle()
		result := make(chan protocol.AckResult, 1)
		go func() {
			r, _ := w.toTower().Call(t.Context(), input("rm -rf\n"))
			result <- r
		}()
		settle() // read on epoch 1, queued behind the slow one
		w.partsT.Epochs.Set(protorig.Cert(2, "other", protorig.SignedOK))
		close(release)
		settle()

		mu.Lock()
		got := append([]string(nil), ran...)
		mu.Unlock()
		if len(got) != 1 || got[0] != "slow" {
			t.Fatalf("ran = %q: the message of the old epoch ran", got)
		}
		if r := <-result; r.Status != protocol.AckError || r.Error == nil || r.Error.Code != protocol.CodeStaleEpoch {
			t.Fatalf("answer = %+v, want stale_epoch", r)
		}
		var ce *protocol.CloseError
		if cause := <-ended; !errors.As(cause, &ce) || ce.Code != protocol.CloseStaleEpoch {
			t.Fatalf("the connection ended with %v, want 4006", cause)
		}
		if w.partsT.Store.Claims() != 1 {
			t.Fatalf("claimed %d ids: the refused message must not be claimed", w.partsT.Store.Claims())
		}
	})
}

// Whether a message is run once is the catalogue's to say: a peer that
// leaves out the ack flag of a type that demands one still gets an ack and
// cannot make it run twice.
func TestTheCatalogueAndNotTheEnvelopeSaysAMessageRunsOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		var runs atomic.Int32
		w.cfgC.Router.HandleFunc(protocol.TypeTerminalStatus, func(context.Context, *protocol.Message) (*protocol.Reply, error) {
			runs.Add(1)
			return nil, nil
		})
		w.startServer()
		defer w.stop()
		settle()
		conn := rawTower(t, w)
		defer conn.Close(1000, "")
		openRaw(t, w, conn, protocol.DefaultRange())
		env := protocol.Envelope{ID: "01J9Z0000000000000000000Q1", Type: protocol.TypeTerminalStatus, Payload: json.RawMessage(`{"terminal_id":"t1","state":"idle"}`)}
		for _, want := range []string{protocol.AckOK, protocol.AckDuplicate} {
			sendRaw(t, w, conn, env) // ack: false
			got := recvRaw(t, conn)
			var a protocol.Ack
			_ = json.Unmarshal(got.Payload, &a)
			if got.Type != protocol.TypeAck || got.ReplyTo != env.ID || a.Status != want {
				t.Fatalf("answer = %s %s, want the ack %s", got.Type, got.Payload, want)
			}
		}
		if runs.Load() != 1 {
			t.Fatalf("ran %d times", runs.Load())
		}
	})
}

// After a reconnection, what waits for an ack goes in the order it was
// sent, whether it waited in the outbox or, for a volatile type, in memory.
func TestVolatileAndDurableMessagesAreSentAgainInTheirOriginalOrder(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		var mu sync.Mutex
		var order []string
		record := func(s string) {
			mu.Lock()
			order = append(order, s)
			mu.Unlock()
		}
		w.cfgT.Router.HandleFunc(protocol.TypeTerminalInput, func(_ context.Context, m *protocol.Message) (*protocol.Reply, error) {
			record(m.Payload.(*protocol.TerminalInput).Data)
			return nil, nil
		})
		w.cfgT.Router.HandleFunc(protocol.TypeAgentSiteLogin, func(context.Context, *protocol.Message) (*protocol.Reply, error) {
			record("login")
			return nil, nil
		})
		w.start()
		defer w.stop()
		settle()
		w.net.Partition(towerHost, citadelID)
		settle()
		login := protocol.Outgoing{Type: protocol.TypeAgentSiteLogin, Payload: protocol.AgentSiteLogin{
			AttemptID: "x", AgentID: "a", Domains: []string{"example.com"}, LoginURL: "https://example.com", Username: "u", Secret: "hunter2",
		}}
		for _, o := range []protocol.Outgoing{input("a"), login, input("b")} {
			if _, err := w.toTower().Send(t.Context(), o); err != nil {
				t.Fatal(err)
			}
		}
		w.net.Heal(towerHost, citadelID)
		advance(2 * time.Minute)
		mu.Lock()
		defer mu.Unlock()
		if len(order) != 3 || order[0] != "a" || order[1] != "login" || order[2] != "b" {
			t.Fatalf("order = %q, want a, login, b", order)
		}
	})
}

// While a newer connection replaces an older one whose handler is still
// running, the messages of the machine still run one at a time.
func TestTheHandlersOfAMachineRunOneAtATimeAcrossAReplacement(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		release := make(chan struct{})
		var running, most atomic.Int32
		var ran sync.Map
		w.cfgC.Router.HandleFunc(protocol.TypeTerminalStatus, func(_ context.Context, m *protocol.Message) (*protocol.Reply, error) {
			n := running.Add(1)
			defer running.Add(-1)
			if n > most.Load() {
				most.Store(n)
			}
			id := m.Payload.(*protocol.TerminalStatus).TerminalID
			if id == "first" {
				<-release
			}
			ran.Store(id, true)
			return nil, nil
		})
		w.startServer()
		defer w.stop()
		settle()
		status := func(c protocol.Conn, id, term string) {
			sendRaw(t, w, c, protocol.Envelope{ID: id, Type: protocol.TypeTerminalStatus, Ack: true,
				Payload: json.RawMessage(`{"terminal_id":"` + term + `","state":"idle"}`)})
		}
		older := rawTower(t, w)
		defer older.Close(1000, "")
		openRaw(t, w, older, protocol.DefaultRange())
		status(older, "01J9Z0000000000000000000R1", "first")
		settle()
		newer := rawTower(t, w)
		defer newer.Close(1000, "")
		openRaw(t, w, newer, protocol.DefaultRange())
		status(newer, "01J9Z0000000000000000000R2", "second")
		settle()
		if _, ok := ran.Load("second"); ok {
			t.Fatal("the message of the newer connection ran while the older one's was still running")
		}
		close(release)
		settle()
		if _, ok := ran.Load("second"); !ok {
			t.Fatal("the message of the newer connection never ran")
		}
		if most.Load() != 1 {
			t.Fatalf("%d handlers ran at once", most.Load())
		}
	})
}
