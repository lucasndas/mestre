package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/lucasndas/mestre/pkg/clock"
)

// MessageType is the frame class of a message: control (JSON, text) or data
// (binary), docs/conectividade/protocolo.md#multiplexação-e-prioridade.
type MessageType int

// Message types.
const (
	MessageText   = MessageType(websocket.MessageText)
	MessageBinary = MessageType(websocket.MessageBinary)
)

// StatusCode is a WebSocket close code.
type StatusCode int

// Close codes: the standard ones the transport uses and the protocol ones
// (docs/conectividade/protocolo.md#heartbeat-e-reconexão).
const (
	StatusNormalClosure   StatusCode = 1000
	StatusGoingAway       StatusCode = 1001
	StatusPolicyViolation StatusCode = 1008
	StatusMessageTooBig   StatusCode = 1009
	StatusInternalError   StatusCode = 1011

	StatusInvalidCredential   StatusCode = 4001
	StatusRevoked             StatusCode = 4002
	StatusIncompatibleVersion StatusCode = 4003
	StatusReplaced            StatusCode = 4004
	StatusShuttingDown        StatusCode = 4005
	StatusStaleEpoch          StatusCode = 4006
	StatusNotCitadel          StatusCode = 4007
)

// CloseError is returned by Read once the peer closed with a code.
type CloseError struct {
	Code   StatusCode
	Reason string
}

func (e *CloseError) Error() string {
	return fmt.Sprintf("connection closed with %d %q", e.Code, e.Reason)
}

// CloseStatus is the code of the CloseError in err, or -1.
func CloseStatus(err error) StatusCode {
	var ce *CloseError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return -1
}

// Message limits (docs/conectividade/protocolo.md#envelope).
const (
	// MaxMessageSize bounds every message, text or binary.
	MaxMessageSize = 1 << 20
	// maxDiscard bounds a message over the limit that Read discards: one
	// longer than 16 MiB closes the connection with StatusMessageTooBig.
	maxDiscard = 16 << 20
)

// ErrTooLarge is returned by Write for a message over the limit (nothing is
// sent) and by Read for a received one, which was discarded: the connection
// stays open and the protocol answers too_large.
var ErrTooLarge = errors.New("message too large")

// ErrNoPong is the cause of a connection given up as dead: no pong within
// the keepalive timeout.
var ErrNoPong = errors.New("no pong from the peer: connection dead")

// Keepalive is the WebSocket ping (docs/conectividade/protocolo.md#heartbeat-e-reconexão).
// Zero values take the defaults. The pong is read by Read, so a connection
// needs a reader for the keepalive to see it.
type Keepalive struct {
	Interval time.Duration // between pings; default 15 s
	Timeout  time.Duration // without pong, the connection is dead; default 45 s
}

func (k Keepalive) withDefaults() Keepalive {
	if k.Interval <= 0 {
		k.Interval = 15 * time.Second
	}
	if k.Timeout <= 0 {
		k.Timeout = 45 * time.Second
	}
	return k
}

// Conn is one message-oriented connection between two machines of the
// Guild. Read must have a single caller at a time; Write may be called
// concurrently (writes are serialised whole). Canceling the context of a
// Read or a Write that is in progress closes the connection.
type Conn interface {
	// Read returns the next whole message. After the peer closes, the error
	// is a *CloseError (see CloseStatus).
	Read(ctx context.Context) (MessageType, []byte, error)
	// Write sends one message; over MaxMessageSize it returns ErrTooLarge.
	Write(ctx context.Context, typ MessageType, p []byte) error
	// Close sends code and reason (at most 123 bytes) and waits for the
	// peer's close, up to 5 s.
	Close(code StatusCode, reason string) error
	// CloseNow drops the connection without the close handshake.
	CloseNow() error
	// Peer is the authenticated other side.
	Peer() Peer
	// Done is closed when the connection ends.
	Done() <-chan struct{}
}

type wsConn struct {
	ws   *websocket.Conn
	peer Peer
	max  int64

	ctx    context.Context // canceled when the connection ends
	cancel context.CancelFunc

	mu    sync.Mutex
	pong  time.Time // last pong, or the start
	cause error     // set when the keepalive gave the connection up
}

var _ Conn = (*wsConn)(nil)

