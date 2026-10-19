package localchan

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
)

// Caller is who the engine found on the other end of a connection.
type Caller struct {
	// Identified is true when the process was found in a terminal of the
	// character AgentID.
	Identified bool
	// AgentID is the character: verified when Identified, otherwise what
	// the request claimed (help and debug run for callers that are not
	// identified, and say so).
	AgentID    string
	TerminalID string
	PID        int
}

// Call is a request being served, with the caller the engine verified. It
// is what a Handler and a Forwarder receive: the same request the CLI
// sent, plus the identity (docs/arquitetura/canal-local.md#encaminhamento-à-cidadela).
type Call struct {
	Request Request
	Caller  Caller
	// Gone is closed when the connection that sent the request is gone (the
	// caller gave up or died); the request goes on. Nil when the call does
	// not come from a connection.
	Gone <-chan struct{}

	publish func(json.RawMessage)
}

// Partial sends a partial response (`queued`, `delivered`, `answering`…) to
// the CLI, now and to any connection that resends the request later.
func (c *Call) Partial(data any) {
	b, err := json.Marshal(data)
	if err != nil || c.publish == nil {
		return
	}
	c.publish(b)
}

// SetPartialSink makes the partial responses of the call go to f: the
// Citadel serves the call of a Tower with it and sends each partial back as a
// cli.result; the CLI of the Tower then gets them as if the command ran there.
func (c *Call) SetPartialSink(f func(json.RawMessage)) { c.publish = f }

// Handler serves a command. It runs with the context of the server, not of
// the connection: a CLI that dies does not cancel its request.
type Handler interface {
	Handle(ctx context.Context, c *Call) Response
}

// HandlerFunc is a Handler as a function.
type HandlerFunc func(ctx context.Context, c *Call) Response

// Handle calls f.
func (f HandlerFunc) Handle(ctx context.Context, c *Call) Response { return f(ctx, c) }

// Forwarder serves the commands the engine does not resolve itself:
// everything but help, debug and hook goes to the Citadel
// (docs/arquitetura/canal-local.md#encaminhamento-à-cidadela). The
// response, partials included, is what the CLI receives.
type Forwarder interface {
	Forward(ctx context.Context, c *Call) Response
}

// LocalForwarder is the Forwarder of a machine that is its own Citadel (and
// of the tests): the call is served in this process, by the same code the
// Citadel runs for the calls it receives over `cli.call` (M2).
type LocalForwarder struct {
	mu       sync.Mutex
	handlers map[string]Handler
}

var _ Forwarder = (*LocalForwarder)(nil)

// NewLocalForwarder returns a forwarder with no commands.
func NewLocalForwarder() *LocalForwarder {
	return &LocalForwarder{handlers: map[string]Handler{}}
}

// Handle registers the handler of a command ("list", "note write").
func (l *LocalForwarder) Handle(cmd string, h Handler) {
	l.mu.Lock()
	l.handlers[cmd] = h
	l.mu.Unlock()
}

// Commands returns the registered commands, sorted.
func (l *LocalForwarder) Commands() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(l.handlers))
	for c := range l.handlers {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// Forward serves c with the handler of its command.
func (l *LocalForwarder) Forward(ctx context.Context, c *Call) Response {
	l.mu.Lock()
	h, ok := l.handlers[c.Request.Cmd]
	l.mu.Unlock()
	if !ok {
		return Fail(CodeUsage, "unknown command "+c.Request.Cmd, "mestre help lists the commands")
	}
	return h.Handle(ctx, c)
}

// UnavailableForwarder is the Forwarder of an engine with no Citadel to
// forward to (none configured yet, or it is out).
type UnavailableForwarder struct{}

var _ Forwarder = UnavailableForwarder{}

// Forward always answers citadel_unavailable.
func (UnavailableForwarder) Forward(context.Context, *Call) Response {
	return Fail(CodeCitadelUnavailable, "the Citadel is not available, the request was not made",
		"the Owner is already warned; repeat later")
}
