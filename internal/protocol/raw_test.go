package protocol_test

import (
	"encoding/json"
	"testing"

	"github.com/mestre-ia/mestre/internal/protocol"
)

// rawTower dials the Citadel as the Tower, without a Client: the test plays
// the Tower by hand.
func rawTower(t *testing.T, w *world) protocol.Conn {
	t.Helper()
	c, err := w.nw.Dialer(towerHost, citadelAddr, protocol.Peer{Role: protocol.RoleMachine, ID: towerID}).Dial(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// sendRaw stamps and sends an envelope as the Tower, on the epoch of its
// configuration.
func sendRaw(t *testing.T, w *world, c protocol.Conn, e protocol.Envelope) {
	t.Helper()
	if e.MachineID == "" {
		e.MachineID = towerID
	}
	if e.Epoch == 0 {
		e.Epoch = w.partsT.Epochs.Epoch()
	}
	if e.TS.IsZero() {
		e.TS = protocol.Timestamp{Time: w.clk.Now()}
	}
	b, err := protocol.Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write(t.Context(), protocol.FrameText, b); err != nil {
		t.Fatal(err)
	}
}

// recvRaw reads the next envelope, past the floors of the outbox that the
// Citadel sends on its own.
func recvRaw(t *testing.T, c protocol.Conn) protocol.Envelope {
	t.Helper()
	for {
		_, data, err := c.Read(t.Context())
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		e, perr := protocol.Decode(data)
		if perr != nil {
			t.Fatalf("Decode: %v", perr)
		}
		if e.Type != protocol.TypeOutboxFloor {
			return e
		}
	}
}

// helloPayload is the hello of the Tower with the range r and its certificate.
func helloPayload(t *testing.T, w *world, r protocol.Range) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(protocol.Hello{Protocol: r, Version: "0.0.0-test", Sheet: protocol.Object{},
		Certificate: w.partsT.Epochs.Certificate()})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const helloID = "01J9Z0000000000000000000H1"

// openRaw does the opening by hand and returns the welcome.
func openRaw(t *testing.T, w *world, c protocol.Conn, r protocol.Range) protocol.Welcome {
	t.Helper()
	sendRaw(t, w, c, protocol.Envelope{ID: helloID, Type: protocol.TypeHello, Payload: helloPayload(t, w, r)})
	e := recvRaw(t, c)
	if e.Type != protocol.TypeWelcome || e.ReplyTo != helloID {
		t.Fatalf("answer to hello = %s reply_to %s", e.Type, e.ReplyTo)
	}
	var wel protocol.Welcome
	if err := json.Unmarshal(e.Payload, &wel); err != nil {
		t.Fatal(err)
	}
	return wel
}

func errorCode(t *testing.T, e protocol.Envelope) string {
	t.Helper()
	if e.Type != protocol.TypeError {
		t.Fatalf("got %s, want an error", e.Type)
	}
	var pe protocol.Error
	if err := json.Unmarshal(e.Payload, &pe); err != nil {
		t.Fatal(err)
	}
	return pe.Code
}
