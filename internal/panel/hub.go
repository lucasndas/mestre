package panel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/coder/websocket"

	"github.com/mestre-ia/mestre/internal/users"
)

const (
	// sendQueue is how many messages wait for a slow browser; a browser that lets
	// the queue of events fill is closed, and one that lets the binary frames
	// fill loses them (it asks for the replay again).
	sendQueue = 256
	// frameQueue is how many of those places the binary frames may take: the
	// text (events, answers, the picture of a screen) keeps the rest.
	frameQueue   = sendQueue * 3 / 4
	maxUIMessage = 1 << 20
)

// Topic is what the Panel needs to serve one kind of topic ("workspace",
// "table", "terminal"): who may subscribe, and the snapshot that opens it. The
// areas register theirs with Hub.Provide.
type Topic struct {
	// Authorize says whether the user may follow the topic of that id (nil: any
	// user of the panel may).
	Authorize func(u users.User, id string) bool
	// Snapshot is the state the subscriber gets first (nil: no snapshot).
	Snapshot func(ctx context.Context, u users.User, id string) (any, error)
	// Open is called once the subscription is accepted and its snapshot queued:
	// the area starts what it pushes to the subscriber (the stream of a terminal)
	// and returns what ends it, called when the subscriber leaves the topic, when
	// its socket closes, and before a second subscribe to the same topic opens
	// again (nil: nothing to start).
	Open func(ctx context.Context, sub *Subscription, u users.User, id string) (stop func())
}

// MessageHandler runs a message of the browser that the hub does not know (the
// areas register theirs with Hub.HandleMessage). It runs in the reading
// goroutine of the socket, one message at a time, so it must return soon.
type MessageHandler func(ctx context.Context, s Socket, env UIEnvelope)

// Socket is one browser socket, as the handlers of an area see it.
type Socket struct {
	h *Hub
	c *uiConn
}

// Subscription is a socket that follows a topic.
type Subscription struct {
	Socket
	Topic, ID string
	// State is whatever the area keeps for the subscription (set by Open).
	State any
}

// Subscription is the subscription of the socket to the topic, nil if none.
func (s Socket) Subscription(topic string) *Subscription {
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	return s.c.subs[topic]
}

// Fresh reads the user again: the level may have changed since the socket
// opened. It is false when the user is gone, and the socket is then closed.
func (s Socket) Fresh(ctx context.Context) (users.User, bool) {
	u, err := s.h.p.users.Get(ctx, s.c.user.ID)
	if err != nil || u.State != users.Active {
		s.c.close(CloseSessionEnded, "session ended")
		return users.User{}, false
	}
	return u, true
}

// SessionID is the session the socket belongs to.
func (s Socket) SessionID() string { return s.c.sessionID }

// Reply sends a message to the browser, answering replyTo.
func (s Socket) Reply(typ, replyTo string, payload any) { s.h.reply(s.c, typ, replyTo, payload) }

// Fail answers replyTo with an error.
func (s Socket) Fail(replyTo string, e *APIError) { s.h.reply(s.c, TypeUIError, replyTo, e) }

// Event sends a ui.event of the topic to this socket only.
func (s Socket) Event(topic, event string, data any) {
	d, err := json.Marshal(data)
	if err != nil {
		return
	}
	if b, err := s.h.envelope(TypeUIEvent, "", UIEvent{Topic: topic, Event: event, Data: d}); err == nil {
		s.c.enqueue(websocket.MessageText, b)
	}
}

// Frame queues a binary frame for this socket; false when its queue is full (the
// frame is lost and the caller must resynchronise).
func (s Socket) Frame(frame []byte) bool { return s.c.enqueue(websocket.MessageBinary, frame) }

// HandleMessage registers the handler of a type of message of the browser.
func (h *Hub) HandleMessage(typ string, f MessageHandler) {
	h.mu.Lock()
	h.msgs[typ] = f
	h.mu.Unlock()
}

// Hub is the channel of the browser: it keeps the sockets of /ws/ui and fans the
// events out to the subscribers of each topic.
type Hub struct {
	p *Panel

	mu     sync.Mutex
	conns  map[*uiConn]struct{}
	topics map[string]Topic
	msgs   map[string]MessageHandler
}

func newHub(p *Panel) *Hub {
	return &Hub{p: p, conns: map[*uiConn]struct{}{}, topics: map[string]Topic{}, msgs: map[string]MessageHandler{}}
}

// Provide registers the kind of topic.
func (h *Hub) Provide(kind string, t Topic) {
	h.mu.Lock()
	h.topics[kind] = t
	h.mu.Unlock()
}

type outgoing struct {
	typ  websocket.MessageType
	data []byte
}

type uiConn struct {
	ws        *websocket.Conn
	user      users.User
	sessionID string
	cancel    context.CancelFunc
	send      chan outgoing

	mu     sync.Mutex
	topics map[string]bool
	subs   map[string]*Subscription
	stops  map[string]func()
}

