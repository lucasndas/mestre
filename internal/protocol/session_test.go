package protocol_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/protocol"
)

// typist is the handler of terminal.input on a Tower: it records what was
// typed.
type typist struct {
	mu    sync.Mutex
	typed []string
}

func (ty *typist) Handle(_ context.Context, m *protocol.Message) (*protocol.Reply, error) {
	in := m.Payload.(*protocol.TerminalInput)
	ty.mu.Lock()
	ty.typed = append(ty.typed, in.Data)
	ty.mu.Unlock()
	return nil, nil
}

func (ty *typist) list() []string {
	ty.mu.Lock()
	defer ty.mu.Unlock()
	return append([]string(nil), ty.typed...)
}

func input(data string) protocol.Outgoing {
	return protocol.Outgoing{Type: protocol.TypeTerminalInput, Payload: protocol.TerminalInput{TerminalID: "t1", Mode: protocol.InputMessage, Data: data}}
}

// CA-PROTO-01
func TestACommandRepeatedAfterReconnectingIsNotRunAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		ty := &typist{}
		w.cfgT.Router.Handle(protocol.TypeTerminalInput, ty)
		w.start()
		defer w.stop()
		settle()
		if !w.toTower().Connected() {
			t.Fatal("the Tower did not connect")
		}

		// The Tower runs the command, but its ack is lost on the way: the
		// Citadel keeps the command in its outbox.
		w.dropAcks.Store(true)
		result := make(chan protocol.AckResult, 1)
		id := "01J9Z0000000000000000000A1"
		go func() {
			o := input("ls\n")
			o.ID = id
			r, err := w.toTower().Call(t.Context(), o)
			if err != nil {
				t.Error(err)
			}
			result <- r
		}()
		settle()
		if got := ty.list(); len(got) != 1 {
			t.Fatalf("typed = %q, want one run", got)
		}
		if n := w.partsC.Store.Waiting(towerID); n != 1 {
			t.Fatalf("outbox of the Citadel has %d, want the unconfirmed command", n)
		}

		// The connection drops and comes back: the command is sent again with
		// the same id, and the answer is "duplicate".
		w.dropAcks.Store(false)
		w.net.Cut(towerHost, citadelID)
		advance(5 * time.Second)
		select {
		case r := <-result:
			if r.Status != protocol.AckDuplicate {
				t.Fatalf("ack = %+v, want duplicate", r)
			}
		default:
			t.Fatal("no ack after reconnecting")
		}
		if got := ty.list(); len(got) != 1 {
			t.Fatalf("typed = %q: the command ran again", got)
		}
		if n := w.partsC.Store.Waiting(towerID); n != 0 {
			t.Fatalf("outbox of the Citadel still has %d", n)
		}
	})
}

func TestAnAckedCommandRunsOnceAndIsConfirmed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		ty := &typist{}
		w.cfgT.Router.Handle(protocol.TypeTerminalInput, ty)
		w.start()
		defer w.stop()
		settle()
		r, err := w.toTower().Call(t.Context(), input("echo\n"))
		if err != nil || r.Status != protocol.AckOK {
			t.Fatalf("Call = %+v, %v", r, err)
		}
		if got := ty.list(); len(got) != 1 || got[0] != "echo\n" {
			t.Fatalf("typed = %q", got)
		}
		if n := w.partsC.Store.Waiting(towerID); n != 0 {
			t.Fatalf("outbox has %d after the ack", n)
		}
	})
}

func TestAMessageSentWhileDisconnectedWaitsInTheOutboxAndGoesInOrder(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		ty := &typist{}
		w.cfgT.Router.Handle(protocol.TypeTerminalInput, ty)
		w.start()
		defer w.stop()
		settle()
		w.net.Partition(towerHost, citadelID)
		settle()
		for _, s := range []string{"a", "b", "c"} {
			if _, err := w.toTower().Send(t.Context(), input(s)); err != nil {
				t.Fatal(err)
			}
		}
		if n := w.partsC.Store.Waiting(towerID); n != 3 {
			t.Fatalf("outbox has %d, want 3", n)
		}
		w.net.Heal(towerHost, citadelID)
		advance(2 * time.Minute)
		if got := ty.list(); len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
			t.Fatalf("typed = %q, want a b c in order", got)
		}
		if n := w.partsC.Store.Waiting(towerID); n != 0 {
			t.Fatalf("outbox has %d after the acks", n)
		}
	})
}

func TestARetryableRefusalCanRunWhenSentAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		busy := true
		var runs int
		w.cfgT.Router.HandleFunc(protocol.TypeTerminalInput, func(context.Context, *protocol.Message) (*protocol.Reply, error) {
			runs++
			if busy {
				return nil, protocol.NewError(protocol.CodeBusyTerminal, "busy")
			}
			return nil, nil
		})
		w.start()
		defer w.stop()
		settle()
		o := input("x")
		o.ID = "01J9Z0000000000000000000B1"
		r, err := w.toTower().Call(t.Context(), o)
		if err != nil || r.Status != protocol.AckError || r.Error.Code != protocol.CodeBusyTerminal || !r.Error.Retryable {
			t.Fatalf("first = %+v, %v", r, err)
		}
		busy = false
		r, err = w.toTower().Call(t.Context(), o) // the same id, the same message
		if err != nil || r.Status != protocol.AckOK || runs != 2 {
			t.Fatalf("second = %+v, %v, runs = %d", r, err, runs)
		}
	})
}

