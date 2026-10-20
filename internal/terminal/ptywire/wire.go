package ptywire

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// Protocol versions. The protocol is additive only: fields and messages may
// be added, never removed or changed in meaning, and unknown fields are
// ignored. A new engine therefore talks to any older mestre-pty, and a new
// feature only works in terminals created by a mestre-pty that announces
// the version that brought it.
const (
	// ProtoVersion is the version this code speaks.
	ProtoVersion = 4
	// MinProtoVersion is the oldest mestre-pty the engine still serves.
	MinProtoVersion = 1
	// ProtoPing is the version that added the "ping" message and the
	// "lines" field of "screen" (history on request).
	ProtoPing = 2
	// ProtoInJob is the version that added the "injob" message: whether a
	// process is in the Job Object of the terminal (Windows), for the
	// caller identification of the local channel.
	ProtoInJob = 3
	// ProtoHook is the version that added the "hook" message: the
	// asynchronous hook of a class, run as the agent.
	ProtoHook = 4
)

// Limits of the hook message.
const (
	// MaxHookOutput is the most output of a hook that comes back.
	MaxHookOutput = 64 << 10
	// MinHookTimeout and MaxHookTimeout bound the time limit of a hook.
	MinHookTimeout = time.Second
	MaxHookTimeout = time.Hour
)

// Terminal defaults (docs/arquitetura/terminal.md#criação).
const (
	DefaultCols = 200
	DefaultRows = 50
	// HandshakeTimeout is how long a connection may take to prove the secret.
	HandshakeTimeout = 30 * time.Second
	// EngineHandshakeTimeout is how long the engine waits for the mestre-pty to
	// finish the handshake, counted from the connect: longer than the mestre-pty
	// waits (counted from its accept), so the side that cuts is the one that knows why.
	EngineHandshakeTimeout = HandshakeTimeout + 5*time.Second
	// SecretSize is the size of the per-terminal secret in bytes.
	SecretSize = 32
)

// Limits of the wire format.
const (
	MaxLine  = 4 << 20 // one JSON line
	MaxFrame = 1 << 20 // the data of one binary frame
	// MaxReplay is the most bytes one replay answers; the caller asks again
	// from Offset+Len for the rest.
	MaxReplay = 4 << 20
)

// Message types (the "t" field).
const (
	TypeHello    = "hello"
	TypeAuth     = "auth"
	TypeReady    = "ready"
	TypeStart    = "start"
	TypeStarted  = "started"
	TypeStatus   = "status"
	TypeScreen   = "screen"
	TypeReplay   = "replay"
	TypeFollow   = "follow"
	TypeUnfollow = "unfollow"
	TypeInput    = "input"
	TypeAck      = "ack"
	TypeStop     = "stop"
	TypePing     = "ping"
	TypeInJob    = "injob"
	TypeHook     = "hook"
	TypeOK       = "ok"
	TypeError    = "error"
	// Events, sent without a request.
	TypeStable = "stable"
	TypeExit   = "exit"
)

// Stop modes.
const (
	// StopSignal asks the tree to end: SIGTERM to the session on Linux,
	// ClosePseudoConsole on Windows.
	StopSignal = "signal"
	// StopKill ends the whole tree and the mestre-pty.
	StopKill = "kill"
)

// States of the terminal in "status".
const (
	StateIdle    = "idle"
	StateRunning = "running"
	StateExited  = "exited"
)

// Error codes of "error" replies.
const (
	CodeBadRequest     = "bad_request"
	CodeUnknownMessage = "unknown_message"
	CodeAlreadyStarted = "already_started"
	CodeNotRunning     = "not_running"
	CodeStartFailed    = "start_failed"
	// CodeNotSupported: the message is known but this terminal cannot
	// answer it (e.g. "injob" on Linux, where the cgroup groups the tree).
	CodeNotSupported = "not_supported"
	// CodeBusy: a hook is already running in this terminal.
	CodeBusy = "busy"
)

