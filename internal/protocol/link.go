package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// Errors of sending.
var (
	// ErrNotConnected is returned for a message that has no ack and so is not
	// kept for later: there is no connection to put it on.
	ErrNotConnected = errors.New("protocol: not connected")
	// ErrPeerStale is returned while the peer is refusing this machine for
	// its epoch: nothing more is sent on that connection.
	ErrPeerStale = errors.New("protocol: the peer refused our epoch")
)

// Outgoing is a message to send.
type Outgoing struct {
	Type    string
	Payload any
	// ReplyTo is the id of the message this one answers.
	ReplyTo string
	// ID is optional: set it to send again a message that the peer refused
	// with a retryable error (the same id is the same message).
	ID string
	// Transient keeps a message that demands an ack out of the outbox: it
	// goes only on the connection that is open now, or fails with
	// ErrNotConnected. The transfer machine recovers by itself after a
	// reconnection; a request that needs an answer now (cli.call) must not
	// run later, after its caller gave up.
	Transient bool
}

// AckResult is how the peer answered a message that demanded an ack.
type AckResult struct {
	// Status is ack's ok, duplicate or error.
	Status string
	Error  *Error
}

// Link is the relation of this machine with one peer. It outlives the
// connections: it keeps the outbox, resends it on every new connection and
// is where the application sends.
type Link struct {
	cfg  *Config
	key  string // outbox key of the peer
	side Side   // the side of this machine
	gen  *IDGen
	xfer *xferHub

	// turn makes the messages of the link run one at a time, also while an
	// older connection that is being replaced still has a message in its
	// handler: whoever runs one holds its only token.
	turn chan struct{}

	mu      sync.Mutex
	sess    *Session
	order   uint64        // last order given by admit
	up      chan struct{} // closed while there is a connection
	seq     uint64        // order of the messages put in this process, durable or volatile
	putSeq  map[string]uint64
	vol     []volatileMsg // messages of volatile types, in memory only
	waiters map[string]chan AckResult
}

// volatileMsg is a message of a volatile type that waits for its ack, with
// its place among the messages put in this process.
type volatileMsg struct {
	env Envelope
	seq uint64
}

func newLink(cfg *Config, key string, side Side) *Link {
	l := &Link{
		cfg: cfg, key: key, side: side, gen: NewIDGen(cfg.Clock), waiters: map[string]chan AckResult{}, up: make(chan struct{}),
		putSeq: map[string]uint64{}, turn: make(chan struct{}, 1),
	}
	l.xfer = newXferHub(l)
	return l
}

// Session is the current connection, or nil.
func (l *Link) Session() *Session {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sess
}

// Connected says whether there is a connection now.
func (l *Link) Connected() bool { return l.Session() != nil }

func (l *Link) now() Timestamp { return Timestamp{l.cfg.Clock.Now().UTC()} }

