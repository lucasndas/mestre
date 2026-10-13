package protocol_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/testkit/protorig"
	"github.com/lucasndas/mestre/pkg/clock"
)

// versionsSeen records the version each side of a new connection settled on.
func versionsSeen(w *world) (citadel, tower chan int) {
	citadel, tower = make(chan int, 4), make(chan int, 4)
	w.cfgC.Hooks.OnConnect = func(s *protocol.Session) { citadel <- s.Version() }
	w.cfgT.Hooks.OnConnect = func(s *protocol.Session) { tower <- s.Version() }
	return citadel, tower
}

// CA-PROTO-02
func TestVersionsThreeToFourAndFourToFiveSettleOnFour(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{versionsT: protocol.Range{Min: 3, Max: 4}, versionsC: protocol.Range{Min: 4, Max: 5}})
		vc, vt := versionsSeen(w)
		w.start()
		defer w.stop()
		settle()
		if got := <-vc; got != 4 {
			t.Fatalf("Citadel version = %d, want 4", got)
		}
		if got := <-vt; got != 4 {
			t.Fatalf("Tower version = %d, want 4", got)
		}
	})
}

// CA-PROTO-02
func TestATowerThatOnlySpeaksTwoIsClosedWith4003AndTheRangeTheCitadelAccepts(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{versionsC: protocol.Range{Min: 4, Max: 5}})
		w.startServer()
		defer w.stop()
		settle()
		conn := rawTower(t, w)
		defer conn.Close(1000, "")
		sendRaw(t, w, conn, protocol.Envelope{ID: helloID, Type: protocol.TypeHello, Payload: helloPayload(t, w, protocol.Range{Min: 2, Max: 2})})
		_, _, err := conn.Read(t.Context())
		var ce *protocol.CloseError
		if !errors.As(err, &ce) || ce.Code != protocol.CloseIncompatible {
			t.Fatalf("Recv = %v, want a close with 4003", err)
		}
		if r, ok := protocol.ParseVersionReason(ce.Reason); !ok || r != (protocol.Range{Min: 4, Max: 5}) {
			t.Fatalf("reason = %q, want the range 4-5", ce.Reason)
		}
	})
}

func TestTheClientReportsTheRangeOfACitadelThatClosedWith4003(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{versionsT: protocol.Range{Min: 2, Max: 2}, versionsC: protocol.Range{Min: 4, Max: 5}})
		seen := make(chan [2]protocol.Range, 4)
		w.cfgT.Hooks.Incompatible = func(local, remote protocol.Range) { seen <- [2]protocol.Range{local, remote} }
		w.start()
		defer w.stop()
		settle()
		got := <-seen
		if got[0] != (protocol.Range{Min: 2, Max: 2}) || got[1] != (protocol.Range{Min: 4, Max: 5}) {
			t.Fatalf("Incompatible(%v, %v)", got[0], got[1])
		}
		if w.toTower().Connected() {
			t.Fatal("connected with no version in common")
		}
	})
}

