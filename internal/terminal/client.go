package terminal

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/terminal/ptywire"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Errors of the client.
var (
	// ErrUnsupported means the mestre-pty of this terminal does not have
	// the message: it was created by a version that predates it.
	ErrUnsupported = errors.New("mestre-pty does not support this message")
	// ErrClosed means the connection to the mestre-pty ended.
	ErrClosed = errors.New("connection to the mestre-pty closed")
)

// RemoteError is an "error" reply of the mestre-pty.
type RemoteError struct {
	Code string
	Text string
}

func (e *RemoteError) Error() string { return fmt.Sprintf("mestre-pty: %s: %s", e.Code, e.Text) }

// Exit is how the agent ended.
type Exit struct {
	Code int
	// NoCode: the mestre-pty announced the exit with no code. Code is then 0
	// only because it is the zero value, and a reader that decides on success
	// must treat it as a failure.
	NoCode bool
	Signal string
	End    uint64 // offset after the last byte of output
}

// Replayed is the answer to a replay: the bytes from Offset on, and how many
// bytes before Offset were lost (the "lost chunk" mark).
type Replayed struct {
	Offset uint64
	Lost   uint64
	Data   []byte
	End    uint64 // end of the output when the mestre-pty answered
}

// Client is the engine's connection to one mestre-pty.
type Client struct {
	conn *ptywire.Conn
	info ptywire.Info

	writeMu sync.Mutex

	mu      sync.Mutex
	nextID  uint64
	calls   map[uint64]*call
	follows map[uint64]func(ptywire.Frame)
	err     error
	exit    *Exit
	onEvent func(ptywire.Msg)

	done     chan struct{}
	doneOnce sync.Once // fail runs from the reader and from a failed write
	exited   chan struct{}
}

type call struct {
	reply chan reply
	typ   string // the type of the request
	// replay assembly
	want int
	head *ptywire.Msg
	data []byte
}

type reply struct {
	msg  *ptywire.Msg
	data []byte
	err  error
}

