package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

// errGraceful ends the read loop after it queued a close: the writer flushes
// what is queued, sends the close and ends the session.
var errGraceful = errors.New("protocol: closing")

// closeGrace is how long a session waits for its writer to flush a close
// before it gives up and drops the connection.
const closeGrace = 5 * time.Second

type dispatchItem struct {
	env     Envelope
	spec    Spec
	payload any
}

// ackItem is the answer of the peer to a message of ours: an ack, or an
// error in its place.
type ackItem struct {
	id  string
	ack *Ack
	err *Error
}

// Session is one connection, after its opening. It reads, writes with the
// two lanes, runs the handlers in order, pings, sends the heartbeat and
// watches for it. The read loop never waits for a handler, the outbox or the
// disk: what it reads goes to three queues, each emptied by a goroutine of
// its own, so the pongs keep coming while a handler is slow.
type Session struct {
	cfg     *Config
	link    *Link
	conn    Conn
	peer    Peer
	side    Side
	version int
	caps    Capabilities
	order   uint64 // the order of acceptance on the link: the highest is the newest

	lanes  *lanes
	budget atomic.Int64
	disp   *inbound[dispatchItem] // messages for the handlers
	acks   *inbound[ackItem]      // answers to our messages, for the outbox
	parts  *inbound[DataFrame]    // parts of transfers, for the disk
	stale  atomic.Bool            // the peer refused our epoch
	fenced atomic.Bool            // we refused the peer: nothing more of it runs
	epoch  atomic.Uint64          // the highest epoch the peer has shown on this connection
	done   chan struct{}

	overload     atomic.Bool // the peer sent more than the queues hold: the close waits for them to empty
	overloadOnce sync.Once

	mu      sync.Mutex
	cancel  context.CancelCauseFunc
	cause   error // why a graceful close happened
	certs   map[string]chan Certificate
	floor   string    // the last outbox.floor of the peer on this connection
	floorAt time.Time // when it came
	hb      clock.Timer
}

func newSession(cfg *Config, link *Link, conn Conn, order uint64, side Side, version int, caps Capabilities) *Session {
	s := &Session{
		cfg: cfg, link: link, conn: conn, order: order, peer: conn.Peer(), side: side, version: version, caps: caps,
		lanes: newLanes(), done: make(chan struct{}), certs: map[string]chan Certificate{},
	}
	s.disp = newInbound[dispatchItem](&s.budget, cfg.InboundLimit)
	s.acks = newInbound[ackItem](&s.budget, cfg.InboundLimit)
	s.parts = newInbound[DataFrame](&s.budget, cfg.InboundLimit)
	s.disp.onEmpty, s.acks.onEmpty, s.parts.onEmpty = s.queuesEmptied, s.queuesEmptied, s.queuesEmptied
	// Both ends agreed on this epoch in the opening.
	s.epoch.Store(cfg.Epoch.Epoch())
	return s
}

// Peer is the identity of the other end.
func (s *Session) Peer() Peer { return s.peer }

// Version is the protocol version both sides chose.
func (s *Session) Version() int { return s.version }

// Capabilities are the features both sides announced.
func (s *Session) Capabilities() Capabilities { return s.caps }

// Done is closed when the session ended.
func (s *Session) Done() <-chan struct{} { return s.done }

// Close ends the connection with a code and a reason, after what is queued on
// the control lane has been sent.
func (s *Session) Close(code int, reason string) {
	s.closeWith(code, reason, &CloseError{Code: code, Reason: reason})
}

func (s *Session) closeWith(code int, reason string, cause error) {
	s.mu.Lock()
	if s.cause == nil {
		s.cause = cause
	}
	cancel := s.cancel
	s.mu.Unlock()
	s.lanes.pushClose(code, reason)
	if cancel != nil {
		s.cfg.Clock.AfterFunc(closeGrace, func() { cancel(cause) })
	}
}