// Msg is one control message. A single flat struct keeps the protocol
// additive: a field is added here and older peers ignore it.
type Msg struct {
	T  string `json:"t"`
	ID uint64 `json:"id,omitempty"`

	// error replies
	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`

	// hello, auth, ready
	Proto   int    `json:"proto,omitempty"`
	Nonce   string `json:"nonce,omitempty"`
	Proof   string `json:"proof,omitempty"`
	Version string `json:"version,omitempty"`
	PID     int    `json:"pid,omitempty"`

	// start (the engine decides everything about the process)
	Path string   `json:"path,omitempty"`
	Args []string `json:"args,omitempty"`
	Env  []string `json:"env,omitempty"`
	Dir  string   `json:"dir,omitempty"`
	Cols int      `json:"cols,omitempty"`
	Rows int      `json:"rows,omitempty"`

	// started, status, exit
	State    string `json:"state,omitempty"`
	AgentPID int    `json:"agent_pid,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Signal   string `json:"signal,omitempty"`
	Stable   bool   `json:"stable,omitempty"`
	Acked    uint64 `json:"acked,omitempty"`
	// BufferStart is the offset of the oldest byte still kept.
	BufferStart uint64 `json:"buffer_start,omitempty"`
	End         uint64 `json:"end,omitempty"`

	// screen
	Lines   int    `json:"lines,omitempty"`
	Text    string `json:"text,omitempty"`
	History string `json:"history,omitempty"`
	CurCol  int    `json:"cursor_col,omitempty"`
	CurRow  int    `json:"cursor_row,omitempty"`

	// replay, follow, ack, input, stable
	From   uint64 `json:"from,omitempty"`
	Offset uint64 `json:"offset,omitempty"`
	Lost   uint64 `json:"lost,omitempty"`
	Len    int    `json:"len,omitempty"`
	Data   []byte `json:"data,omitempty"`

	// stop
	Mode string `json:"mode,omitempty"`

	// injob: PID is the process asked about and Start its start identity
	// (on Windows the creation time, as System.Identify returns it); the
	// reply carries InJob. A pointer, so that false is not omitted.
	Start string `json:"start,omitempty"`
	InJob *bool  `json:"in_job,omitempty"`

	// hook: Run is the command line, Dir the folder it runs in (the one of the
	// start or inside it), Event the Mestre event (MESTRE_HOOK_EVENT), Stdin
	// the event and TimeoutMS the time limit. The reply carries ExitCode, the
	// Output (stdout and stderr, at most MaxHookOutput bytes), TimedOut and
	// Truncated.
	Run       string `json:"run,omitempty"`
	Event     string `json:"event,omitempty"`
	Stdin     []byte `json:"stdin,omitempty"`
	TimeoutMS int64  `json:"timeout_ms,omitempty"`
	Output    string `json:"output,omitempty"`
	TimedOut  bool   `json:"timed_out,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// Frame is a run of terminal bytes. Stream is the id of the request it
// answers (the replay or the follow); Offset is the absolute offset of its
// first byte, so a jump between two frames of a stream is a lost chunk.
type Frame struct {
	Stream uint64
	Offset uint64
	Data   []byte
}

// frameMark starts a binary frame; a JSON line always starts with '{'.
const frameMark = 0x01

const frameHeader = 1 + 8 + 8 + 4

// Item is what Conn.Read returns: a message or a frame.
type Item struct {
	Msg   *Msg
	Frame *Frame
}

// ErrProtocol is wrapped by every malformed input.
var ErrProtocol = errors.New("ptywire: protocol error")

// Conn reads and writes the wire format on a stream. Writes are not
// synchronised: each side serialises them itself.
type Conn struct {
	nc net.Conn
	br *bufio.Reader
}

// NewConn wraps nc.
func NewConn(nc net.Conn) *Conn { return &Conn{nc: nc, br: bufio.NewReaderSize(nc, 64<<10)} }

// Net returns the underlying connection.
func (c *Conn) Net() net.Conn { return c.nc }

// Close closes the underlying connection.
func (c *Conn) Close() error { return c.nc.Close() }

// Read returns the next message or frame.
func (c *Conn) Read() (Item, error) {
	b, err := c.br.Peek(1)
	if err != nil {
		return Item{}, err
	}
	switch b[0] {
	case frameMark:
		return c.readFrame()
	case '{':
		return c.readLine()
	}
	return Item{}, fmt.Errorf("%w: unexpected byte 0x%02x", ErrProtocol, b[0])
}

func (c *Conn) readLine() (Item, error) {
	var line []byte
	for {
		part, err := c.br.ReadSlice('\n')
		line = append(line, part...)
		if len(line) > MaxLine {
			return Item{}, fmt.Errorf("%w: line over %d bytes", ErrProtocol, MaxLine)
		}
		if err == nil {
			break
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return Item{}, err
		}
	}
	var m Msg
	if err := json.Unmarshal(bytes.TrimSpace(line), &m); err != nil {
		return Item{}, fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	return Item{Msg: &m}, nil
}

func (c *Conn) readFrame() (Item, error) {
	var h [frameHeader]byte
	if _, err := io.ReadFull(c.br, h[:]); err != nil {
		return Item{}, err
	}
	n := binary.BigEndian.Uint32(h[17:])
	if n > MaxFrame {
		return Item{}, fmt.Errorf("%w: frame of %d bytes", ErrProtocol, n)
	}
	f := &Frame{
		Stream: binary.BigEndian.Uint64(h[1:]),
		Offset: binary.BigEndian.Uint64(h[9:]),
		Data:   make([]byte, n),
	}
	if _, err := io.ReadFull(c.br, f.Data); err != nil {
		return Item{}, err
	}
	return Item{Frame: f}, nil
}

// WriteMsg writes m as one JSON line.
func (c *Conn) WriteMsg(m *Msg) error {
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("encode %s: %w", m.T, err)
	}
	_, err = c.nc.Write(append(b, '\n'))
	return err
}

// WriteFrame writes f; it must not hold more than MaxFrame bytes.
func (c *Conn) WriteFrame(f *Frame) error {
	if len(f.Data) > MaxFrame {
		return fmt.Errorf("%w: frame of %d bytes", ErrProtocol, len(f.Data))
	}
	b := make([]byte, frameHeader+len(f.Data))
	b[0] = frameMark
	binary.BigEndian.PutUint64(b[1:], f.Stream)
	binary.BigEndian.PutUint64(b[9:], f.Offset)
	binary.BigEndian.PutUint32(b[17:], uint32(len(f.Data)))
	copy(b[frameHeader:], f.Data)
	_, err := c.nc.Write(b)
	return err
}