// Send validates the message against the catalogue and sends it. A type
// that demands an ack is first put in the outbox, so Send succeeds, and the
// message goes out whenever there is a connection, even if there is none now.
// A type with no ack is sent now or fails with ErrNotConnected. It returns
// the id.
func (l *Link) Send(ctx context.Context, o Outgoing) (string, error) {
	env, spec, err := l.build(&o)
	if err != nil {
		return "", err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if spec.Ack && !o.Transient {
		if err := l.keep(ctx, env, spec.Volatile); err != nil {
			return "", err
		}
	}
	if l.sess == nil {
		if spec.Ack && !o.Transient {
			return env.ID, nil
		}
		return env.ID, ErrNotConnected
	}
	if err := l.sess.enqueue(env); err != nil {
		if spec.Ack && !o.Transient {
			return env.ID, nil // stays in the outbox for the next connection
		}
		return env.ID, err
	}
	return env.ID, nil
}

// keep puts a message that waits for its ack in the outbox, or in memory for
// a volatile type, and notes its place in the line. A message that is
// already kept keeps its place. The caller holds l.mu.
func (l *Link) keep(ctx context.Context, env Envelope, volatile bool) error {
	if _, ok := l.putSeq[env.ID]; ok {
		return nil
	}
	if volatile {
		for _, v := range l.vol {
			if v.env.ID == env.ID {
				return nil
			}
		}
	} else if err := l.cfg.Outbox.Put(ctx, l.key, env); err != nil {
		return fmt.Errorf("outbox: %w", err)
	}
	l.seq++
	l.putSeq[env.ID] = l.seq
	if volatile {
		l.vol = append(l.vol, volatileMsg{env: env, seq: l.seq})
	}
	return nil
}

func (l *Link) build(o *Outgoing) (Envelope, Spec, error) {
	spec, ok := l.cfg.Registry.Spec(o.Type)
	if !ok {
		return Envelope{}, Spec{}, NewError(CodeUnknownType, "type %q is not known in this version", o.Type)
	}
	if !spec.Dir.Allows(l.side) {
		return Envelope{}, Spec{}, NewError(CodeForbidden, "this side does not send %s", o.Type)
	}
	raw, err := l.cfg.Registry.Marshal(o.Type, o.Payload, o.ReplyTo != "")
	if err != nil {
		return Envelope{}, Spec{}, err
	}
	if o.ID == "" {
		o.ID = l.gen.NewString()
	}
	env := Envelope{ID: o.ID, Type: o.Type, TS: l.now(), Payload: raw, ReplyTo: o.ReplyTo, Ack: spec.Ack}
	if _, err := Encode(env); err != nil {
		return Envelope{}, Spec{}, err
	}
	return env, spec, nil
}

// Call sends a message that demands an ack and waits for it. The ack means
// "received and accepted"; the outcome of the command comes later, as a
// message of its own.
func (l *Link) Call(ctx context.Context, o Outgoing) (AckResult, error) {
	spec, ok := l.cfg.Registry.Spec(o.Type)
	if !ok || !spec.Ack {
		return AckResult{}, fmt.Errorf("protocol: %s does not demand an ack", o.Type)
	}
	if o.ID == "" {
		o.ID = l.gen.NewString()
	}
	ch, release := l.track(o.ID)
	defer release()
	if _, err := l.Send(ctx, o); err != nil {
		return AckResult{}, err
	}
	select {
	case r := <-ch:
		return r, nil
	case <-ctx.Done():
		return AckResult{}, ctx.Err()
	}
}

// track registers a waiter for the ack (or the refusal) of the message with
// this id; release removes it.
func (l *Link) track(id string) (ch chan AckResult, release func()) {
	ch = make(chan AckResult, 1)
	l.mu.Lock()
	l.waiters[id] = ch
	l.mu.Unlock()
	return ch, func() {
		l.mu.Lock()
		delete(l.waiters, id)
		l.mu.Unlock()
	}
}

// SendData queues a binary frame on the current connection, waiting if the
// data queue is full.
func (l *Link) SendData(ctx context.Context, f DataFrame) error {
	s := l.Session()
	if s == nil {
		return ErrNotConnected
	}
	b, err := EncodeData(f)
	if err != nil {
		return err
	}
	return s.lanes.pushData(ctx, Frame{Kind: FrameBinary, Data: b})
}

// errSuperseded is returned by attach to a connection that a newer one of the
// same machine already took the place of.
var errSuperseded = errors.New("protocol: replaced by a newer connection")

// admit gives the next order of the link to a connection that was just
// accepted, before its welcome goes out: the newest connection is the one
// accepted last, whatever the order in which they attach.
func (l *Link) admit() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.order++
	return l.order
}

// attach makes s the current connection and puts on it, in order, everything
// that still waits for an ack. A connection that was current is closed with
// 4004. If the current one is newer than s (s was accepted first but got here
// later), s is the one that is not taken: attach returns errSuperseded and
// the link stays as it was.
func (l *Link) attach(ctx context.Context, s *Session) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sess != nil && l.sess.order > s.order {
		return errSuperseded
	}
	pending, err := l.cfg.Outbox.Pending(ctx, l.key)
	if err != nil {
		return fmt.Errorf("outbox: %w", err)
	}
	old := l.sess
	l.sess = s
	if old == nil {
		close(l.up)
	}
	resend := l.inOrder(pending)
	for _, env := range resend {
		if err := s.enqueue(env); err != nil {
			return err
		}
	}
	// After what it sends again, the floor: nothing of ours below it can come
	// again, so the peer may forget those ids.
	l.sendFloor(s, resend)
	if old != nil && old != s {
		old.Close(CloseReplaced, "replaced by a newer connection")
	}
	return nil
}