func (s *Session) closeCause() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cause != nil {
		return s.cause
	}
	return errLocalClose
}

// localCause is why this side decided to close, or nil if it did not.
func (s *Session) localCause() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cause
}

func (s *Session) peerSide() Side {
	if s.side == SideTower {
		return SideCitadel
	}
	return SideTower
}

// serve runs the connection until it ends and returns why: a *CloseError
// when either side closed with a code.
func (s *Session) serve(ctx context.Context) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	defer close(s.done)

	if h := s.cfg.Hooks.OnConnect; h != nil {
		h(s)
	}
	if err := s.link.attach(ctx, s); err != nil {
		if errors.Is(err, errSuperseded) {
			// A newer connection of this machine got here first: this
			// is the one that goes, and the link stays as it is.
			ce := &CloseError{Code: CloseReplaced, Reason: "replaced by a newer connection"}
			_ = s.conn.Close(ce.Code, ce.Reason)
			if h := s.cfg.Hooks.OnDisconnect; h != nil {
				h(s, ce)
			}
			return ce
		}
		_ = s.conn.Close(1011, "outbox")
		s.link.detach(s)
		return err
	}
	if s.cfg.Profile.Heartbeats && s.side == SideCitadel {
		s.hb = s.cfg.Clock.AfterFunc(s.cfg.Timing.HeartbeatLost, s.heartbeatLost)
		defer s.hb.Stop()
	}

	var wg sync.WaitGroup
	start := func(f func(context.Context) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := f(ctx)
			switch {
			case err == nil, errors.Is(err, errGraceful):
			case errors.Is(err, errLocalClose):
				cancel(s.closeCause())
			default:
				// A close this side decided (its code is the cause) wins over
				// the error that the peer's hang-up gives a loop still running:
				// which of the two came first is only timing.
				if local := s.localCause(); local != nil {
					err = local
				}
				cancel(err)
			}
		}()
	}
	start(s.readLoop)
	start(func(ctx context.Context) error { return s.lanes.run(ctx, s.conn) })
	start(func(ctx context.Context) error { return s.disp.run(ctx, func(it dispatchItem) { s.process(ctx, it) }) })
	start(func(ctx context.Context) error { return s.acks.run(ctx, func(it ackItem) { s.answered(ctx, it) }) })
	start(func(ctx context.Context) error {
		return s.parts.run(ctx, func(f DataFrame) { s.link.xfer.onPart(ctx, s, f) })
	})
	if _, ok := s.conn.(Pinger); ok {
		start(s.keepAlive)
	}
	if s.cfg.Profile.Heartbeats && s.side == SideTower && s.cfg.Heartbeat != nil {
		start(s.heartbeatLoop)
	}
	if h := s.cfg.Hooks.OnReady; h != nil {
		h(s)
	}
	<-ctx.Done()
	s.lanes.stop(ErrSessionClosed)
	_ = s.conn.Close(1000, "")
	wg.Wait()

	// The answers that arrived before the end still count: their messages
	// leave the outbox and their callers hear of them.
	s.acks.drain(func(it ackItem) { s.answered(context.WithoutCancel(ctx), it) })
	cause := context.Cause(ctx)
	s.link.detach(s)
	var ce *CloseError
	if s.stale.Load() || (errors.As(cause, &ce) && ce.Code == CloseStaleEpoch) {
		// The peer refused us for our epoch (a CloseError of our own making
		// is the same thing seen from the other side): refresh it.
		s.cfg.Epoch.Stale(context.WithoutCancel(ctx), s.peer)
	}
	if h := s.cfg.Hooks.OnDisconnect; h != nil {
		h(s, cause)
	}
	return cause
}