func newWSConn(ws *websocket.Conn, peer Peer, max int64, ka Keepalive, clk clock.Clock) *wsConn {
	if max <= 0 {
		max = MaxMessageSize
	}
	// Read applies the limit itself, so that an oversized message is
	// discarded instead of closing the connection.
	ws.SetReadLimit(-1)
	ctx, cancel := context.WithCancel(context.Background())
	c := &wsConn{ws: ws, peer: peer, max: max, ctx: ctx, cancel: cancel, pong: clk.Now()}
	go c.keepalive(ka.withDefaults(), clk)
	return c
}

func (c *wsConn) Peer() Peer            { return c.peer }
func (c *wsConn) Done() <-chan struct{} { return c.ctx.Done() }
func (c *wsConn) finish()               { c.cancel() }

func (c *wsConn) Read(ctx context.Context) (MessageType, []byte, error) {
	typ, r, err := c.ws.Reader(ctx)
	if err != nil {
		return 0, nil, c.fatal(err)
	}
	buf, err := io.ReadAll(io.LimitReader(r, c.max+1))
	if err != nil {
		return 0, nil, c.fatal(err)
	}
	if int64(len(buf)) <= c.max {
		return MessageType(typ), buf, nil
	}
	// Discard up to maxDiscard in all; one byte more closes with 1009.
	n, err := io.Copy(io.Discard, io.LimitReader(r, maxDiscard-int64(len(buf))+1))
	if err != nil {
		return 0, nil, c.fatal(err)
	}
	if total := int64(len(buf)) + n; total > maxDiscard {
		_ = c.ws.Close(websocket.StatusMessageTooBig, "message too large")
		return 0, nil, c.fatal(fmt.Errorf("%w: over %d bytes", ErrTooLarge, int64(maxDiscard)))
	}
	return 0, nil, fmt.Errorf("%w: %d bytes, limit %d", ErrTooLarge, int64(len(buf))+n, c.max)
}

func (c *wsConn) Write(ctx context.Context, typ MessageType, p []byte) error {
	if int64(len(p)) > c.max {
		return fmt.Errorf("%w: %d bytes, limit %d", ErrTooLarge, len(p), c.max)
	}
	if err := c.ws.Write(ctx, websocket.MessageType(typ), p); err != nil {
		return c.fatal(err)
	}
	return nil
}

func (c *wsConn) Close(code StatusCode, reason string) error {
	defer c.finish()
	if err := c.ws.Close(websocket.StatusCode(code), reason); err != nil {
		return fmt.Errorf("transport: close: %w", err)
	}
	return nil
}

func (c *wsConn) CloseNow() error {
	defer c.finish()
	return c.ws.CloseNow()
}

// fatal ends the connection and translates err: the peer's close becomes a
// *CloseError, and a connection the keepalive gave up returns ErrNoPong.
func (c *wsConn) fatal(err error) error {
	c.finish()
	c.mu.Lock()
	cause := c.cause
	c.mu.Unlock()
	if cause != nil {
		return fmt.Errorf("transport: %w", cause)
	}
	var ce websocket.CloseError
	if errors.As(err, &ce) {
		return &CloseError{Code: StatusCode(ce.Code), Reason: ce.Reason}
	}
	return fmt.Errorf("transport: %w", err)
}

// keepalive pings every Interval and drops the connection, with ErrNoPong
// as the cause, at the first tick that finds no pong for Timeout. Counting
// from the last pong, and not per ping, also covers a ping that cannot even
// be written because a long write holds the connection.
func (c *wsConn) keepalive(ka Keepalive, clk clock.Clock) {
	t := clk.NewTicker(ka.Interval)
	defer t.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-t.C():
			if clock.Since(clk, c.lastPong()) >= ka.Timeout {
				c.mu.Lock()
				c.cause = ErrNoPong
				c.mu.Unlock()
				_ = c.ws.CloseNow()
				c.finish()
				return
			}
			go c.ping(ka.Timeout, clk)
		}
	}
}

func (c *wsConn) lastPong() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pong
}

func (c *wsConn) ping(timeout time.Duration, clk clock.Clock) {
	ctx, cancel := clock.WithTimeout(c.ctx, clk, timeout)
	defer cancel()
	err := c.ws.Ping(ctx)
	switch {
	case err == nil:
		c.mu.Lock()
		c.pong = clk.Now()
		c.mu.Unlock()
	case errors.Is(err, net.ErrClosed):
		c.finish()
	}
	// Any other failure (no pong yet, write lock busy) is judged by the
	// next tick, from the last pong.
}