// NewClient runs the handshake on nc as the engine and returns the
// client; on failure it closes nc. A handshake that takes longer than
// timeout (the default 5 s) fails, measured by clk.
func NewClient(nc net.Conn, secret []byte, clk clock.Clock, timeout time.Duration) (*Client, error) {
	if timeout == 0 {
		timeout = ptywire.HandshakeTimeout
	}
	conn := ptywire.NewConn(nc)
	timer := clk.AfterFunc(timeout, func() { _ = nc.Close() })
	info, err := ptywire.ClientHandshake(conn, secret)
	timer.Stop()
	if err != nil {
		_ = nc.Close()
		return nil, err
	}
	if info.Proto < ptywire.MinProtoVersion {
		_ = nc.Close()
		return nil, fmt.Errorf("mestre-pty speaks protocol %d, the oldest supported is %d", info.Proto, ptywire.MinProtoVersion)
	}
	c := &Client{
		conn: conn, info: info,
		calls:   map[uint64]*call{},
		follows: map[uint64]func(ptywire.Frame){},
		done:    make(chan struct{}), exited: make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

// Dial connects to the socket at path and runs the handshake. While the
// socket does not exist yet or nobody accepts (the mestre-pty is still
// starting), it tries again until timeout; a failed handshake is final.
func Dial(ctx context.Context, clk clock.Clock, path string, secret []byte, timeout time.Duration) (*Client, error) {
	if timeout == 0 {
		timeout = ptywire.HandshakeTimeout
	}
	ctx, cancel := clock.WithTimeout(ctx, clk, timeout)
	defer cancel()
	var d net.Dialer
	for {
		nc, err := d.DialContext(ctx, "unix", path)
		if err == nil {
			return NewClient(nc, secret, clk, timeout)
		}
		if serr := clock.Sleep(ctx, clk, 20*time.Millisecond); serr != nil {
			return nil, fmt.Errorf("connect to %s: %w", path, err)
		}
	}
}

// SetEventHandler sets the function called, from the reading goroutine,
// for each event ("stable", "exit"); it must not block. Events are hints:
// "status" is the truth after a reconnection.
func (c *Client) SetEventHandler(f func(ptywire.Msg)) {
	c.mu.Lock()
	c.onEvent = f
	c.mu.Unlock()
}

// Info is what the mestre-pty said about itself after the handshake.
func (c *Client) Info() ptywire.Info { return c.info }

// Net returns the underlying connection (the peer PID check reads it).
func (c *Client) Net() net.Conn { return c.conn.Net() }

// Done is closed when the connection ended.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err is why the connection ended, once Done is closed.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Exited is closed when the agent's exit event arrived.
func (c *Client) Exited() <-chan struct{} { return c.exited }

// Exit returns how the agent ended, once Exited is closed.
func (c *Client) Exit() Exit {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.exit == nil {
		return Exit{}
	}
	return *c.exit
}

// Close ends the connection; the terminal keeps running.
func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) readLoop() {
	for {
		it, err := c.conn.Read()
		if err != nil {
			c.fail(err)
			return
		}
		select {
		case <-c.done:
			// the connection failed on the item before (a violation of the
			// protocol): what is left in the buffer cannot be trusted either
			return
		default:
		}
		if it.Frame != nil {
			c.onFrame(it.Frame)
		} else {
			c.onMsg(it.Msg)
		}
	}
}

func (c *Client) fail(err error) {
	c.mu.Lock()
	if c.err == nil {
		c.err = fmt.Errorf("%w: %v", ErrClosed, err)
	}
	calls := c.calls
	c.calls = map[uint64]*call{}
	failure := c.err
	c.mu.Unlock()
	_ = c.conn.Close()
	for _, cl := range calls {
		cl.reply <- reply{err: failure}
	}
	c.doneOnce.Do(func() { close(c.done) })
}

func (c *Client) onMsg(m *ptywire.Msg) {
	if m.ID == 0 {
		c.onEventMsg(m)
		return
	}
	c.mu.Lock()
	cl := c.calls[m.ID]
	if cl == nil {
		c.mu.Unlock()
		return
	}
	if m.T == ptywire.TypeReplay && m.Len > 0 {
		// What comes from the mestre-pty may be controlled by the agent of the
		// terminal: a header of a replay answers a replay, once, and no
		// allocation is made of a length it chose. The frames that would follow
		// a violation cannot be trusted either. The call stays in c.calls, for fail.
		var violation error
		switch {
		case cl.typ != ptywire.TypeReplay:
			violation = fmt.Errorf("the mestre-pty answered a %q with the header of a replay", cl.typ)
		case cl.head != nil:
			violation = errors.New("the mestre-pty sent the header of a replay twice")
		case m.Len > ptywire.MaxReplay:
			violation = fmt.Errorf("the mestre-pty announced a replay of %d bytes, the most is %d", m.Len, ptywire.MaxReplay)
		}
		if violation != nil {
			c.mu.Unlock()
			c.fail(violation)
			return
		}
		cl.want, cl.head, cl.data = m.Len, m, make([]byte, 0, m.Len)
		c.mu.Unlock()
		return
	}
	delete(c.calls, m.ID)
	c.mu.Unlock()
	cl.reply <- reply{msg: m}
}

func (c *Client) onEventMsg(m *ptywire.Msg) {
	c.mu.Lock()
	h := c.onEvent
	if m.T == ptywire.TypeExit && c.exit == nil {
		code := 0
		if m.ExitCode != nil {
			code = *m.ExitCode
		}
		c.exit = &Exit{Code: code, NoCode: m.ExitCode == nil, Signal: m.Signal, End: m.End}
		close(c.exited)
	}
	c.mu.Unlock()
	if h != nil {
		h(*m)
	}
}

func (c *Client) onFrame(f *ptywire.Frame) {
	c.mu.Lock()
	if h := c.follows[f.Stream]; h != nil {
		c.mu.Unlock()
		h(*f)
		return
	}
	cl := c.calls[f.Stream]
	if cl == nil || cl.head == nil {
		c.mu.Unlock()
		return
	}
	// The frames of a replay are the bytes the header announced, in order, from
	// the offset it said: a mestre-pty sends exactly that, and one that sends
	// more or elsewhere is broken or taken over.
	if f.Offset != cl.head.Offset+uint64(len(cl.data)) || len(cl.data)+len(f.Data) > cl.want {
		got := len(cl.data)
		c.mu.Unlock()
		c.fail(fmt.Errorf("a frame of the replay has %d bytes at offset %d, after %d of the %d announced from offset %d",
			len(f.Data), f.Offset, got, cl.want, cl.head.Offset))
		return
	}
	cl.data = append(cl.data, f.Data...)
	if len(cl.data) < cl.want {
		c.mu.Unlock()
		return
	}
	delete(c.calls, f.Stream)
	c.mu.Unlock()
	cl.reply <- reply{msg: cl.head, data: cl.data}
}

// do sends req and waits for its reply.
func (c *Client) do(ctx context.Context, req ptywire.Msg) (reply, error) {
	cl := &call{reply: make(chan reply, 1), typ: req.T}
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return reply{}, err
	}
	c.nextID++
	req.ID = c.nextID
	c.calls[req.ID] = cl
	c.mu.Unlock()

	c.writeMu.Lock()
	err := c.conn.WriteMsg(&req)
	c.writeMu.Unlock()
	if err != nil {
		c.fail(err)
		return reply{}, c.Err()
	}
	select {
	case r := <-cl.reply:
		if r.err != nil {
			return reply{}, r.err
		}
		if r.msg.T == ptywire.TypeError {
			if r.msg.Code == ptywire.CodeUnknownMessage || r.msg.Code == ptywire.CodeNotSupported {
				return reply{}, fmt.Errorf("%w: %s", ErrUnsupported, req.T)
			}
			return reply{}, &RemoteError{Code: r.msg.Code, Text: r.msg.Error}
		}
		return r, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.calls, req.ID)
		c.mu.Unlock()
		return reply{}, ctx.Err()
	}
}