func (s *Session) readLoop(ctx context.Context) error {
	for {
		kind, data, err := s.conn.Read(ctx)
		if errors.Is(err, ErrFrameTooLarge) { // the transport discarded it; the connection stays
			s.sendError("", NewError(CodeTooLarge, "message over %d bytes", MaxMessage))
			continue
		}
		if err != nil {
			return err
		}
		switch kind {
		case FrameBinary:
			err = s.onBinary(ctx, data)
		case FrameText:
			err = s.onText(data)
		}
		if err != nil {
			return err
		}
	}
}

// overloaded stops reading a peer that sends faster than its messages run,
// and closes it with 4008 as soon as what the queues already hold has run
// and been answered (at most closeGrace later): the peer sends again, after
// reconnecting, what still waits for an ack. Closing at once would let a
// round end before the first of its messages ran, or run them without the
// peer hearing of it, and a peer that sends the same flood after every
// reconnection would never get past the first of them: each round must move
// the receiver forward. The read loop does not wait for the queues; the
// close does.
func (s *Session) overloaded() error {
	s.cfg.Logger.Warn("protocol: the peer sends faster than its messages run", "peer", s.peer.ID, "limit", s.cfg.InboundLimit)
	s.overload.Store(true)
	if s.budget.Load() == 0 {
		s.closeOverloaded()
		return errGraceful
	}
	s.cfg.Clock.AfterFunc(closeGrace, s.closeOverloaded)
	return errGraceful
}

// queuesEmptied is told by the queues that nothing waits any more.
func (s *Session) queuesEmptied() {
	if s.overload.Load() {
		s.closeOverloaded()
	}
}

func (s *Session) closeOverloaded() {
	s.overloadOnce.Do(func() {
		s.closeWith(CloseOverloaded, "too far behind", fmt.Errorf("%w: %s", ErrOverloaded, s.peer.ID))
	})
}

// refuseStale is the answer to a message of an older epoch: stale_epoch,
// nothing more of the peer runs, and the connection closes with 4006.
func (s *Session) refuseStale(replyTo string, theirs, mine uint64) error {
	s.fenced.Store(true)
	s.sendError(replyTo, NewError(CodeStaleEpoch, "epoch %d is older than %d", theirs, mine))
	s.closeWith(CloseStaleEpoch, "stale epoch", fmt.Errorf("refused epoch %d of %s: %w", theirs, s.peer.ID, ErrStaleEpoch))
	return errGraceful
}

// sawEpoch records that the peer showed epoch e on this connection.
func (s *Session) sawEpoch(e uint64) {
	for {
		cur := s.epoch.Load()
		if e <= cur || s.epoch.CompareAndSwap(cur, e) {
			return
		}
	}
}

// onBinary takes a binary frame. It carries no epoch: it is taken only while
// the peer has shown, on this connection, an epoch at least ours, so the
// records of a Citadel that this machine has since seen replaced are not
// applied.
func (s *Session) onBinary(ctx context.Context, data []byte) error {
	if s.fenced.Load() {
		return nil
	}
	f, err := DecodeData(data)
	if err != nil {
		s.cfg.Logger.Warn("protocol: bad binary frame", "peer", s.peer.ID, "err", err)
		return nil
	}
	if s.peer.Role != RoleBrowser {
		if theirs, mine := s.epoch.Load(), s.cfg.Epoch.Epoch(); theirs < mine {
			return s.refuseStale("", theirs, mine)
		}
	}
	if f.Type == DataXfer {
		if !s.parts.push(f, len(data)) {
			return s.overloaded()
		}
		return nil
	}
	if h, ok := s.cfg.Router.dataHandler(f.Type); ok {
		h.HandleData(ctx, s.peer, f)
		return nil
	}
	s.cfg.Logger.Debug("protocol: binary frame of an unknown kind", "peer", s.peer.ID, "type", f.Type)
	return nil
}