// leave ends the subscription to the topic: what the area started stops.
func (c *uiConn) leave(topic string) {
	c.mu.Lock()
	delete(c.topics, topic)
	delete(c.subs, topic)
	stop := c.stops[topic]
	delete(c.stops, topic)
	c.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// leaveAll ends every subscription (the socket closed).
func (c *uiConn) leaveAll() {
	c.mu.Lock()
	topics := make([]string, 0, len(c.stops))
	for t := range c.stops {
		topics = append(topics, t)
	}
	c.mu.Unlock()
	for _, t := range topics {
		c.leave(t)
	}
}

func (c *uiConn) subscribed(topic string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.topics[topic]
}

// enqueue puts a message on the queue of the socket; text that does not fit
// closes the socket, binary that does not fit is lost.
func (c *uiConn) enqueue(typ websocket.MessageType, data []byte) bool {
	if typ == websocket.MessageBinary && len(c.send) >= frameQueue {
		return false // the rest of the queue is for the text, which must never be lost
	}
	select {
	case c.send <- outgoing{typ, data}:
		return true
	default:
		if typ == websocket.MessageText {
			c.close(websocket.StatusPolicyViolation, "too slow")
		}
		return false
	}
}

func (c *uiConn) close(code websocket.StatusCode, reason string) {
	// the close handshake first: cancelling the context before it would drop
	// the connection without the code
	go func() {
		_ = c.ws.Close(code, reason)
		c.cancel()
	}()
}

func (h *Hub) envelope(typ, replyTo string, payload any) ([]byte, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(UIEnvelope{ID: h.p.ids.NewString(), Type: typ, TS: h.p.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z"), Payload: b, ReplyTo: replyTo})
}

func (h *Hub) snapshot() []*uiConn {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*uiConn, 0, len(h.conns))
	for c := range h.conns {
		out = append(out, c)
	}
	return out
}

// Publish sends an event to the browsers that follow the topic.
func (h *Hub) Publish(topic, event string, data any) error {
	d, err := json.Marshal(data)
	if err != nil {
		return err
	}
	msg, err := h.envelope(TypeUIEvent, "", UIEvent{Topic: topic, Event: event, Data: d})
	if err != nil {
		return err
	}
	for _, c := range h.snapshot() {
		if c.subscribed(topic) {
			c.enqueue(websocket.MessageText, msg)
		}
	}
	return nil
}

// PublishFrame sends a binary frame (the output of a terminal, type 0x01) to the
// browsers that follow the topic, without keeping it.
func (h *Hub) PublishFrame(topic string, frame []byte) {
	for _, c := range h.snapshot() {
		if c.subscribed(topic) {
			c.enqueue(websocket.MessageBinary, frame)
		}
	}
}

// Topics lists the topics of the kind ("canvas") that some socket follows now.
func (h *Hub) Topics(kind string) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range h.snapshot() {
		c.mu.Lock()
		for t := range c.topics {
			if k, _, _ := strings.Cut(t, ":"); k == kind && !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
		c.mu.Unlock()
	}
	return out
}

// Subscribers is how many sockets follow the topic.
func (h *Hub) Subscribers(topic string) int {
	n := 0
	for _, c := range h.snapshot() {
		if c.subscribed(topic) {
			n++
		}
	}
	return n
}

// closeSession closes the sockets of a session that ended.
func (h *Hub) closeSession(id string) {
	for _, c := range h.snapshot() {
		if c.sessionID == id {
			c.close(CloseSessionEnded, "session ended")
		}
	}
}

// closeUserGone closes the sockets of the user whose session is no longer there.
func (h *Hub) closeUserGone(ctx context.Context, userID string) {
	for _, c := range h.snapshot() {
		if c.user.ID == userID && !h.p.sessions.Exists(ctx, c.sessionID) {
			c.close(CloseSessionEnded, "session ended")
		}
	}
}

func (h *Hub) closeAll() {
	for _, c := range h.snapshot() {
		c.close(websocket.StatusGoingAway, "the Citadel is stopping")
	}
}

func (h *Hub) add(c *uiConn) {
	h.mu.Lock()
	h.conns[c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) remove(c *uiConn) {
	h.mu.Lock()
	delete(h.conns, c)
	h.mu.Unlock()
}

// serveWS is GET /ws/ui: the cookie of the session and the Origin are checked
// before the upgrade.
func (p *Panel) serveWS(w http.ResponseWriter, r *http.Request) {
	if !originOK(r) {
		if _, err := r.Cookie(CookieName); err == nil {
			p.audit(r.Context(), "panel.origin_refused", map[string]any{"ip": clientIP(r), "path": clip(r.URL.Path, 200)})
		}
		WriteError(w, Err(CodeBadOrigin, "origem não permitida", nil))
		return
	}
	ctx, apiErr := p.authenticate(r)
	if apiErr != nil {
		WriteError(w, apiErr)
		return
	}
	r = r.WithContext(ctx)
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return // Accept answered
	}
	ws.SetReadLimit(maxUIMessage)
	cctx, cancel := context.WithCancel(context.Background())
	c := &uiConn{ws: ws, user: UserOf(r), sessionID: SessionOf(r).ID, cancel: cancel, send: make(chan outgoing, sendQueue), topics: map[string]bool{},
		subs: map[string]*Subscription{}, stops: map[string]func(){}}
	p.hub.add(c)
	defer func() {
		p.hub.remove(c)
		c.leaveAll()
		cancel()
		_ = ws.CloseNow()
	}()
	go func() {
		for {
			select {
			case <-cctx.Done():
				return
			case m := <-c.send:
				if err := ws.Write(cctx, m.typ, m.data); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	// the session may have ended between the check and the upgrade
	if !p.sessions.Exists(cctx, c.sessionID) {
		_ = ws.Close(CloseSessionEnded, "session ended")
		return
	}
	for {
		typ, data, err := ws.Read(cctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			_ = ws.Close(websocket.StatusUnsupportedData, "text frames only")
			return
		}
		// each message is a use of the session, and a session that is over closes the socket
		if _, err := p.sessions.Use(cctx, c.sessionID); err != nil {
			if errors.Is(err, ErrInFailover) { // the touch of the session is a write: refused while the Citadel drains
				_ = ws.Close(websocket.StatusTryAgainLater, string(CodeCitadelInFailover))
				return
			}
			_ = ws.Close(CloseSessionEnded, "session ended")
			return
		}
		p.hub.handle(cctx, c, data)
	}
}

func (h *Hub) reply(c *uiConn, typ, replyTo string, payload any) {
	if b, err := h.envelope(typ, replyTo, payload); err == nil {
		c.enqueue(websocket.MessageText, b)
	}
}

func (h *Hub) handle(ctx context.Context, c *uiConn, data []byte) {
	var env UIEnvelope
	if err := json.Unmarshal(data, &env); err != nil || env.ID == "" {
		h.reply(c, TypeUIError, "", errBadRequest())
		return
	}
	var in UISubscribe
	switch env.Type {
	case TypeUISubscribe, TypeUIUnsubscribe:
		if err := json.Unmarshal(env.Payload, &in); err != nil || len(in.Topics) == 0 || len(in.Topics) > 64 {
			h.reply(c, TypeUIError, env.ID, errBadRequest())
			return
		}
	default:
		h.mu.Lock()
		f := h.msgs[env.Type]
		h.mu.Unlock()
		if f == nil {
			h.reply(c, TypeUIError, env.ID, Err(CodeBadRequest, "tipo de mensagem desconhecido", map[string]any{"type": env.Type}))
			return
		}
		f(ctx, Socket{h, c}, env)
		return
	}
	if env.Type == TypeUIUnsubscribe {
		for _, t := range in.Topics {
			c.leave(t)
		}
		h.reply(c, TypeUISubscribed, env.ID, UISubscribed{Topics: []string{}, Rejected: []UIRejected{}})
		return
	}
	// the level is read again: the user of the socket may have changed meanwhile,
	// though a change of level ends the session and the socket with it
	u, err := h.p.users.Get(ctx, c.user.ID)
	if err != nil || u.State != users.Active {
		c.close(CloseSessionEnded, "session ended")
		return
	}
	ok, rejected := []string{}, []UIRejected{}
	for _, topic := range in.Topics {
		kind, id, found := strings.Cut(topic, ":")
		h.mu.Lock()
		t, known := h.topics[kind]
		h.mu.Unlock()
		switch {
		case !found || id == "" || !known:
			rejected = append(rejected, UIRejected{Topic: topic, Code: CodeUnknownTopic})
			continue
		case t.Authorize != nil && !t.Authorize(u, id):
			rejected = append(rejected, UIRejected{Topic: topic, Code: CodeForbiddenLevel})
			continue
		}
		c.leave(topic) // a second subscribe starts the topic again
		sub := &Subscription{Socket: Socket{h, c}, Topic: topic, ID: id}
		c.mu.Lock()
		c.topics[topic] = true
		c.subs[topic] = sub
		c.mu.Unlock()
		ok = append(ok, topic)
		if t.Snapshot != nil {
			snap, err := t.Snapshot(ctx, u, id)
			if err != nil {
				h.p.log.Warn("panel: the snapshot of a topic failed", "topic", topic, "err", err)
			} else {
				d, _ := json.Marshal(snap)
				if b, err := h.envelope(TypeUIEvent, env.ID, UIEvent{Topic: topic, Event: EventSnapshot, Data: d}); err == nil {
					c.enqueue(websocket.MessageText, b)
				}
			}
		}
		if t.Open != nil {
			if stop := t.Open(ctx, sub, u, id); stop != nil {
				c.mu.Lock()
				c.stops[topic] = stop
				c.mu.Unlock()
			}
		}
	}
	h.reply(c, TypeUISubscribed, env.ID, UISubscribed{Topics: ok, Rejected: rejected})
}
