package protocol

import (
	"context"
	"strings"
	"sync"
)

// Message is a validated message handed to a Handler.
type Message struct {
	Env Envelope
	// Payload is a pointer to the struct of the type (see payloads.go), or
	// to the Object for the types whose fields live in another document.
	Payload any
	// Peer is the verified identity of the sender.
	Peer Peer
	// Link is the relation with the sender, to answer or to send more.
	Link *Link
}

// Reply is the result message that answers a command: it goes out with
// reply_to set to the id of the command, through the outbox if its type asks
// for an ack. The ack of the command itself only says "received and
// accepted" (docs/conectividade/protocolo.md#ack-e-idempotência).
type Reply struct {
	Type    string
	Payload any
	// Transient keeps the reply out of the outbox: it is sent only on the
	// connection that is open now (the answers of the transfer machine, which
	// recovers by itself).
	Transient bool
	// InAck puts the payload in the ack of the command (Ack.Result) instead of
	// sending a message: for the commands whose caller waits for the answer
	// inside a handler of its own, which would hold the line of the link.
	InAck bool
}

// Handler runs a message. It runs on the dispatch goroutine of the
// connection, one message at a time and in the order they arrived, so it must
// return soon: a command that takes long starts its work and answers with a
// result message later. An error is the refusal: a *Error keeps its code; any
// other becomes `internal`. A message with ack whose handler returns a
// retryable error is forgotten and may run when sent again; any other
// outcome is final and a repeat of the id gets "duplicate".
type Handler interface {
	Handle(ctx context.Context, m *Message) (*Reply, error)
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(ctx context.Context, m *Message) (*Reply, error)

// Handle calls f.
func (f HandlerFunc) Handle(ctx context.Context, m *Message) (*Reply, error) { return f(ctx, m) }

// DataHandler receives the binary frames of one kind. It runs on the read
// goroutine of the connection and must not block.
type DataHandler interface {
	HandleData(ctx context.Context, peer Peer, f DataFrame)
}

// DataHandlerFunc adapts a function to DataHandler.
type DataHandlerFunc func(ctx context.Context, peer Peer, f DataFrame)

// HandleData calls f.
func (f DataHandlerFunc) HandleData(ctx context.Context, peer Peer, df DataFrame) { f(ctx, peer, df) }

// Router maps message types to handlers. A pattern ending in ".*" matches
// every type with that prefix; an exact type wins over a pattern.
type Router struct {
	mu       sync.RWMutex
	exact    map[string]Handler
	prefixes map[string]Handler
	data     map[DataType]DataHandler
}

// NewRouter returns an empty router.
func NewRouter() *Router {
	return &Router{exact: map[string]Handler{}, prefixes: map[string]Handler{}, data: map[DataType]DataHandler{}}
}

// Handle registers h for a type or a "prefix.*" pattern.
func (r *Router) Handle(pattern string, h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := strings.CutSuffix(pattern, ".*"); ok {
		r.prefixes[p+"."] = h
		return
	}
	r.exact[pattern] = h
}

// HandleFunc registers a function.
func (r *Router) HandleFunc(pattern string, f func(ctx context.Context, m *Message) (*Reply, error)) {
	r.Handle(pattern, HandlerFunc(f))
}

// HandleData registers the receiver of a kind of binary frame.
func (r *Router) HandleData(t DataType, h DataHandler) {
	r.mu.Lock()
	r.data[t] = h
	r.mu.Unlock()
}

func (r *Router) lookup(typ string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if h, ok := r.exact[typ]; ok {
		return h, true
	}
	for p, h := range r.prefixes {
		if strings.HasPrefix(typ, p) {
			return h, true
		}
	}
	return nil, false
}

func (r *Router) dataHandler(t DataType) (DataHandler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.data[t]
	return h, ok
}
