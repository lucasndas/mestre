package protocol_test

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/lucasndas/mestre/internal/protocol"
)

// A terminal that prints 50 MiB does not delay the heartbeat: once it is
// queued, it goes before the next 64 KiB of data.
// CA-PROTO-03
func TestAHeartbeatGoesBeforeTheNext64KiBOfAFiftyMiBTerminalOutput(t *testing.T) {
	t.Parallel()
	for _, frame := range []int{16 << 10} {
		t.Run("16 KiB frames", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := newWorld(t, opts{})
				w.start()
				defer w.stop()
				settle()
				link := w.client.Link()
				id, _ := protocol.ParseULID("01J9Z0000000000000000000T1")

				// the Tower writes through a gate, so the test decides when the
				// heartbeat meets the stream
				gate := make(chan struct{})
				w.gate.Store(&gate)
				const total = 50 << 20
				produced := make(chan int, 1)
				go func() {
					n := 0
					buf := make([]byte, frame)
					for pos := uint64(0); n < total; pos += uint64(frame) {
						if err := link.SendData(t.Context(), protocol.DataFrame{Type: protocol.DataTerminal, ID: id, Pos: pos, Data: buf}); err != nil {
							break
						}
						n += frame
					}
					produced <- n
				}()
				settle() // the writer is holding its first batch, the queue is full

				before := len(w.sent())
				if _, err := link.Send(t.Context(), protocol.Outgoing{Type: protocol.TypeMachineHeartbeat,
					Payload: protocol.MachineHeartbeat{CPUPct: 1, MemPct: 1, DiskFreeBytes: 1, Terminals: 1}}); err != nil {
					t.Fatal(err)
				}
				close(gate)
				w.gate.Store(nil)
				settle()
				var dataBefore int
				found := false
				for _, f := range w.sent()[before:] {
					if f.kind == protocol.FrameText && f.typ == protocol.TypeMachineHeartbeat {
						found = true
						break
					}
					dataBefore += f.n
				}
				if !found {
					t.Fatal("the heartbeat was not sent while the terminal was printing")
				}
				if dataBefore > protocol.DataBudget {
					t.Fatalf("%d bytes of terminal output went out after the heartbeat was queued, before it; the limit is %d", dataBefore, protocol.DataBudget)
				}
				if dataBefore == 0 {
					t.Fatal("the test did not meet the stream in the middle")
				}
				// the rest of the 50 MiB keeps flowing after the heartbeat
				select {
				case n := <-produced:
					if n != total {
						t.Fatalf("produced %d bytes", n)
					}
				default:
					t.Fatal("the stream did not finish")
				}
			})
		})
	}
}

func TestAnOutputFrameGoesToTheHandlerOfItsKindOnTheCitadel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		type got struct {
			peer protocol.Peer
			f    protocol.DataFrame
		}
		out := make(chan got, 4)
		w.cfgC.Router.HandleData(protocol.DataTerminal, protocol.DataHandlerFunc(func(_ context.Context, p protocol.Peer, f protocol.DataFrame) {
			f.Data = append([]byte(nil), f.Data...)
			out <- got{p, f}
		}))
		w.start()
		defer w.stop()
		settle()
		id, _ := protocol.ParseULID("01J9Z0000000000000000000T2")
		if err := w.client.Link().SendData(t.Context(), protocol.DataFrame{Type: protocol.DataTerminal, ID: id, Pos: 4096, Data: []byte("hello\r\n")}); err != nil {
			t.Fatal(err)
		}
		settle()
		g := <-out
		if g.peer.ID != towerID || g.f.ID != id || g.f.Pos != 4096 || string(g.f.Data) != "hello\r\n" {
			t.Fatalf("got %+v", g)
		}
	})
}

func TestTheBinaryFrameHasTheDocumentedLayout(t *testing.T) {
	t.Parallel()
	id, _ := protocol.ParseULID("01J9Z0000000000000000000T3")
	b, err := protocol.EncodeData(protocol.DataFrame{Type: protocol.DataXfer, ID: id, Pos: 0x0102030405060708, Data: []byte("xyz")})
	if err != nil {
		t.Fatal(err)
	}
	if b[0] != 0x02 || string(b[1:17]) != string(id[:]) || string(b[17:25]) != "\x01\x02\x03\x04\x05\x06\x07\x08" || string(b[25:]) != "xyz" {
		t.Fatalf("frame = % x", b)
	}
	f, err := protocol.DecodeData(b)
	if err != nil || f.Type != protocol.DataXfer || f.ID != id || f.Pos != 0x0102030405060708 || string(f.Data) != "xyz" {
		t.Fatalf("decoded = %+v, %v", f, err)
	}
	if _, err := protocol.DecodeData(b[:10]); err == nil {
		t.Fatal("a short frame was accepted")
	}
}