func TestTheVersionRangeIsDecidedByTheHighestInCommon(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		a, b protocol.Range
		want int
		ok   bool
	}{
		{protocol.Range{Min: 3, Max: 4}, protocol.Range{Min: 4, Max: 5}, 4, true},
		{protocol.Range{Min: 1, Max: 2}, protocol.Range{Min: 2, Max: 3}, 2, true},
		{protocol.Range{Min: 1, Max: 1}, protocol.Range{Min: 1, Max: 1}, 1, true},
		{protocol.Range{Min: 1, Max: 2}, protocol.Range{Min: 3, Max: 4}, 0, false},
		{protocol.Range{Min: 0, Max: 2}, protocol.Range{Min: 1, Max: 2}, 0, false},
		{protocol.Range{Min: 3, Max: 2}, protocol.Range{Min: 1, Max: 2}, 0, false},
	} {
		t.Run(tc.a.String()+"/"+tc.b.String(), func(t *testing.T) {
			t.Parallel()
			got, ok := protocol.Negotiate(tc.a, tc.b)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("Negotiate = %d, %v; want %d, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestOnlyTheCapabilitiesBothSidesAnnounceAreOn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		w.cfgC.Capabilities = protocol.Capabilities{"b", "a", "d"}
		w.cfgT.Capabilities = protocol.Capabilities{"c", "a", "b"}
		got := make(chan protocol.Capabilities, 2)
		w.cfgC.Hooks.OnConnect = func(s *protocol.Session) { got <- s.Capabilities() }
		w.cfgT.Hooks.OnConnect = func(s *protocol.Session) { got <- s.Capabilities() }
		w.start()
		defer w.stop()
		settle()
		for range 2 {
			c := <-got
			if len(c) != 2 || !c.Has("a") || !c.Has("b") || c.Has("c") || c.Has("d") {
				t.Fatalf("capabilities = %v, want a and b", c)
			}
		}
	})
}

func TestAnOpeningThatDoesNotStartWithHelloIsRefused(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		w.startServer()
		defer w.stop()
		settle()
		conn := rawTower(t, w)
		defer conn.Close(1000, "")
		sendRaw(t, w, conn, protocol.Envelope{ID: helloID, Type: protocol.TypeMachineSheet, Ack: true, Payload: json.RawMessage(`{}`)})
		if code := errorCode(t, recvRaw(t, conn)); code != protocol.CodeInvalidPayload {
			t.Fatalf("code = %s", code)
		}
		if _, _, err := conn.Read(t.Context()); err == nil {
			t.Fatal("the connection stayed open")
		}
	})
}

func TestAnOpeningThatNeverComesIsGivenUp(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		w.startServer()
		defer w.stop()
		settle()
		conn := rawTower(t, w)
		defer conn.Close(1000, "")
		advance(11 * time.Second) // Timing.Handshake is 10 s
		if _, _, err := conn.Read(t.Context()); err == nil {
			t.Fatal("the Citadel kept waiting for a hello")
		}
	})
}

func TestTheHelloCanBeRefusedByTheCitadel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		w.cfgC.Hooks.OnHello = func(_ context.Context, p protocol.Peer, h *protocol.Hello) error {
			if p.ID != towerID || h.Version != "0.0.0-test" {
				t.Errorf("OnHello(%v, %+v)", p, h)
			}
			return &protocol.CloseError{Code: protocol.CloseRevoked, Reason: "revoked"}
		}
		revoked := make(chan struct{}, 1)
		w.cfgT.Hooks.Revoked = func() { revoked <- struct{}{} }
		w.start()
		defer w.stop()
		settle()
		select {
		case <-revoked:
		default:
			t.Fatal("the Tower kept trying after 4002")
		}
		advance(5 * time.Minute)
		if w.toTower().Connected() {
			t.Fatal("a revoked machine connected")
		}
	})
}

func TestTheTowerWarnsWhenTheClocksDifferByMoreThanFiveSeconds(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		w.cfgC.Clock = clock.NewFake(clock.Real{}.Now().Add(12 * time.Second))
		skew := make(chan time.Duration, 1)
		w.cfgT.Hooks.ClockSkew = func(d time.Duration) { skew <- d }
		w.start()
		defer w.stop()
		settle()
		select {
		case d := <-skew:
			if d < 11*time.Second || d > 13*time.Second {
				t.Fatalf("skew = %v, want about 12 s", d)
			}
		default:
			t.Fatal("no warning")
		}
	})
}

// ---- epoch ----