// onText applies the checks in the order that matters: the form of the
// envelope, who sent it, the epoch (nothing is done for a message of an old
// epoch), then the type, the direction and the schema. It never waits: what
// runs later is queued.
func (s *Session) onText(data []byte) error {
	if s.fenced.Load() {
		return nil
	}
	env, perr := Decode(data)
	if perr != nil {
		replyTo := ""
		if _, err := ParseULID(env.ID); err == nil {
			replyTo = env.ID
		}
		s.sendError(replyTo, perr)
		return nil
	}
	if perr := env.CheckSender(s.peer); perr != nil {
		s.sendError(env.ID, perr)
		return nil
	}
	if s.peer.Role != RoleBrowser {
		if env.Type == TypeGuildEpoch && env.ReplyTo == "" {
			// A peer that asks for our certificate gets it whatever its
			// epoch: this is how a machine that is behind catches up, and
			// nothing runs.
			s.answerEpoch(env)
			return nil
		}
		if mine := s.cfg.Epoch.Epoch(); env.Epoch < mine {
			return s.refuseStale(env.ID, env.Epoch, mine)
		}
		s.sawEpoch(env.Epoch)
	}
	spec, ok := s.cfg.Registry.Spec(env.Type)
	if !ok {
		s.sendError(env.ID, NewError(CodeUnknownType, "type %q is not known in this version", env.Type))
		return nil
	}
	if !spec.Dir.Allows(s.peerSide()) {
		s.sendError(env.ID, NewError(CodeForbidden, "%s may not be sent by this side of the connection", env.Type))
		return nil
	}
	payload, perr := spec.decode(env.Payload, env.ReplyTo != "")
	if perr != nil {
		s.sendError(env.ID, perr)
		return nil
	}
	switch env.Type {
	case s.cfg.Profile.HelloType, s.cfg.Profile.WelcomeType:
		s.sendError(env.ID, NewError(CodeForbidden, "the connection is already open"))
		return nil
	case TypeAck:
		if env.ReplyTo != "" && !s.acks.push(ackItem{id: env.ReplyTo, ack: payload.(*Ack)}, len(data)) {
			return s.overloaded()
		}
		return nil
	case TypeError:
		e := payload.(*Error)
		if e.Code == CodeStaleEpoch {
			s.stale.Store(true)
		}
		if env.ReplyTo != "" && !s.acks.push(ackItem{id: env.ReplyTo, err: e}, len(data)) {
			return s.overloaded()
		}
		return nil
	case TypeOutboxFloor:
		s.mu.Lock()
		s.floor, s.floorAt = payload.(*OutboxFloor).ID, s.cfg.Clock.Now()
		s.mu.Unlock()
		return nil
	case TypeGuildEpoch: // the answer to our request (requests were answered above)
		s.deliverCertificate(env, payload.(*Certificate))
		return nil
	case TypeMachineHeartbeat:
		s.heartbeatSeen()
	}
	if !s.disp.push(dispatchItem{env: env, spec: spec, payload: payload}, len(data)) {
		return s.overloaded()
	}
	return nil
}

// answered hands the answer of the peer to the link, off the read loop: it
// may write the outbox.
func (s *Session) answered(ctx context.Context, it ackItem) {
	if it.ack != nil {
		s.link.ackArrived(ctx, it.id, it.ack)
		return
	}
	s.link.errorArrived(ctx, it.id, it.err)
}