// inOrder puts the volatile messages among the durable ones in the order
// they were put: a durable message of an earlier process goes first, then
// every one of this process by its place in the line. The caller holds l.mu.
func (l *Link) inOrder(pending []Envelope) []Envelope {
	out := make([]Envelope, 0, len(pending)+len(l.vol))
	vi := 0
	for _, env := range pending {
		if seq, ok := l.putSeq[env.ID]; ok {
			for vi < len(l.vol) && l.vol[vi].seq < seq {
				out = append(out, l.vol[vi].env)
				vi++
			}
		}
		out = append(out, env)
	}
	for ; vi < len(l.vol); vi++ {
		out = append(out, l.vol[vi].env)
	}
	return out
}

// sendFloor tells the peer the lowest id this machine may still send it
// again: the oldest of what waits for an ack, or, with nothing waiting, an
// id newer than every one already used. The ids are ULIDs, ordered as the
// strings are. The caller holds l.mu and passes what waits now.
func (l *Link) sendFloor(s *Session, waiting []Envelope) {
	if _, ok := l.cfg.Registry.Spec(TypeOutboxFloor); !ok {
		return
	}
	floor := l.gen.NewString()
	for _, env := range waiting {
		if env.ID < floor {
			floor = env.ID
		}
	}
	if err := s.sendTyped(TypeOutboxFloor, OutboxFloor{ID: floor}); err != nil && !errors.Is(err, ErrPeerStale) && !errors.Is(err, ErrSessionClosed) {
		l.cfg.Logger.Warn("protocol: outbox floor", "peer", l.key, "err", err)
	}
}

// refreshFloor sends the floor again on the current connection, so the
// peer can forget the ids of a connection that lasts long.
func (l *Link) refreshFloor(ctx context.Context) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sess == nil {
		return
	}
	pending, err := l.cfg.Outbox.Pending(ctx, l.key)
	if err != nil {
		l.cfg.Logger.Warn("protocol: outbox floor", "peer", l.key, "err", err)
		return
	}
	l.sendFloor(l.sess, l.inOrder(pending))
}

// detach takes s off the link when it ends. Only the current connection
// releases the files of the transfers: an older one that ends after it was
// replaced must not close what the newer one is writing.
func (l *Link) detach(s *Session) {
	l.mu.Lock()
	current := l.sess == s
	if current {
		l.sess = nil
		l.up = make(chan struct{})
	}
	l.mu.Unlock()
	if current {
		l.xfer.releaseFiles()
	}
}

// waitConnected returns the current connection, waiting for one if there is
// none.
func (l *Link) waitConnected(ctx context.Context) (*Session, error) {
	for {
		l.mu.Lock()
		s, up := l.sess, l.up
		l.mu.Unlock()
		if s != nil {
			return s, nil
		}
		select {
		case <-up:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// ackArrived is the answer of the handler of the peer to a message of ours.
// Whatever its status the message leaves the outbox: with a retryable error
// it is the sender that decides whether to send it again, with the same id
// (docs/conectividade/protocolo.md#ack-e-idempotência).
func (l *Link) ackArrived(ctx context.Context, id string, a *Ack) {
	l.finish(ctx, id, AckResult{Status: a.Status, Error: a.Error}, true)
}

// errorArrived is a refusal in place of the ack: the message did not reach a
// handler. Unless it can work when sent again, or it only says that our
// epoch is old (the message goes to the right Citadel later), the message
// leaves the outbox: it would only be refused again.
func (l *Link) errorArrived(ctx context.Context, id string, e *Error) {
	l.finish(ctx, id, AckResult{Status: AckError, Error: e}, !e.Retryable && e.Code != CodeStaleEpoch)
}

func (l *Link) finish(ctx context.Context, id string, r AckResult, drop bool) {
	if drop {
		if err := l.cfg.Outbox.Done(ctx, l.key, id); err != nil {
			l.cfg.Logger.Error("protocol: outbox done", "id", id, "err", err)
		}
	}
	l.mu.Lock()
	if drop {
		delete(l.putSeq, id)
		for i, v := range l.vol {
			if v.env.ID == id {
				l.vol = append(l.vol[:i], l.vol[i+1:]...)
				break
			}
		}
	}
	ch := l.waiters[id]
	l.mu.Unlock()
	if ch != nil {
		select {
		case ch <- r:
		default:
		}
	}
}

// handlerFor finds who runs a type: the machinery of the protocol first, then
// the application.
func (l *Link) handlerFor(typ string) (Handler, bool) {
	switch typ {
	case TypeXferBegin, TypeXferEnd, TypeXferReady, TypeXferProgress, TypeXferDone:
		return l.xfer, true
	}
	return l.cfg.Router.lookup(typ)
}

// jsonOf encodes v, which always encodes for the protocol types.
func jsonOf(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