// Start starts the agent and returns its PID.
func (c *Client) Start(ctx context.Context, spec ptywire.Msg) (int, error) {
	spec.T = ptywire.TypeStart
	r, err := c.do(ctx, spec)
	if err != nil {
		return 0, err
	}
	return r.msg.AgentPID, nil
}

// Status returns the "status" message: state, agent PID, exit, offsets.
func (c *Client) Status(ctx context.Context) (ptywire.Msg, error) {
	r, err := c.do(ctx, ptywire.Msg{T: ptywire.TypeStatus})
	if err != nil {
		return ptywire.Msg{}, err
	}
	return *r.msg, nil
}

// Screen returns the screen as text, with lines > 0 also that many lines of
// history (only a mestre-pty of protocol 2 or newer sends it).
func (c *Client) Screen(ctx context.Context, lines int) (ptywire.Msg, error) {
	r, err := c.do(ctx, ptywire.Msg{T: ptywire.TypeScreen, Lines: lines})
	if err != nil {
		return ptywire.Msg{}, err
	}
	return *r.msg, nil
}

// Replay is terminal.replay: the output from offset from on, with the
// "lost chunk" mark when the start is no longer in the buffer. It answers at
// most ptywire.MaxReplay bytes; ask again from Offset+len(Data).
func (c *Client) Replay(ctx context.Context, from uint64) (Replayed, error) {
	r, err := c.do(ctx, ptywire.Msg{T: ptywire.TypeReplay, From: from})
	if err != nil {
		return Replayed{}, err
	}
	return Replayed{Offset: r.msg.Offset, Lost: r.msg.Lost, Data: r.data, End: r.msg.End}, nil
}

// FollowStart says where a live stream begins.
type FollowStart struct {
	Offset uint64
	Lost   uint64
}