// process runs one message. The messages of a link run one at a time, in
// order, even while a newer connection replaces an older one; and the epoch
// is checked again, because ours may have moved while the message waited.
func (s *Session) process(ctx context.Context, it dispatchItem) {
	l := s.link
	select {
	case l.turn <- struct{}{}:
		defer func() { <-l.turn }()
	case <-ctx.Done():
		return
	}
	if ctx.Err() != nil || s.fenced.Load() {
		return // not run: the peer sends again, on its next connection, what needs an answer
	}
	env := it.env
	if s.peer.Role != RoleBrowser {
		mine := s.cfg.Epoch.Epoch()
		if env.Epoch < mine {
			_ = s.refuseStale(env.ID, env.Epoch, mine)
			return
		}
		if env.Epoch > mine {
			if perr := s.followHigher(ctx, env.Epoch); perr != nil {
				s.sendError(env.ID, perr)
				return
			}
			if !s.stillTheRightPeer() {
				return
			}
		}
	}
	h, ok := l.handlerFor(env.Type)
	if !ok {
		if env.Type == TypeMachineHeartbeat {
			return // watched by the session itself; the application may not care
		}
		s.sendError(env.ID, NewError(CodeUnknownType, "%s has no handler in this build", env.Type))
		return
	}
	m := &Message{Env: env, Payload: it.payload, Peer: s.peer, Link: l}
	// The catalogue, not the flag of the envelope, says whether the message
	// is run once: a peer cannot skip the memory of ids by leaving it out.
	if it.spec.Ack {
		s.runAcked(ctx, m, h)
		return
	}
	reply, err := h.Handle(ctx, m)
	if err != nil {
		s.sendError(env.ID, asError(err))
		return
	}
	if reply != nil {
		s.sendReply(ctx, env.ID, reply)
	}
}

// stillTheRightPeer is checked after this machine followed the higher epoch
// of a message: a Tower runs only what the Citadel of that epoch sends, and a
// Citadel that the new epoch replaced is no longer one. Either way the
// connection closes with 4007 and nothing of the message runs.
func (s *Session) stillTheRightPeer() bool {
	switch s.side {
	case SideTower:
		if c := s.cfg.Epoch.Certificate(); c != nil && c.CitadelMachineID != s.peer.ID {
			s.fenced.Store(true)
			s.closeWith(CloseNotCitadel, "not the Citadel of the epoch",
				fmt.Errorf("%w: epoch %d names %s, not %s", ErrNotCitadel, c.Epoch, c.CitadelMachineID, s.peer.ID))
			return false
		}
	case SideCitadel:
		if s.cfg.IsCitadel != nil && !s.cfg.IsCitadel() {
			s.fenced.Store(true)
			s.closeWith(CloseNotCitadel, "this machine is not the Citadel", fmt.Errorf("%w: this machine followed a higher epoch", ErrNotCitadel))
			return false
		}
	}
	return true
}

// ErrNotCitadel is the cause of a session closed with 4007 after a higher
// epoch was followed.
var ErrNotCitadel = errors.New("protocol: not the Citadel of the epoch")

// runAcked is the idempotency of the receiver: the id is claimed durably
// before the handler runs, so a repeat of it, after a reconnection, gets
// "duplicate" and runs nothing. The ids are kept per link, not per machine:
// on a Tower the sender is "the Citadel", whichever machine it is, because
// the outbox that a promoted Citadel sends again is the replicated one of the
// old Citadel, with the same ids.
func (s *Session) runAcked(ctx context.Context, m *Message, h Handler) {
	env, key := m.Env, s.link.key
	fresh, prev, err := s.cfg.Processed.Claim(ctx, key, env.ID, env.Type, s.cfg.Clock.Now())
	if err != nil {
		s.sendError(env.ID, NewError(CodeInternal, "idempotency store: %v", err))
		return
	}
	if !fresh {
		var first Ack
		if prev != nil {
			_ = json.Unmarshal(prev, &first)
		}
		s.sendAck(env.ID, Ack{Status: AckDuplicate, Error: first.Error, Result: first.Result})
		return
	}
	reply, herr := h.Handle(ctx, m)
	if herr != nil {
		pe := asError(herr)
		if pe.Retryable {
			if err := s.cfg.Processed.Release(ctx, key, env.ID); err != nil {
				s.cfg.Logger.Error("protocol: release", "id", env.ID, "err", err)
			}
		} else {
			s.finish(ctx, env.ID, Ack{Status: AckError, Error: pe})
		}
		s.sendAck(env.ID, Ack{Status: AckError, Error: pe})
		return
	}
	ack := Ack{Status: AckOK}
	if reply != nil && reply.InAck {
		raw, err := s.cfg.Registry.Marshal(reply.Type, reply.Payload, true)
		if err != nil {
			pe := asError(err)
			s.finish(ctx, env.ID, Ack{Status: AckError, Error: pe})
			s.sendAck(env.ID, Ack{Status: AckError, Error: pe})
			return
		}
		ack.Result = raw
	} else if reply != nil {
		s.sendReply(ctx, env.ID, reply)
	}
	s.finish(ctx, env.ID, ack)
	s.sendAck(env.ID, ack)
}

