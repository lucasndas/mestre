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

// dispatchQueue is how many validated messages wait for their handler before
// the read loop waits too.
const dispatchQueue = 4096

type dispatchItem struct {
	env     Envelope
	payload any
}

// Session is one connection, after its opening. It reads, writes with the
// two lanes, runs the handlers in order, pings, sends the heartbeat and
// watches for it.
type Session struct {
	cfg     *Config
	link    *Link
	conn    Conn
	peer    Peer
	side    Side
	version int
	caps    Capabilities
	order   uint64 // the order of acceptance on the link: the highest is the newest

	lanes *lanes
	disp  chan dispatchItem
	stale atomic.Bool
	done  chan struct{}

	mu     sync.Mutex
	cancel context.CancelCauseFunc
	cause  error // why a graceful close happened
	certs  map[string]chan Certificate
	hb     clock.Timer
}

func newSession(cfg *Config, link *Link, conn Conn, order uint64, side Side, version int, caps Capabilities) *Session {
	return &Session{
		cfg: cfg, link: link, conn: conn, order: order, peer: conn.Peer(), side: side, version: version, caps: caps,
		lanes: newLanes(), disp: make(chan dispatchItem, dispatchQueue), done: make(chan struct{}),
		certs: map[string]chan Certificate{},
	}
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
				cancel(err)
			}
		}()
	}
	start(s.readLoop)
	start(func(ctx context.Context) error { return s.lanes.run(ctx, s.conn) })
	start(s.dispatchLoop)
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
			s.onBinary(ctx, data)
		case FrameText:
			if err := s.onText(ctx, data); err != nil {
				return err
			}
		}
	}
}

func (s *Session) onBinary(ctx context.Context, data []byte) {
	f, err := DecodeData(data)
	if err != nil {
		s.cfg.Logger.Warn("protocol: bad binary frame", "peer", s.peer.ID, "err", err)
		return
	}
	if f.Type == DataXfer {
		s.link.xfer.onPart(ctx, s, f)
		return
	}
	if h, ok := s.cfg.Router.dataHandler(f.Type); ok {
		h.HandleData(ctx, s.peer, f)
		return
	}
	s.cfg.Logger.Debug("protocol: binary frame of an unknown kind", "peer", s.peer.ID, "type", f.Type)
}

// onText applies the checks in the order that matters: the form of the
// envelope, who sent it, the epoch (nothing is done for a message of an old
// epoch), then the type, the direction and the schema.
func (s *Session) onText(ctx context.Context, data []byte) error {
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
		if mine := s.cfg.Epoch.Epoch(); env.Epoch < mine {
			s.sendError(env.ID, NewError(CodeStaleEpoch, "epoch %d is older than %d", env.Epoch, mine))
			s.closeWith(CloseStaleEpoch, "stale epoch", fmt.Errorf("refused epoch %d of %s: %w", env.Epoch, s.peer.ID, ErrStaleEpoch))
			return errGraceful
		}
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
		if env.ReplyTo != "" {
			s.link.ackArrived(ctx, env.ReplyTo, payload.(*Ack))
		}
		return nil
	case TypeError:
		e := payload.(*Error)
		if e.Code == CodeStaleEpoch {
			s.stale.Store(true)
		}
		if env.ReplyTo != "" {
			s.link.errorArrived(ctx, env.ReplyTo, e)
		}
		return nil
	case TypeGuildEpoch:
		if env.ReplyTo != "" {
			s.deliverCertificate(env, payload.(*Certificate))
			return nil
		}
	case TypeMachineHeartbeat:
		s.heartbeatSeen()
	}
	select {
	case s.disp <- dispatchItem{env: env, payload: payload}:
	case <-ctx.Done():
	}
	return nil
}

func (s *Session) dispatchLoop(ctx context.Context) error {
	for {
		select {
		case it := <-s.disp:
			s.process(ctx, it)
		case <-ctx.Done():
			return nil
		}
	}
}

func (s *Session) process(ctx context.Context, it dispatchItem) {
	env := it.env
	if s.peer.Role != RoleBrowser && env.Epoch > s.cfg.Epoch.Epoch() {
		if perr := s.followHigher(ctx, env.Epoch); perr != nil {
			s.sendError(env.ID, perr)
			return
		}
	}
	h, ok := s.link.handlerFor(env.Type)
	if !ok {
		if env.Type == TypeMachineHeartbeat {
			return // watched by the session itself; the application may not care
		}
		s.sendError(env.ID, NewError(CodeUnknownType, "%s has no handler in this build", env.Type))
		return
	}
	m := &Message{Env: env, Payload: it.payload, Peer: s.peer, Link: s.link}
	if env.Ack {
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

// runAcked is the idempotency of the receiver: the id is claimed durably
// before the handler runs, so a repeat of it, after a reconnection, gets
// "duplicate" and runs nothing.
func (s *Session) runAcked(ctx context.Context, m *Message, h Handler) {
	env := m.Env
	fresh, prev, err := s.cfg.Processed.Claim(ctx, s.peer.ID, env.ID, env.Type, s.cfg.Clock.Now())
	if err != nil {
		s.sendError(env.ID, NewError(CodeInternal, "idempotency store: %v", err))
		return
	}
	if !fresh {
		var first Ack
		if prev != nil {
			_ = json.Unmarshal(prev, &first)
		}
		s.sendAck(env.ID, Ack{Status: AckDuplicate, Error: first.Error})
		return
	}
	reply, herr := h.Handle(ctx, m)
	if herr != nil {
		pe := asError(herr)
		if pe.Retryable {
			if err := s.cfg.Processed.Release(ctx, s.peer.ID, env.ID); err != nil {
				s.cfg.Logger.Error("protocol: release", "id", env.ID, "err", err)
			}
		} else {
			s.finish(ctx, env.ID, Ack{Status: AckError, Error: pe})
		}
		s.sendAck(env.ID, Ack{Status: AckError, Error: pe})
		return
	}
	if reply != nil {
		s.sendReply(ctx, env.ID, reply)
	}
	s.finish(ctx, env.ID, Ack{Status: AckOK})
	s.sendAck(env.ID, Ack{Status: AckOK})
}

func (s *Session) finish(ctx context.Context, id string, a Ack) {
	if err := s.cfg.Processed.Finish(ctx, s.peer.ID, id, jsonOf(a)); err != nil {
		s.cfg.Logger.Error("protocol: processed finish", "id", id, "err", err)
	}
}

func (s *Session) sendReply(ctx context.Context, to string, r *Reply) {
	if _, err := s.link.Send(ctx, Outgoing{Type: r.Type, Payload: r.Payload, ReplyTo: to, transient: r.Transient}); err != nil && !errors.Is(err, ErrNotConnected) {
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
	env := s.newEnvelope(TypeGuildEpoch, Empty{}, "", true)
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
	if env.Ack {
		s.sendAck(env.ID, Ack{Status: AckOK})
	}
	if ch != nil {
		select {
		case ch <- *c:
		default:
		}
	}
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