// Nothing is done for a message of an old epoch: the machine answers
// stale_epoch and closes with 4006.
// CA-PROTO-10
func TestACitadelOnEpochTwoRefusesAMessageOfEpochOne(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{epochC: 2, epochT: 2})
		ran := 0
		w.cfgC.Router.HandleFunc(protocol.TypeTerminalStatus, func(_ context.Context, _ *protocol.Message) (*protocol.Reply, error) {
			ran++
			return nil, nil
		})
		w.startServer()
		defer w.stop()
		settle()
		conn := rawTower(t, w)
		defer conn.Close(1000, "")
		openRaw(t, w, conn, protocol.DefaultRange())

		sendRaw(t, w, conn, protocol.Envelope{ID: "01J9Z0000000000000000000K1", Type: protocol.TypeTerminalStatus, Ack: true, Epoch: 1,
			Payload: json.RawMessage(`{"terminal_id":"t1","state":"idle"}`)})
		e := recvRaw(t, conn)
		if code := errorCode(t, e); code != protocol.CodeStaleEpoch || e.ReplyTo != "01J9Z0000000000000000000K1" {
			t.Fatalf("answer = %s reply_to %s, want stale_epoch", code, e.ReplyTo)
		}
		_, _, err := conn.Read(t.Context())
		var ce *protocol.CloseError
		if !errors.As(err, &ce) || ce.Code != protocol.CloseStaleEpoch {
			t.Fatalf("Recv = %v, want a close with 4006", err)
		}
		settle()
		if ran != 0 {
			t.Fatal("a message of an old epoch was executed")
		}
		if w.partsC.Store.Claims() != 0 {
			t.Fatal("a message of an old epoch was claimed")
		}
	})
}

// The same on the Tower: a Tower that is ahead refuses what a Citadel of an
// older epoch sends, never runs it, and the Citadel learns it is stale.
// CA-PROTO-10
func TestATowerOnEpochTwoRefusesAMessageOfACitadelOnEpochOne(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{epochC: 1, epochT: 1})
		ty := &typist{}
		w.cfgT.Router.Handle(protocol.TypeTerminalInput, ty)
		ended := make(chan error, 8)
		w.cfgC.Hooks.OnDisconnect = func(_ *protocol.Session, cause error) { ended <- cause }
		w.start()
		defer w.stop()
		settle()
		// the Tower learns epoch 2 (a promotion happened elsewhere)
		w.partsT.Epochs.Set(protorig.Cert(2, "other", protorig.SignedOK))
		r, err := w.toTower().Call(t.Context(), input("rm -rf\n"))
		if err != nil || r.Status != protocol.AckError || r.Error == nil || r.Error.Code != protocol.CodeStaleEpoch {
			t.Fatalf("answer = %+v, %v; want stale_epoch", r, err)
		}
		settle()
		var ce *protocol.CloseError
		if cause := <-ended; !errors.As(cause, &ce) || ce.Code != protocol.CloseStaleEpoch {
			t.Fatalf("the connection ended with %v, want 4006", cause)
		}
		if got := ty.list(); len(got) != 0 {
			t.Fatalf("typed = %q: a message of an old epoch ran", got)
		}
		if w.partsT.Store.Claims() != 0 {
			t.Fatal("a message of an old epoch was claimed")
		}
		if w.partsC.Epochs.StaleCalls() == 0 {
			t.Fatal("the Citadel was not told that its epoch is old")
		}
	})
}

// A welcome with a valid certificate of epoch 3 makes the Tower follow it.
// CA-PROTO-10
func TestAWelcomeWithTheCertificateOfEpochThreeMakesTheTowerFollowIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{epochC: 3, epochT: 2})
		w.start()
		defer w.stop()
		settle()
		if !w.toTower().Connected() {
			t.Fatal("the Tower did not connect")
		}
		if got := w.partsT.Epochs.Followed(); len(got) != 1 || got[0] != 3 {
			t.Fatalf("followed = %v, want [3]", got)
		}
		if w.partsT.Epochs.Epoch() != 3 {
			t.Fatalf("epoch = %d", w.partsT.Epochs.Epoch())
		}
		// and from now on it talks on epoch 3
		ty := &typist{}
		w.cfgT.Router.Handle(protocol.TypeTerminalInput, ty)
	})
}

func TestAWelcomeWithAForgedCertificateIsNotFollowed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{epochC: 3, epochT: 2})
		w.partsC.Epochs.Set(protorig.Cert(3, citadelID, "forged"))
		w.start()
		defer w.stop()
		settle()
		if w.toTower().Connected() {
			t.Fatal("the Tower connected to a Citadel with a forged certificate")
		}
		if len(w.partsT.Epochs.Followed()) != 0 {
			t.Fatal("followed a forged certificate")
		}
	})
}