func (s *Session) finish(ctx context.Context, id string, a Ack) {
	if err := s.cfg.Processed.Finish(ctx, s.link.key, id, jsonOf(a)); err != nil {
		s.cfg.Logger.Error("protocol: processed finish", "id", id, "err", err)
	}
}

func (s *Session) sendReply(ctx context.Context, to string, r *Reply) {
	if _, err := s.link.Send(ctx, Outgoing{Type: r.Type, Payload: r.Payload, ReplyTo: to, Transient: r.Transient}); err != nil && !errors.Is(err, ErrNotConnected) {
		s.cfg.Logger.Error("protocol: reply", "to", to, "type", r.Type, "err", err)
	}
}

// ErrStaleEpoch is the cause of a session that ended because of an epoch.
var ErrStaleEpoch = errors.New("protocol: stale epoch")

// enqueue stamps the machine and the epoch on a message, which is why a
// message of the outbox can be sent to a Citadel of a newer epoch, and puts
// it on the control lane. It sends nothing once the peer has said our epoch
// is old.
func (s *Session) enqueue(env Envelope) error {
	if s.stale.Load() {
		return ErrPeerStale
	}
	return s.enqueueRaw(env)
}

func (s *Session) enqueueRaw(env Envelope) error {
	env.MachineID = s.cfg.MachineID
	env.Epoch = s.cfg.Epoch.Epoch()
	b, err := Encode(env)
	if err != nil {
		return err
	}
	return s.lanes.pushControl(Frame{Kind: FrameText, Data: b})
}

func (s *Session) newEnvelope(typ string, payload any, replyTo string, ack bool) Envelope {
	return Envelope{
		ID: s.link.gen.NewString(), Type: typ, TS: s.link.now(), Payload: jsonOf(payload), ReplyTo: replyTo, Ack: ack,
	}
}

func (s *Session) sendAck(replyTo string, a Ack) {
	_ = s.enqueueRaw(s.newEnvelope(TypeAck, a, replyTo, false))
}

func (s *Session) sendError(replyTo string, e *Error) {
	_ = s.enqueueRaw(s.newEnvelope(TypeError, e, replyTo, false))
}

// sendTyped sends a message with no outbox, checked against the catalogue.
func (s *Session) sendTyped(typ string, payload any) error {
	raw, err := s.cfg.Registry.Marshal(typ, payload, false)
	if err != nil {
		return err
	}
	env := s.newEnvelope(typ, nil, "", false)
	env.Payload = raw
	return s.enqueue(env)
}

// followHigher is the receiver's side of the epoch: the sender follows a
// higher epoch, so ask it for the certificate, check the signature and, if
// valid, follow it. A Citadel that does so demotes itself before anything
// else is done with the message.
func (s *Session) followHigher(ctx context.Context, claimed uint64) *Error {
	cert, err := s.fetchCertificate(ctx)
	if err != nil {
		return NewError(CodeUnavailable, "cannot get the certificate of epoch %d: %v", claimed, err)
	}
	return s.follow(ctx, cert, claimed)
}

// follow checks cert, which must prove at least the epoch claimed, and makes
// the machine follow it if it is higher than ours.
func (s *Session) follow(ctx context.Context, cert Certificate, claimed uint64) *Error {
	return s.cfg.follow(ctx, cert, claimed)
}

