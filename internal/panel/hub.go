package panel

import (
	"context"
	"encoding/json"
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
	sendQueue    = 256
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
}

// Hub is the channel of the browser: it keeps the sockets of /ws/ui and fans the
// events out to the subscribers of each topic.
type Hub struct {
	p *Panel

	mu     sync.Mutex
	conns  map[*uiConn]struct{}
	topics map[string]Topic
}

func newHub(p *Panel) *Hub {
	return &Hub{p: p, conns: map[*uiConn]struct{}{}, topics: map[string]Topic{}}
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
}

func (c *uiConn) subscribed(topic string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.topics[topic]
}

// enqueue puts a message on the queue of the socket; text that does not fit
// closes the socket, binary that does not fit is lost.
func (c *uiConn) enqueue(typ websocket.MessageType, data []byte) {
	select {
	case c.send <- outgoing{typ, data}:
	default:
		if typ == websocket.MessageText {
			c.close(websocket.StatusPolicyViolation, "too slow")
		}
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
	c := &uiConn{ws: ws, user: UserOf(r), sessionID: SessionOf(r).ID, cancel: cancel, send: make(chan outgoing, sendQueue), topics: map[string]bool{}}
	p.hub.add(c)
	defer func() {
		p.hub.remove(c)
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
		h.reply(c, TypeUIError, env.ID, Err(CodeBadRequest, "tipo de mensagem desconhecido", map[string]any{"type": env.Type}))
		return
	}
	if env.Type == TypeUIUnsubscribe {
		c.mu.Lock()
		for _, t := range in.Topics {
			delete(c.topics, t)
		}
		c.mu.Unlock()
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
		c.mu.Lock()
		c.topics[topic] = true
		c.mu.Unlock()
		ok = append(ok, topic)
		if t.Snapshot == nil {
			continue
		}
		snap, err := t.Snapshot(ctx, u, id)
		if err != nil {
			h.p.log.Warn("panel: the snapshot of a topic failed", "topic", topic, "err", err)
			continue
		}
		d, _ := json.Marshal(snap)
		if b, err := h.envelope(TypeUIEvent, env.ID, UIEvent{Topic: topic, Event: EventSnapshot, Data: d}); err == nil {
			c.enqueue(websocket.MessageText, b)
		}
	}
	h.reply(c, TypeUISubscribed, env.ID, UISubscribed{Topics: ok, Rejected: rejected})
}
