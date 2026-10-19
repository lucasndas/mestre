// Package protorig is the double of the connection of the protocol for the
// fast suite (docs/qualidade/dubles.md): a protocol.Conn over the in-memory
// network (internal/testkit/fakenet) with the same shape as the WSS adapter
// of internal/transport (text and binary frames, ping and pong, close with a
// code, the identity of the peer), plus in-memory versions of the durable
// halves of the protocol and a fake epoch, so a test plays a Tower and a
// Citadel without a socket.
//
// Like the WebSocket under the transport, the control frames (a ping, the
// pong that answers ours) are seen only while the connection is read: a
// frame that nobody takes holds the ones behind it, so a reader that stops
// stops the pongs too.
package protorig

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
)

// Wire kinds of the framing over the fake byte stream.
const (
	kText     byte = 'T'
	kBinary   byte = 'B'
	kPing     byte = 'P'
	kPong     byte = 'O'
	kClose    byte = 'C'
	kIdentity byte = 'I'
)

// maxFrame bounds a frame read from the stream, like the read limit of a
// WebSocket.
const maxFrame = protocol.MaxMessage + protocol.DataHeaderSize + protocol.MaxDataPayload

// ErrClosed is what Send says after the connection was closed locally.
var ErrClosed = errors.New("protorig: connection closed")

// Conn is a protocol.Conn over a net.Conn of the fake network.
type Conn struct {
	nc   net.Conn
	peer protocol.Peer

	wmu    sync.Mutex
	frames chan protocol.Frame
	pong   chan struct{}
	dead   chan struct{} // closed when the reader ended
	closed chan struct{} // closed by a local Close
	once   sync.Once
	err    error // why the reader ended; read after dead is closed
}

var (
	_ protocol.Conn   = (*Conn)(nil)
	_ protocol.Pinger = (*Conn)(nil)
)

// NewConn runs the framing over nc: it announces self and reads who the other
// end is, as the TLS handshake does with the certificates.
func NewConn(ctx context.Context, nc net.Conn, self protocol.Peer) (*Conn, error) {
	c := &Conn{
		nc: nc, frames: make(chan protocol.Frame), pong: make(chan struct{}, 1),
		dead: make(chan struct{}), closed: make(chan struct{}),
	}
	// the exchange of identities is part of the establishment: ctx bounds it
	stop := context.AfterFunc(ctx, func() { _ = nc.Close() })
	defer stop()
	id := append([]byte{byte(self.Role)}, self.ID...)
	if err := c.write(kIdentity, id); err != nil {
		return nil, err
	}
	kind, payload, err := readFrame(nc)
	if err != nil || kind != kIdentity || len(payload) < 1 {
		_ = nc.Close()
		return nil, fmt.Errorf("protorig: no identity from the other end: %v", err)
	}
	c.peer = protocol.Peer{Role: protocol.Role(payload[0]), ID: string(payload[1:])}
	go c.readLoop()
	return c, nil
}

func readFrame(r io.Reader) (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > maxFrame {
		return 0, nil, fmt.Errorf("protorig: frame of %d bytes is over the read limit", n)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return hdr[0], payload, nil
}

func (c *Conn) write(kind byte, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	buf := make([]byte, 5+len(payload))
	buf[0] = kind
	binary.BigEndian.PutUint32(buf[1:], uint32(len(payload)))
	copy(buf[5:], payload)
	_, err := c.nc.Write(buf)
	return err
}

func (c *Conn) readLoop() {
	defer close(c.dead)
	for {
		kind, payload, err := readFrame(c.nc)
		if err != nil {
			c.err = err
			return
		}
		switch kind {
		case kText, kBinary:
			f := protocol.Frame{Kind: protocol.FrameText, Data: payload}
			if kind == kBinary {
				f.Kind = protocol.FrameBinary
			}
			select {
			case c.frames <- f:
			case <-c.closed:
				c.err = ErrClosed
				return
			}
		case kPing:
			_ = c.write(kPong, nil)
		case kPong:
			select {
			case c.pong <- struct{}{}:
			default:
			}
		case kClose:
			ce := &protocol.CloseError{}
			if len(payload) >= 2 {
				ce.Code, ce.Reason = int(binary.BigEndian.Uint16(payload)), string(payload[2:])
			}
			c.err = ce
			return
		}
	}
}

// Write implements protocol.Conn.
func (c *Conn) Write(ctx context.Context, kind protocol.FrameKind, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-c.closed:
		return ErrClosed
	default:
	}
	k := kText
	if kind == protocol.FrameBinary {
		k = kBinary
	}
	return c.write(k, data)
}

// Read implements protocol.Conn.
func (c *Conn) Read(ctx context.Context) (protocol.FrameKind, []byte, error) {
	select {
	case f := <-c.frames:
		return f.Kind, f.Data, nil
	case <-c.dead:
		select {
		case f := <-c.frames:
			return f.Kind, f.Data, nil
		default:
			return 0, nil, c.err
		}
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	}
}

// Ping implements protocol.Pinger: the fake network has no transport that
// keeps the connection alive, so the protocol does it.
func (c *Conn) Ping(ctx context.Context) error {
	select {
	case <-c.pong:
	default:
	}
	if err := c.write(kPing, nil); err != nil {
		return err
	}
	select {
	case <-c.pong:
		return nil
	case <-c.dead:
		return c.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close implements protocol.Conn.
func (c *Conn) Close(code int, reason string) error {
	c.once.Do(func() {
		payload := binary.BigEndian.AppendUint16(nil, uint16(code))
		_ = c.write(kClose, append(payload, reason...))
		close(c.closed)
		_ = c.nc.Close()
	})
	return nil
}

// Peer implements protocol.Conn.
func (c *Conn) Peer() protocol.Peer { return c.peer }

// Network is the fake network seen by the protocol.
type Network struct{ Net *fakenet.Network }

// Dialer dials addr from the host `from` of the fake network, presenting the
// identity self.
func (n Network) Dialer(from, addr string, self protocol.Peer) protocol.Dialer {
	return protocol.DialerFunc(func(ctx context.Context) (protocol.Conn, error) {
		nc, err := n.Net.Dial(ctx, from, addr)
		if err != nil {
			return nil, err
		}
		return NewConn(ctx, nc, self)
	})
}

// Listen listens on addr with the identity self.
func (n Network) Listen(addr string, self protocol.Peer) (protocol.Listener, error) {
	l, err := n.Net.Listen(addr)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	pl := &listener{l: l, self: self, conns: make(chan protocol.Conn), done: make(chan struct{}), ctx: ctx, cancel: cancel}
	go pl.loop()
	return pl, nil
}

type listener struct {
	l     net.Listener
	self  protocol.Peer
	conns chan protocol.Conn
	done  chan struct{}
	once  sync.Once
	// ctx ends the exchange of identities of the connections still opening
	ctx    context.Context
	cancel context.CancelFunc
}

func (l *listener) loop() {
	for {
		nc, err := l.l.Accept()
		if err != nil {
			return
		}
		go func() {
			c, err := NewConn(l.ctx, nc, l.self)
			if err != nil {
				return
			}
			select {
			case l.conns <- c:
			case <-l.done:
				_ = c.Close(1001, "")
			}
		}()
	}
}

func (l *listener) Accept(ctx context.Context) (protocol.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (l *listener) Close() error {
	l.once.Do(func() {
		close(l.done)
		l.cancel()
		_ = l.l.Close()
	})
	return nil
}