func (s *Session) fetchCertificate(ctx context.Context) (Certificate, error) {
	id := s.link.gen.NewString()
	ch := make(chan Certificate, 1)
	s.mu.Lock()
	s.certs[id] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.certs, id)
		s.mu.Unlock()
	}()
	env := s.newEnvelope(TypeGuildEpoch, Empty{}, "", false)
	env.ID = id
	if err := s.enqueueRaw(env); err != nil {
		return Certificate{}, err
	}
	ctx, cancel := clock.WithTimeout(ctx, s.cfg.Clock, s.cfg.Timing.CertTimeout)
	defer cancel()
	select {
	case c := <-ch:
		return c, nil
	case <-ctx.Done():
		return Certificate{}, fmt.Errorf("no answer: %w", ctx.Err())
	}
}

func (s *Session) deliverCertificate(env Envelope, c *Certificate) {
	s.mu.Lock()
	ch := s.certs[env.ReplyTo]
	s.mu.Unlock()
	if ch != nil {
		select {
		case ch <- *c:
		default:
		}
	}
}

// answerEpoch answers a guild.epoch request with the certificate this
// machine follows, at once, on the read loop: the answer is the
// confirmation, it goes on this connection only (never the outbox) and
// nothing is claimed, because reading the certificate twice is harmless.
func (s *Session) answerEpoch(req Envelope) {
	c := s.cfg.Epoch.Certificate()
	if c == nil {
		s.sendError(req.ID, NewError(CodeNotFound, "this machine has no certificate of epoch"))
		return
	}
	_ = s.enqueueRaw(s.newEnvelope(TypeGuildEpoch, c, req.ID, false))
}

// peerFloor is the outbox.floor the peer sent on this connection, or "" if
// none came in the last two rounds of the prune: a floor that is not kept
// fresh is not trusted.
func (s *Session) peerFloor() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.floor == "" || clock.Since(s.cfg.Clock, s.floorAt) > 2*s.cfg.Timing.ProcessedPrune {
		return ""
	}
	return s.floor
}

func (s *Session) heartbeatSeen() {
	if s.hb == nil {
		return
	}
	s.hb.Reset(s.cfg.Timing.HeartbeatLost)
}

func (s *Session) heartbeatLost() {
	if h := s.cfg.Hooks.HeartbeatLost; h != nil {
		h(s.peer)
	}
}

// keepAlive, for a Conn that is a Pinger, pings every Timing.Ping; a ping that gets no pong in
// Timing.PongTimeout ends the session, whether the connection was cut with
// no warning or is half open.
func (s *Session) keepAlive(ctx context.Context) error {
	t := s.cfg.Clock.NewTicker(s.cfg.Timing.Ping)
	defer t.Stop()
	for {
		select {
		case <-t.C():
		case <-ctx.Done():
			return nil
		}
		pctx, cancel := clock.WithTimeout(ctx, s.cfg.Clock, s.cfg.Timing.PongTimeout)
		err := s.conn.(Pinger).Ping(pctx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("%w: no pong in %s: %v", ErrDead, s.cfg.Timing.PongTimeout, err)
		}
	}
}

// ErrDead is the cause of a session that ended because the peer stopped
// answering pings.
var ErrDead = errors.New("protocol: connection is dead")

func (s *Session) heartbeatLoop(ctx context.Context) error {
	t := s.cfg.Clock.NewTicker(s.cfg.Timing.Heartbeat)
	defer t.Stop()
	for {
		hb, err := s.cfg.Heartbeat(ctx)
		if err != nil {
			s.cfg.Logger.Warn("protocol: heartbeat numbers", "err", err)
		} else if err := s.sendTyped(TypeMachineHeartbeat, hb); err != nil {
			s.cfg.Logger.Warn("protocol: heartbeat", "err", err)
		}
		select {
		case <-t.C():
		case <-ctx.Done():
			return nil
		}
	}
}
