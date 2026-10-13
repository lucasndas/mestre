package protocol_test

import (
	"encoding/json"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/protocol"
)

// A receiver that stops answering (its progress lost, its file closed under
// it) does not hold the transfer forever: after Timing.XferStall with no sign
// the sender sends xfer.begin again, with the same xfer_id, on the same
// connection, and goes on from where the receiver says.
func TestATransferWhoseReceiverStopsAnsweringBeginsAgainOnTheSameConnection(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w, rec, _ := xferWorld(t)
		w.start()
		defer w.stop()
		settle()
		w.dropProgress.Store(true) // the window never opens
		data := content(100 * 1024)
		done := make(chan error, 1)
		go func() {
			done <- w.client.Link().Transfer(t.Context(), protocol.Transfer{Kind: protocol.KindFile, Source: source(data), ChunkSize: 1024})
		}()
		advance(time.Minute)
		select {
		case err := <-done:
			t.Fatalf("finished with the window closed: %v", err)
		default:
		}
		w.dropProgress.Store(false)
		advance(2 * time.Minute)
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatal("the transfer stalled for good")
		}
		begins := 0
		for _, f := range w.sent() {
			if f.typ == protocol.TypeXferBegin {
				begins++
			}
		}
		if begins != 2 || len(w.gaps()) != 0 {
			t.Fatalf("xfer.begin sent %d times over %d connections, want 2 on the same one", begins, len(w.gaps())+1)
		}
		if rec.count() != 1 || sum(rec.files[0]) != sum(data) {
			t.Fatal("the content that arrived does not match")
		}
	})
}

// A transfer that the disk of the receiver cannot take is refused at
// xfer.begin as unavailable, before any part goes.
func TestATransferThatDoesNotFitOnTheDiskIsRefusedAtTheBegin(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w, rec, _ := xferWorld(t)
		free := uint64(protocol.XferDiskReserve + 10*1024)
		w.cfgC.Xfer.FreeSpace = func(string) (uint64, error) { return free, nil }
		w.start()
		defer w.stop()
		settle()
		err := w.client.Link().Transfer(t.Context(), protocol.Transfer{Kind: protocol.KindFile, Source: source(content(20 * 1024)), ChunkSize: 1024})
		var pe *protocol.Error
		if !errors.As(err, &pe) || pe.Code != protocol.CodeUnavailable || !pe.Retryable {
			t.Fatalf("Transfer = %v, want unavailable", err)
		}
		if n := parts(w.sent(), 1000); n != 0 {
			t.Fatalf("%d parts went to a disk without room", n)
		}
		// what fits goes
		if err := w.client.Link().Transfer(t.Context(), protocol.Transfer{Kind: protocol.KindFile, Source: source(content(5 * 1024)), ChunkSize: 1024}); err != nil {
			t.Fatal(err)
		}
		if rec.count() != 1 {
			t.Fatal("the transfer that fits did not arrive")
		}
	})
}

// rawXfer opens a transfer of content(size) by hand, as the Tower, and
// returns its id and a function that sends one of its parts.
func rawXfer(t *testing.T, w *world, c protocol.Conn, size int) (protocol.ULID, func(pos uint64)) {
	t.Helper()
	data := content(size)
	b, _ := json.Marshal(protocol.XferBegin{XferID: "01J9Z0000000000000000000X9", Kind: protocol.KindFile, Size: int64(size), SHA256: sum(data), ChunkSize: 1024})
	sendRaw(t, w, c, protocol.Envelope{ID: "01J9Z0000000000000000000B9", Type: protocol.TypeXferBegin, Ack: true, Payload: b})
	if e := recvType(t, c, protocol.TypeXferReady); e.ReplyTo != "01J9Z0000000000000000000B9" {
		t.Fatalf("xfer.ready answers %s", e.ReplyTo)
	}
	id, _ := protocol.ParseULID("01J9Z0000000000000000000X9")
	return id, func(pos uint64) {
		t.Helper()
		sendPart(t, c, id, pos, data[pos*1024:min(len(data), int(pos+1)*1024)])
	}
}

func sendPart(t *testing.T, c protocol.Conn, id protocol.ULID, pos uint64, data []byte) {
	t.Helper()
	b, err := protocol.EncodeData(protocol.DataFrame{Type: protocol.DataXfer, ID: id, Pos: pos, Data: data})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write(t.Context(), protocol.FrameBinary, b); err != nil {
		t.Fatal(err)
	}
}