// Follow starts the live stream of the output from offset from on; h is
// called from the reading goroutine, in order, for each frame. A jump
// between the end of one frame and the offset of the next is a lost chunk.
// One stream per connection: a new Follow replaces the old one.
func (c *Client) Follow(ctx context.Context, from uint64, h func(ptywire.Frame)) (FollowStart, error) {
	c.mu.Lock()
	next := c.nextID + 1
	c.follows = map[uint64]func(ptywire.Frame){next: h}
	c.mu.Unlock()
	r, err := c.do(ctx, ptywire.Msg{T: ptywire.TypeFollow, From: from})
	if err != nil {
		return FollowStart{}, err
	}
	return FollowStart{Offset: r.msg.Offset, Lost: r.msg.Lost}, nil
}

// Input types data into the terminal.
func (c *Client) Input(ctx context.Context, data []byte) error {
	_, err := c.do(ctx, ptywire.Msg{T: ptywire.TypeInput, Data: data})
	return err
}

// Ack tells the mestre-pty the log holds every byte before offset.
func (c *Client) Ack(ctx context.Context, offset uint64) error {
	_, err := c.do(ctx, ptywire.Msg{T: ptywire.TypeAck, Offset: offset})
	return err
}

// Stop asks the mestre-pty to end the tree (ptywire.StopSignal) or to kill it
// and exit (ptywire.StopKill).
func (c *Client) Stop(ctx context.Context, mode string) error {
	_, err := c.do(ctx, ptywire.Msg{T: ptywire.TypeStop, Mode: mode})
	return err
}

// Ping checks the mestre-pty answers; it returns the end of the output. It
// is ErrUnsupported for a mestre-pty older than protocol 2.
func (c *Client) Ping(ctx context.Context) (uint64, error) {
	if c.info.Proto < ptywire.ProtoPing {
		return 0, fmt.Errorf("%w: ping needs protocol %d, this one speaks %d", ErrUnsupported, ptywire.ProtoPing, c.info.Proto)
	}
	r, err := c.do(ctx, ptywire.Msg{T: ptywire.TypePing})
	if err != nil {
		return 0, err
	}
	return r.msg.End, nil
}

// InJob asks whether the process pid, whose start identity is start (as
// System.Identify gives it), is in the Job Object of the terminal: how the
// engine identifies who calls the CLI on Windows
// (docs/arquitetura/canal-local.md#identificação-e-versão). It is
// ErrUnsupported for a mestre-pty older than protocol 3 and on Linux, where
// the terminal has no Job Object.
func (c *Client) InJob(ctx context.Context, pid int, start string) (bool, error) {
	if c.info.Proto < ptywire.ProtoInJob {
		return false, fmt.Errorf("%w: injob needs protocol %d, this one speaks %d", ErrUnsupported, ptywire.ProtoInJob, c.info.Proto)
	}
	r, err := c.do(ctx, ptywire.Msg{T: ptywire.TypeInJob, PID: pid, Start: start})
	if err != nil {
		return false, err
	}
	return r.msg.InJob != nil && *r.msg.InJob, nil
}

// HookResult is how a hook of a class ended in the terminal.
type HookResult struct {
	Exit      int
	Output    string
	TimedOut  bool
	Truncated bool
}

// RunHook runs a hook of a class as the agent, in the folder dir (empty: the
// one of the agent), and waits for it. It is ErrUnsupported for a mestre-pty
// older than protocol 4: the engine never runs the hook as another account.
func (c *Client) RunHook(ctx context.Context, run, dir, event string, stdin []byte, timeout time.Duration) (HookResult, error) {
	if c.info.Proto < ptywire.ProtoHook {
		return HookResult{}, fmt.Errorf("%w: hook needs protocol %d, this one speaks %d", ErrUnsupported, ptywire.ProtoHook, c.info.Proto)
	}
	r, err := c.do(ctx, ptywire.Msg{T: ptywire.TypeHook, Run: run, Dir: dir, Event: event, Stdin: stdin, TimeoutMS: timeout.Milliseconds()})
	if err != nil {
		return HookResult{}, err
	}
	res := HookResult{Output: r.msg.Output, TimedOut: r.msg.TimedOut, Truncated: r.msg.Truncated}
	if r.msg.ExitCode != nil {
		res.Exit = *r.msg.ExitCode
	}
	return res, nil
}
