package protocol

import (
	"context"
	"errors"
	"fmt"

	"github.com/mestre-ia/mestre/internal/transport"
)

// The adapter between internal/transport (WSS with mTLS) and the Conn,
// Dialer and Listener of the protocol. It only converts types: the
// keepalive, the size limit and the identity are the transport's.

type transportConn struct{ c transport.Conn }

// FromTransport adapts a connection of the transport. Read reports the close
// of the peer as a *CloseError and a message that the transport discarded
// for its size as ErrFrameTooLarge. The transport pings by itself, so the
// result is not a Pinger.
func FromTransport(c transport.Conn) Conn { return transportConn{c} }

func (t transportConn) Write(ctx context.Context, kind FrameKind, data []byte) error {
	typ := transport.MessageText
	if kind == FrameBinary {
		typ = transport.MessageBinary
	}
	return t.c.Write(ctx, typ, data)
}

func (t transportConn) Read(ctx context.Context) (FrameKind, []byte, error) {
	typ, data, err := t.c.Read(ctx)
	if err != nil {
		var ce *transport.CloseError
		switch {
		case errors.As(err, &ce):
			return 0, nil, &CloseError{Code: int(ce.Code), Reason: ce.Reason}
		case errors.Is(err, transport.ErrTooLarge):
			return 0, nil, fmt.Errorf("%w: %w", ErrFrameTooLarge, err)
		}
		return 0, nil, err
	}
	if typ == transport.MessageBinary {
		return FrameBinary, data, nil
	}
	return FrameText, data, nil
}

func (t transportConn) Close(code int, reason string) error {
	return t.c.Close(transport.StatusCode(code), reason)
}

func (t transportConn) Peer() Peer {
	p := t.c.Peer()
	role := RoleMachine
	if p.Kind == transport.KindGuardian {
		role = RoleGuardian
	}
	return Peer{Role: role, ID: p.MachineID}
}

// TransportDialer adapts a transport Dialer bound to the address and the
// route of the Citadel (found by the discovery) to the Dialer of the protocol.
// A Tower that rediscovers the Guild changes the address through the function.
func TransportDialer(d transport.Dialer, target func() (addr, route string)) Dialer {
	return DialerFunc(func(ctx context.Context) (Conn, error) {
		addr, route := target()
		c, err := d.Dial(ctx, addr, route)
		if err != nil {
			return nil, err
		}
		return FromTransport(c), nil
	})
}

type transportListener struct{ l transport.Listener }

// FromTransportListener adapts the listener of a route.
func FromTransportListener(l transport.Listener) Listener { return transportListener{l} }

func (t transportListener) Accept(ctx context.Context) (Conn, error) {
	c, err := t.l.Accept(ctx)
	if err != nil {
		return nil, err
	}
	return FromTransport(c), nil
}

func (t transportListener) Close() error { return t.l.Close() }
