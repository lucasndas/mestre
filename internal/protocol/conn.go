package protocol

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
)

// Role is who is on the other end of a connection, as proven by its mTLS
// certificate (or its session cookie, for the browser).
type Role uint8

// The roles a connection can have.
const (
	RoleMachine  Role = iota + 1 // the motor of a machine
	RoleGuardian                 // the Guardian of a machine
	RoleBrowser                  // the panel
)

func (r Role) String() string {
	switch r {
	case RoleMachine:
		return "machine"
	case RoleGuardian:
		return "guardian"
	case RoleBrowser:
		return "browser"
	}
	return fmt.Sprintf("role(%d)", uint8(r))
}

// Peer is the verified identity of the other end.
type Peer struct {
	Role Role
	// ID is the machine id (motor or Guardian) taken from the certificate;
	// for the browser, the id of the session.
	ID string
}

// FrameKind tells the two classes of frame apart: control (JSON in a text
// frame) and data (binary).
type FrameKind uint8

// The two classes of frame.
const (
	FrameText FrameKind = iota + 1
	FrameBinary
)

// Frame is one message of the connection.
type Frame struct {
	Kind FrameKind
	Data []byte
}

// Conn is what the protocol needs from a connection: a message-oriented,
// ordered, reliable channel (a WebSocket in production, adapted from
// internal/transport). The implementation must accept messages of at least
// MaxMessage bytes in both classes (a binary frame is DataHeaderSize plus at
// most MaxDataPayload, which fits in MaxMessage).
//
// The transport owns the keepalive of the connection (the ping every 15 s
// and the drop after 45 s without a pong): a dead connection makes Read and Write fail. A Conn that does not do that itself may implement Pinger
// and the session pings for it.
//
// Read is called from one goroutine at a time; Write from one goroutine at a
// time; Close and Peer may be called at any time.
type Conn interface {
	// Write sends one message, or fails when ctx is done or the connection is
	// gone.
	Write(ctx context.Context, kind FrameKind, data []byte) error
	// Read returns the next message. When the peer closed the connection with
	// a code it returns a *CloseError; a message the transport discarded for
	// its size comes as an error that wraps ErrFrameTooLarge and the
	// connection stays open.
	Read(ctx context.Context) (FrameKind, []byte, error)
	// Close sends a close frame with the code and the reason (at most 123
	// bytes) and drops the connection. It is idempotent.
	Close(code int, reason string) error
	// Peer is the identity proven by the certificate of the other end.
	Peer() Peer
}

// Pinger is implemented by a Conn whose transport does not keep the
// connection alive by itself: Ping sends a ping and waits for its pong.
type Pinger interface {
	Ping(ctx context.Context) error
}

// ErrFrameTooLarge is wrapped by the error of a Read that discarded an
// oversized message.
var ErrFrameTooLarge = errors.New("protocol: message over the size limit")

// Dialer opens a connection to the current Citadel: it knows the address
// (descoberta) and the credentials. It is what internal/transport provides.
// The context bounds only the establishment (TCP, TLS, the upgrade); it ends
// when Dial returns, and the connection outlives it.
type Dialer interface {
	Dial(ctx context.Context) (Conn, error)
}

// DialerFunc adapts a function to Dialer.
type DialerFunc func(ctx context.Context) (Conn, error)

// Dial calls f.
func (f DialerFunc) Dial(ctx context.Context) (Conn, error) { return f(ctx) }

// Listener accepts connections whose peer was already authenticated.
type Listener interface {
	Accept(ctx context.Context) (Conn, error)
	Close() error
}

// DataType is the first byte of a binary frame.
type DataType byte

// The binary frames (docs/conectividade/protocolo.md#multiplexação-e-prioridade).
const (
	DataTerminal DataType = 0x01 // terminal output: ID of the terminal, Pos is the byte offset
	DataXfer     DataType = 0x02 // part of a transfer: ID of the transfer, Pos is the index of the part
	DataReplica  DataType = 0x03 // change records: ID is the guild_id, Pos is the lsn of the first record
)

// DataHeaderSize is [1 byte type][16 bytes id][8 bytes position].
const DataHeaderSize = 1 + 16 + 8

// DataFrame is a decoded binary frame.
type DataFrame struct {
	Type DataType
	ID   ULID
	Pos  uint64
	Data []byte
}

// EncodeData writes the binary frame: type, id, position (big-endian), data.
func EncodeData(f DataFrame) ([]byte, error) {
	if len(f.Data) > MaxDataPayload {
		return nil, NewError(CodeTooLarge, "data frame of %d bytes, the limit is %d", len(f.Data), MaxDataPayload)
	}
	b := make([]byte, DataHeaderSize+len(f.Data))
	b[0] = byte(f.Type)
	copy(b[1:17], f.ID[:])
	binary.BigEndian.PutUint64(b[17:25], f.Pos)
	copy(b[DataHeaderSize:], f.Data)
	return b, nil
}

// DecodeData reads a binary frame. Data aliases b.
func DecodeData(b []byte) (DataFrame, error) {
	if len(b) < DataHeaderSize {
		return DataFrame{}, fmt.Errorf("protocol: binary frame of %d bytes is shorter than its header", len(b))
	}
	if len(b)-DataHeaderSize > MaxDataPayload {
		return DataFrame{}, NewError(CodeTooLarge, "data frame of %d bytes, the limit is %d", len(b)-DataHeaderSize, MaxDataPayload)
	}
	f := DataFrame{Type: DataType(b[0]), Pos: binary.BigEndian.Uint64(b[17:25]), Data: b[DataHeaderSize:]}
	copy(f.ID[:], b[1:17])
	return f, nil
}