func TestAWelcomeFromAMachineThatTheCertificateDoesNotNameIsRefused(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{epochC: 1})
		w.partsC.Epochs.Set(protorig.Cert(1, "someone-else", "ok")) // valid, but it names another machine
		w.start()
		defer w.stop()
		settle()
		if w.toTower().Connected() {
			t.Fatal("the Tower connected to a machine that the certificate does not name")
		}
	})
}

func TestAMachineThatIsNotTheCitadelSendsTheTowerToTheOneItsCertificateNames(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{epochC: 2, epochT: 1})
		w.cfgC.IsCitadel = func() bool { return false }
		var rediscovered int
		w.cfgT.Hooks.Rediscover = func(context.Context) { rediscovered++ }
		w.start()
		defer w.stop()
		settle()
		if got := w.partsT.Epochs.Followed(); len(got) != 1 || got[0] != 2 {
			t.Fatalf("followed = %v, want the certificate of epoch 2", got)
		}
		// at once, not after the third failure
		if rediscovered != 1 || len(w.gaps()) != 0 {
			t.Fatalf("rediscovered %d times after %d retries", rediscovered, len(w.gaps()))
		}
	})
}

func TestATowerDoesNotConnectIfTheCitadelCannotFollowItsEpoch(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{epochC: 1, epochT: 2})
		w.partsC.Epochs.OnFollow(func(protocol.Certificate) error { return errors.New("not ready") })
		w.start()
		defer w.stop()
		settle()
		if w.toTower().Connected() {
			t.Fatal("the Tower connected to a Citadel that is behind")
		}
		if len(w.partsC.Epochs.Followed()) != 0 {
			t.Fatal("followed")
		}
	})
}

func TestACitadelBehindFollowsTheEpochOfTheHelloOfATowerThatIsAhead(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{epochC: 1, epochT: 2})
		w.start()
		defer w.stop()
		settle()
		if got := w.partsC.Epochs.Followed(); len(got) != 1 || got[0] != 2 {
			t.Fatalf("Citadel followed %v, want [2]", got)
		}
		if !w.toTower().Connected() {
			t.Fatal("not connected after the Citadel caught up")
		}
	})
}

// A message of a higher epoch makes the receiver ask for the certificate,
// check it and follow it before anything is done with the message.
func TestAMessageOfAHigherEpochMakesTheReceiverFollowTheCertificate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{epochC: 2, epochT: 2})
		var before uint64
		ran := 0
		w.cfgC.Router.HandleFunc(protocol.TypeTerminalStatus, func(_ context.Context, _ *protocol.Message) (*protocol.Reply, error) {
			ran++
			before = w.partsC.Epochs.Epoch()
			return nil, nil
		})
		w.startServer()
		defer w.stop()
		settle()
		conn := rawTower(t, w)
		defer conn.Close(1000, "")
		openRaw(t, w, conn, protocol.DefaultRange())

		sendRaw(t, w, conn, protocol.Envelope{ID: "01J9Z0000000000000000000N1", Type: protocol.TypeTerminalStatus, Ack: true, Epoch: 3,
			Payload: json.RawMessage(`{"terminal_id":"t1","state":"idle"}`)})
		req := recvRaw(t, conn)
		if req.Type != protocol.TypeGuildEpoch || req.ReplyTo != "" {
			t.Fatalf("got %s %s, want a guild.epoch request", req.Type, req.Payload)
		}
		cert := protorig.Cert(3, towerID, protorig.SignedOK)
		b, _ := json.Marshal(cert)
		sendRaw(t, w, conn, protocol.Envelope{ID: "01J9Z0000000000000000000N2", Type: protocol.TypeGuildEpoch, ReplyTo: req.ID, Epoch: 3, Payload: b})
		settle()
		if ran != 1 || before != 3 {
			t.Fatalf("ran = %d, epoch at that time = %d: the certificate must be followed before the message runs", ran, before)
		}
		if got := w.partsC.Epochs.Followed(); len(got) != 1 || got[0] != 3 {
			t.Fatalf("followed = %v", got)
		}
	})
}