func TestAFinalRefusalIsRememberedAsDuplicateWithTheSameError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		runs := 0
		w.cfgT.Router.HandleFunc(protocol.TypeTerminalInput, func(context.Context, *protocol.Message) (*protocol.Reply, error) {
			runs++
			return nil, protocol.NewError(protocol.CodeNotFound, "no terminal t1")
		})
		w.start()
		defer w.stop()
		settle()
		o := input("x")
		o.ID = "01J9Z0000000000000000000C1"
		r, _ := w.toTower().Call(t.Context(), o)
		if r.Status != protocol.AckError || r.Error.Code != protocol.CodeNotFound {
			t.Fatalf("first = %+v", r)
		}
		r, _ = w.toTower().Call(t.Context(), o)
		if r.Status != protocol.AckDuplicate || r.Error == nil || r.Error.Code != protocol.CodeNotFound || runs != 1 {
			t.Fatalf("second = %+v, runs = %d", r, runs)
		}
	})
}

// The ack means "received and accepted"; the result comes in a message of its
// own, linked by reply_to (docs/conectividade/protocolo.md#tipos-de-mensagem).
func TestTheResultIsAnotherMessageLinkedByReplyTo(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		w.cfgT.Router.HandleFunc(protocol.TypeTerminalOpen, func(_ context.Context, m *protocol.Message) (*protocol.Reply, error) {
			open := m.Payload.(*protocol.TerminalOpen)
			return &protocol.Reply{Type: protocol.TypeTerminalOpened, Payload: protocol.TerminalOpened{TerminalID: open.TerminalID, PID: 4242}}, nil
		})
		type opened struct {
			replyTo string
			pid     int
		}
		got := make(chan opened, 1)
		w.cfgC.Router.HandleFunc(protocol.TypeTerminalOpened, func(_ context.Context, m *protocol.Message) (*protocol.Reply, error) {
			got <- opened{m.Env.ReplyTo, m.Payload.(*protocol.TerminalOpened).PID}
			return nil, nil
		})
		w.start()
		defer w.stop()
		settle()
		o := protocol.Outgoing{Type: protocol.TypeTerminalOpen, Payload: protocol.TerminalOpen{TerminalID: "t1", AgentID: "a1", Command: "claude"}}
		o.ID = "01J9Z0000000000000000000D1"
		r, err := w.toTower().Call(t.Context(), o)
		if err != nil || r.Status != protocol.AckOK {
			t.Fatalf("Call = %+v, %v", r, err)
		}
		settle()
		select {
		case g := <-got:
			if g.replyTo != o.ID || g.pid != 4242 {
				t.Fatalf("result = %+v", g)
			}
		default:
			t.Fatal("no terminal.opened")
		}
		if w.partsT.Store.Waiting(citadelID) != 0 {
			t.Fatal("the result was not acked")
		}
	})
}

func TestAnUnknownTypeGetsAnErrorAndTheConnectionStays(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		w.startServer()
		defer w.stop()
		settle()
		conn := rawTower(t, w)
		defer conn.Close(1000, "")
		openRaw(t, w, conn, protocol.DefaultRange())
		sendRaw(t, w, conn, protocol.Envelope{ID: "01J9Z0000000000000000000E1", Type: "nothing.here", Payload: json.RawMessage(`{}`)})
		e := recvRaw(t, conn)
		var pe protocol.Error
		_ = json.Unmarshal(e.Payload, &pe)
		if e.Type != protocol.TypeError || pe.Code != protocol.CodeUnknownType || e.ReplyTo != "01J9Z0000000000000000000E1" {
			t.Fatalf("answer = %+v %+v", e, pe)
		}
		// still open: a heartbeat is taken
		sendRaw(t, w, conn, protocol.Envelope{ID: "01J9Z0000000000000000000E2", Type: protocol.TypeMachineHeartbeat,
			Payload: json.RawMessage(`{"cpu_pct":1,"mem_pct":2,"disk_free_bytes":3,"terminals":0}`)})
		settle()
	})
}

func TestAMessageTooBigAndAMalformedPayloadAreRefusedWithoutDroppingTheConnection(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		w.startServer()
		defer w.stop()
		settle()
		conn := rawTower(t, w)
		defer conn.Close(1000, "")
		openRaw(t, w, conn, protocol.DefaultRange())

		big := make([]byte, protocol.MaxMessage+1)
		if err := conn.Write(t.Context(), protocol.FrameText, big); err != nil {
			t.Fatal(err)
		}
		if code := errorCode(t, recvRaw(t, conn)); code != protocol.CodeTooLarge {
			t.Fatalf("code = %s, want too_large", code)
		}
		sendRaw(t, w, conn, protocol.Envelope{ID: "01J9Z0000000000000000000F1", Type: protocol.TypeMachineHeartbeat, Payload: json.RawMessage(`{"cpu_pct":"high"}`)})
		if code := errorCode(t, recvRaw(t, conn)); code != protocol.CodeInvalidPayload {
			t.Fatalf("code = %s, want invalid_payload", code)
		}
		if err := conn.Write(t.Context(), protocol.FrameText, []byte("not json")); err != nil {
			t.Fatal(err)
		}
		if code := errorCode(t, recvRaw(t, conn)); code != protocol.CodeInvalidPayload {
			t.Fatalf("code = %s, want invalid_payload", code)
		}
	})
}

