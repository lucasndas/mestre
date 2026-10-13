package protocol_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/testkit/protorig"
)

// A higher epoch in the middle of a connection, between a real Citadel and a
// real Tower: the receiver asks the sender for the certificate (the request
// is answered whatever the epoch of the asker), follows it, and only then
// decides. These cases go through Client and Server, not a hand-played peer.

// The Citadel took epoch 2 itself (the lock again, after a suspension): the
// Tower follows it on the same connection and runs what it sends.
func TestATowerFollowsTheHigherEpochOfItsCitadelAndRunsWhatItSends(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		ty := &typist{}
		w.cfgT.Router.Handle(protocol.TypeTerminalInput, ty)
		w.start()
		defer w.stop()
		settle()
		w.partsC.Epochs.Set(protorig.Cert(2, citadelID, protorig.SignedOK))
		r, err := w.toTower().Call(t.Context(), input("ls\n"))
		if err != nil || r.Status != protocol.AckOK {
			t.Fatalf("Call = %+v, %v", r, err)
		}
		if got := w.partsT.Epochs.Followed(); len(got) != 1 || got[0] != 2 {
			t.Fatalf("the Tower followed %v, want [2]", got)
		}
		if got := ty.list(); len(got) != 1 {
			t.Fatalf("typed = %q", got)
		}
		if n := len(w.gaps()); n != 0 {
			t.Fatalf("the Tower connected again %d times: the epoch must be settled on the connection", n)
		}
		if n := w.partsC.Store.Waiting(towerID); n != 0 {
			t.Fatalf("the outbox of the Citadel has %d: the certificate must not wait for an ack", n)
		}
	})
}

// The Citadel learned that another machine took epoch 2 and still sends: the
// Tower follows the certificate, runs nothing of a machine that it does not
// name, and closes with 4007.
func TestATowerRunsNothingOfAPeerThatTheHigherEpochDoesNotName(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		ty := &typist{}
		w.cfgT.Router.Handle(protocol.TypeTerminalInput, ty)
		ended := make(chan error, 8)
		w.cfgT.Hooks.OnDisconnect = func(_ *protocol.Session, cause error) { ended <- cause }
		w.start()
		defer w.stop()
		settle()
		w.partsC.Epochs.Set(protorig.Cert(2, "other", protorig.SignedOK))
		if _, err := w.toTower().Send(t.Context(), input("rm -rf\n")); err != nil {
			t.Fatal(err)
		}
		settle()
		if got := w.partsT.Epochs.Followed(); len(got) != 1 || got[0] != 2 {
			t.Fatalf("the Tower followed %v, want [2]", got)
		}
		if got := ty.list(); len(got) != 0 {
			t.Fatalf("typed = %q: a machine that is not the Citadel of the epoch was obeyed", got)
		}
		if cause := <-ended; !errors.Is(cause, protocol.ErrNotCitadel) {
			t.Fatalf("the connection ended with %v, want 4007", cause)
		}
	})
}

// The Tower knows epoch 2, which names another machine: the Citadel asks it
// for the certificate, follows it, stops being the Citadel and closes with
// 4007; the message does not run.
func TestACitadelThatFollowsTheHigherEpochOfATowerStepsDownWith4007(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		var demoted atomic.Bool
		w.partsC.Epochs.OnFollow(func(protocol.Certificate) error { demoted.Store(true); return nil })
		w.cfgC.IsCitadel = func() bool { return !demoted.Load() }
		var ran atomic.Int32
		w.cfgC.Router.HandleFunc(protocol.TypeTerminalStatus, func(context.Context, *protocol.Message) (*protocol.Reply, error) {
			ran.Add(1)
			return nil, nil
		})
		ended := make(chan error, 8)
		w.cfgT.Hooks.OnDisconnect = func(_ *protocol.Session, cause error) { ended <- cause }
		w.start()
		defer w.stop()
		settle()
		w.partsT.Epochs.Set(protorig.Cert(2, "other", protorig.SignedOK))
		if _, err := w.client.Link().Send(t.Context(), protocol.Outgoing{Type: protocol.TypeTerminalStatus,
			Payload: protocol.TerminalStatus{TerminalID: "t1", State: "idle"}}); err != nil {
			t.Fatal(err)
		}
		settle()
		if got := w.partsC.Epochs.Followed(); len(got) != 1 || got[0] != 2 || !demoted.Load() {
			t.Fatalf("the Citadel followed %v, demoted = %v", got, demoted.Load())
		}
		if ran.Load() != 0 {
			t.Fatal("a Citadel that stepped down ran the message")
		}
		var ce *protocol.CloseError
		if cause := <-ended; !errors.As(cause, &ce) || ce.Code != protocol.CloseNotCitadel {
			t.Fatalf("the connection ended with %v, want 4007", cause)
		}
	})
}

// A binary frame carries no epoch: it is taken only while the peer has shown
// an epoch at least ours. Records of a Citadel that this machine has since
// seen replaced are refused, and the connection closes with 4006.
func TestABinaryFrameOfAPeerBehindOurEpochIsRefused(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		var got atomic.Int32
		w.cfgT.Router.HandleData(protocol.DataReplica, protocol.DataHandlerFunc(func(context.Context, protocol.Peer, protocol.DataFrame) {
			got.Add(1)
		}))
		ended := make(chan error, 8)
		w.cfgC.Hooks.OnDisconnect = func(_ *protocol.Session, cause error) { ended <- cause }
		w.start()
		defer w.stop()
		settle()
		gid, _ := protocol.ParseULID("01J9Z0000000000000000000G1")
		send := func() {
			if err := w.toTower().SendData(t.Context(), protocol.DataFrame{Type: protocol.DataReplica, ID: gid, Pos: 1, Data: []byte("records")}); err != nil {
				t.Fatal(err)
			}
			settle()
		}
		send()
		if got.Load() != 1 {
			t.Fatalf("frames taken = %d, want 1", got.Load())
		}
		w.partsT.Epochs.Set(protorig.Cert(2, "other", protorig.SignedOK))
		send()
		if got.Load() != 1 {
			t.Fatal("a frame of a Citadel behind the epoch of the Tower was applied")
		}
		var ce *protocol.CloseError
		if cause := <-ended; !errors.As(cause, &ce) || ce.Code != protocol.CloseStaleEpoch {
			t.Fatalf("the connection ended with %v, want 4006", cause)
		}
	})
}