func TestAMessageOfAHigherEpochWithABadCertificateIsRefusedAndNotRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{epochC: 2, epochT: 2})
		ran := 0
		w.cfgC.Router.HandleFunc(protocol.TypeTerminalStatus, func(_ context.Context, _ *protocol.Message) (*protocol.Reply, error) {
			ran++
			return nil, nil
		})
		w.startServer()
		defer w.stop()
		settle()
		conn := rawTower(t, w)
		defer conn.Close(1000, "")
		openRaw(t, w, conn, protocol.DefaultRange())
		sendRaw(t, w, conn, protocol.Envelope{ID: "01J9Z0000000000000000000M1", Type: protocol.TypeTerminalStatus, Ack: true, Epoch: 9,
			Payload: json.RawMessage(`{"terminal_id":"t1","state":"idle"}`)})
		req := recvRaw(t, conn)
		b, _ := json.Marshal(protorig.Cert(9, towerID, "forged"))
		sendRaw(t, w, conn, protocol.Envelope{ID: "01J9Z0000000000000000000M2", Type: protocol.TypeGuildEpoch, ReplyTo: req.ID, Epoch: 9, Payload: b})
		if code := errorCode(t, recvRaw(t, conn)); code != protocol.CodeForbidden {
			t.Fatalf("code = %s, want forbidden", code)
		}
		if ran != 0 || len(w.partsC.Epochs.Followed()) != 0 || w.partsC.Epochs.Epoch() != 2 {
			t.Fatalf("ran = %d, followed = %v", ran, w.partsC.Epochs.Followed())
		}
	})
}

func TestACitadelThatSeesAHigherEpochInTheHelloFollowsAndStopsBeingTheCitadel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{epochC: 2, epochT: 3})
		demoted := false
		w.partsC.Epochs.OnFollow(func(protocol.Certificate) error { demoted = true; return nil })
		w.cfgC.IsCitadel = func() bool { return !demoted }
		w.startServer()
		defer w.stop()
		settle()
		conn := rawTower(t, w)
		defer conn.Close(1000, "")
		sendRaw(t, w, conn, protocol.Envelope{ID: helloID, Type: protocol.TypeHello, Payload: helloPayload(t, w, protocol.DefaultRange())})
		e := recvRaw(t, conn)
		if e.Type != protocol.TypeGuildEpoch || e.ReplyTo != helloID {
			t.Fatalf("got %s, want the certificate", e.Type)
		}
		_, _, err := conn.Read(t.Context())
		var ce *protocol.CloseError
		if !errors.As(err, &ce) || ce.Code != protocol.CloseNotCitadel {
			t.Fatalf("Recv = %v, want a close with 4007", err)
		}
		if !demoted || w.partsC.Epochs.Epoch() != 3 {
			t.Fatalf("demoted = %v, epoch = %d", demoted, w.partsC.Epochs.Epoch())
		}
	})
}

func TestAnOutboxMessageIsResentToACitadelOfANewerEpochStampedWithIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{epochC: 1, epochT: 1})
		got := make(chan uint64, 1)
		w.cfgC.Router.HandleFunc(protocol.TypeTerminalStatus, func(_ context.Context, m *protocol.Message) (*protocol.Reply, error) {
			got <- m.Env.Epoch
			return nil, nil
		})
		w.start()
		defer w.stop()
		settle()
		w.net.Partition(towerHost, citadelID)
		settle()
		// queued on epoch 1 while the Citadel is away ...
		_, err := w.client.Link().Send(t.Context(), protocol.Outgoing{Type: protocol.TypeTerminalStatus, Payload: protocol.TerminalStatus{TerminalID: "t1", State: "idle"}})
		if err != nil {
			t.Fatal(err)
		}
		// ... and the Guild moves to epoch 2 meanwhile
		w.partsC.Epochs.Set(protorig.Cert(2, citadelID, protorig.SignedOK))
		w.net.Heal(towerHost, citadelID)
		advance(2 * time.Minute)
		select {
		case e := <-got:
			if e != 2 {
				t.Fatalf("the resent message carried epoch %d, want 2", e)
			}
		default:
			t.Fatal("the message was not resent")
		}
	})
}