// recvType reads up to the next envelope of the type, skipping the others.
func recvType(t *testing.T, c protocol.Conn, typ string) protocol.Envelope {
	t.Helper()
	for {
		if e := recvRaw(t, c); e.Type == typ {
			return e
		}
	}
}

// A part of the wrong size is not a loss on the way that sending it again
// would mend: the transfer ends with invalid_payload instead of stalling.
func TestAPartOfTheWrongSizeEndsTheTransferWithAnError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w, rec, _ := xferWorld(t)
		w.startServer()
		defer w.stop()
		settle()
		conn := rawTower(t, w)
		defer conn.Close(1000, "")
		openRaw(t, w, conn, protocol.DefaultRange())
		id, _ := rawXfer(t, w, conn, 3*1024)
		sendPart(t, conn, id, 0, make([]byte, 10))
		e := recvType(t, conn, protocol.TypeXferDone)
		var d protocol.XferDone
		_ = json.Unmarshal(e.Payload, &d)
		if d.OK || d.Error == nil || d.Error.Code != protocol.CodeInvalidPayload {
			t.Fatalf("xfer.done = %s, want invalid_payload", e.Payload)
		}
		if rec.count() != 0 {
			t.Fatal("the transfer reached the finalizer")
		}
	})
}

// A missing part makes the receiver ask, once, to go back to it; the parts
// already on the way after it are dropped without more requests.
func TestAMissingPartIsAskedForOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w, rec, _ := xferWorld(t)
		w.startServer()
		defer w.stop()
		settle()
		conn := rawTower(t, w)
		defer conn.Close(1000, "")
		openRaw(t, w, conn, protocol.DefaultRange())
		id, part := rawXfer(t, w, conn, 5*1024)
		for _, pos := range []uint64{0, 2, 3} { // part 1 is lost on the way
			part(pos)
		}
		e := recvType(t, conn, protocol.TypeXferProgress)
		var p protocol.XferProgress
		_ = json.Unmarshal(e.Payload, &p)
		if p.NextIndex != 1 || !p.Resend {
			t.Fatalf("xfer.progress = %s, want to go back to part 1", e.Payload)
		}
		for pos := uint64(1); pos < 5; pos++ {
			part(pos)
		}
		e = recvType(t, conn, protocol.TypeXferProgress)
		p = protocol.XferProgress{}
		_ = json.Unmarshal(e.Payload, &p)
		if p.NextIndex != 5 || p.Resend {
			t.Fatalf("the next xfer.progress = %s, want all 5 parts and no second request", e.Payload)
		}
		end, _ := json.Marshal(protocol.XferEnd{XferID: id.String()})
		sendRaw(t, w, conn, protocol.Envelope{ID: "01J9Z0000000000000000000E9", Type: protocol.TypeXferEnd, Ack: true, Payload: end})
		e = recvType(t, conn, protocol.TypeXferDone)
		var d protocol.XferDone
		_ = json.Unmarshal(e.Payload, &d)
		if !d.OK {
			t.Fatalf("xfer.done = %s", e.Payload)
		}
		if rec.count() != 1 {
			t.Fatal("the file did not reach the finalizer")
		}
	})
}

// The number of parts does not overflow near the largest size, and a size
// over MaxXferSize is refused.
func TestThePartsOfAHugeTransferDoNotOverflowAndItsSizeIsCapped(t *testing.T) {
	t.Parallel()
	b := protocol.XferBegin{Size: 1<<63 - 1, ChunkSize: 1 << 20}
	if got, want := b.Parts(), uint64((1<<63-1)/(1<<20))+1; got != want {
		t.Fatalf("Parts = %d, want %d", got, want)
	}
	reg := protocol.DefaultRegistry()
	for size, ok := range map[int64]bool{protocol.MaxXferSize: true, protocol.MaxXferSize + 1: false} {
		begin := protocol.XferBegin{XferID: "01J9Z0000000000000000000A1", Kind: protocol.KindFile, Size: size, SHA256: sum(nil)}
		if _, err := reg.Marshal(protocol.TypeXferBegin, begin, false); (err == nil) != ok {
			t.Errorf("size %d: Marshal = %v, want ok = %v", size, err, ok)
		}
	}
}