func TestAMessageForTheOtherDirectionIsForbidden(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		w.startServer()
		defer w.stop()
		settle()
		conn := rawTower(t, w)
		defer conn.Close(1000, "")
		openRaw(t, w, conn, protocol.DefaultRange())
		// terminal.open goes from the Citadel to the Tower, never the reverse
		sendRaw(t, w, conn, protocol.Envelope{ID: "01J9Z0000000000000000000G1", Type: protocol.TypeTerminalOpen, Ack: true,
			Payload: json.RawMessage(`{"terminal_id":"t","agent_id":"a","command":"sh"}`)})
		if code := errorCode(t, recvRaw(t, conn)); code != protocol.CodeForbidden {
			t.Fatalf("code = %s, want forbidden", code)
		}
	})
}

func TestAnEnvelopeThatClaimsAnotherMachineIsRefused(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		w.startServer()
		defer w.stop()
		settle()
		conn := rawTower(t, w)
		defer conn.Close(1000, "")
		openRaw(t, w, conn, protocol.DefaultRange())
		env := protocol.Envelope{ID: "01J9Z0000000000000000000H1", Type: protocol.TypeMachineSheet, Ack: true, Payload: json.RawMessage(`{}`),
			MachineID: "someone-else", Epoch: 1, TS: protocol.Timestamp{Time: w.clk.Now()}}
		b, _ := protocol.Encode(env)
		_ = conn.Write(t.Context(), protocol.FrameText, b)
		if code := errorCode(t, recvRaw(t, conn)); code != protocol.CodeForbidden {
			t.Fatalf("code = %s, want forbidden", code)
		}
	})
}

func TestAMessageWithoutAHandlerIsAnUnknownTypeForThatBuild(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		w.start()
		defer w.stop()
		settle()
		r, err := w.toTower().Call(t.Context(), input("x"))
		if err != nil || r.Status != protocol.AckError || r.Error.Code != protocol.CodeUnknownType {
			t.Fatalf("Call = %+v, %v", r, err)
		}
		if n := w.partsC.Store.Waiting(towerID); n != 0 {
			t.Fatalf("a final refusal must leave the outbox, has %d", n)
		}
	})
}

func TestAMessageThatBreaksTheSchemaIsNotSent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		w.start()
		defer w.stop()
		settle()
		_, err := w.toTower().Send(t.Context(), protocol.Outgoing{Type: protocol.TypeTerminalInput, Payload: protocol.TerminalInput{TerminalID: "t1", Mode: "weird", Data: "x"}})
		var pe *protocol.Error
		if !errors.As(err, &pe) || pe.Code != protocol.CodeInvalidPayload {
			t.Fatalf("err = %v, want invalid_payload", err)
		}
		_, err = w.toTower().Send(t.Context(), protocol.Outgoing{Type: "no.such", Payload: protocol.Empty{}})
		if !errors.As(err, &pe) || pe.Code != protocol.CodeUnknownType {
			t.Fatalf("err = %v, want unknown_type", err)
		}
		_, err = w.toTower().Send(t.Context(), protocol.Outgoing{Type: protocol.TypeHello, Payload: protocol.Empty{}})
		if !errors.As(err, &pe) || pe.Code != protocol.CodeForbidden {
			t.Fatalf("err = %v, want forbidden (the Citadel does not send hello)", err)
		}
	})
}

func TestAnUnackedMessageWithoutAConnectionFails(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		w.start()
		defer w.stop()
		settle()
		w.net.Partition(towerHost, citadelID)
		settle()
		_, err := w.toTower().Send(t.Context(), protocol.Outgoing{Type: protocol.TypeTerminalReplay, Payload: protocol.TerminalReplay{TerminalID: "t1"}})
		if !errors.Is(err, protocol.ErrNotConnected) {
			t.Fatalf("err = %v, want ErrNotConnected", err)
		}
	})
}

func TestTheSecretOfAVolatileTypeNeverReachesTheOutbox(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		w.start()
		defer w.stop()
		settle()
		w.net.Partition(towerHost, citadelID)
		settle()
		_, err := w.toTower().Send(t.Context(), protocol.Outgoing{Type: protocol.TypeAgentSiteLogin, Payload: protocol.AgentSiteLogin{
			AttemptID: "x", AgentID: "a", Domains: []string{"example.com"}, LoginURL: "https://example.com", Username: "u", Secret: "hunter2",
		}})
		if err != nil {
			t.Fatal(err)
		}
		if n := w.partsC.Store.Waiting(towerID); n != 0 {
			t.Fatalf("the outbox has %d: a secret must wait in memory only", n)
		}
	})
}
