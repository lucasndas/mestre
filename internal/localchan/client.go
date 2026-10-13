package localchan

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

// Defaults of the client.
const (
	// DefaultLiveness is how long the CLI waits for a line from the engine
	// before it takes the connection for dead: three signs of life.
	DefaultLiveness = 3 * DefaultHeartbeat
	// InitialBackoff and MaxBackoff are the waits between two attempts to
	// connect to an engine that is restarting; the wait doubles up to the
	// maximum.
	InitialBackoff = 100 * time.Millisecond
	MaxBackoff     = 2 * time.Second
)

// ClientConfig configures a Client.
type ClientConfig struct {
	Clock clock.Clock
	Dial  Dialer
	// ConnectWindow (60 s) is how long a connection that is refused is
	// retried before engine_unreachable; a negative value makes a single
	// attempt (`mestre debug` wants an answer, not a wait).
	ConnectWindow time.Duration
	// Liveness (45 s) is the silence after which the connection is taken
	// for dead and the request is resent on a new one.
	Liveness time.Duration
}

// Client is the CLI side of the local channel.
type Client struct {
	cfg ClientConfig
	clk clock.Clock
}

// NewClient returns a Client.
func NewClient(cfg ClientConfig) *Client {
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.ConnectWindow == 0 {
		cfg.ConnectWindow = DefaultConnectWindow
	}
	if cfg.Liveness == 0 {
		cfg.Liveness = DefaultLiveness
	}
	return &Client{cfg: cfg, clk: cfg.Clock}
}

// CallOptions are the options of one call.
type CallOptions struct {
	// Timeout is the time limit of the command itself; the call waits that
	// plus TimeoutGrace. Zero means DefaultCommandTimeout.
	Timeout time.Duration
	// OnPartial receives the data of each partial response, signs of life
	// included; the CLI shows them only with --debug.
	OnPartial func(json.RawMessage)
}

// Call sends the request and returns its final response. It fills in the
// version and, if empty, the id. Failures of the channel itself come back
// as responses, with the codes and exit codes of docs/cli/convencoes.md:
// engine_unreachable (5) when the engine does not accept connections for
// ConnectWindow, timeout (6) when there is no answer in time, too_large (8)
// for a request over MaxLine. The error is only the cancellation of ctx.
//
// A connection that drops in the middle of a request is reopened and the
// same request, with the same id, is sent again: the engine answers with the
// stored response or goes on with the request in progress, never running it
// twice. That time counts for the time limit of the command.
func (c *Client) Call(ctx context.Context, req Request, opt CallOptions) (Response, error) {
	req.V = Version
	if req.ID == "" {
		id, err := NewID(c.clk)
		if err != nil {
			return Response{}, err
		}
		req.ID = id
	}
	if req.Args == nil {
		req.Args = []json.RawMessage{}
	}
	if req.Flags == nil {
		req.Flags = map[string]any{}
	}
	line, err := marshalLine(req)
	if err != nil {
		return Response{}, err
	}
	if len(line) > MaxLine+1 {
		return c.fail(req, CodeTooLarge, fmt.Sprintf("the request is over %d bytes", MaxLine),
			"send big content as a file path (--file, --attach)"), nil
	}
	limit := opt.Timeout
	if limit <= 0 {
		limit = DefaultCommandTimeout
	}
	// The time limit of the command counts from the first connection: a
	// refused connection has its own window. A reconnection after a drop
	// counts for the limit.
	var tctx context.Context
	for {
		cctx := ctx
		if tctx != nil {
			cctx = tctx
		}
		conn, derr := c.connect(cctx)
		if derr != nil {
			return c.connectFailure(ctx, tctx, req, derr)
		}
		if tctx == nil {
			var cancel context.CancelFunc
			tctx, cancel = clock.WithTimeout(ctx, c.clk, limit+TimeoutGrace)
			defer cancel()
		}
		resp, dropped := c.exchange(tctx, conn, req, line, opt)
		if !dropped {
			return resp, nil
		}
		if tctx.Err() != nil {
			return c.connectFailure(ctx, tctx, req, tctx.Err())
		}
	}
}

// connectFailure turns the reason a call could not go on into a response.
func (c *Client) connectFailure(ctx, tctx context.Context, req Request, err error) (Response, error) {
	if ctx.Err() != nil {
		return Response{}, ctx.Err()
	}
	if tctx != nil && tctx.Err() != nil {
		return c.fail(req, CodeTimeout, "no answer from the engine in time; the request is still pending there",
			"do not resend it: a late answer goes to your inbox"), nil
	}
	return c.fail(req, CodeEngineUnreachable, "the local engine does not answer: "+err.Error(),
		"run mestre debug; the Guardian restarts the engine"), nil
}

func (c *Client) fail(req Request, code, msg, hint string) Response {
	r := Fail(code, msg, hint)
	r.V, r.ID = Version, req.ID
	return r
}

// connect opens a connection, retrying a refused one with a growing wait
// for up to the connect window.
func (c *Client) connect(ctx context.Context) (net.Conn, error) {
	window := c.cfg.ConnectWindow
	wctx, cancel := ctx, context.CancelFunc(func() {})
	if window > 0 {
		wctx, cancel = clock.WithTimeout(ctx, c.clk, window)
	}
	defer cancel()
	wait := InitialBackoff
	for {
		conn, err := c.cfg.Dial(wctx)
		if err == nil {
			return conn, nil
		}
		// Not being allowed is not the engine restarting.
		if window < 0 || errors.Is(err, fs.ErrPermission) || wctx.Err() != nil {
			return nil, err
		}
		if clock.Sleep(wctx, c.clk, wait) != nil {
			return nil, err
		}
		wait = min(wait*2, MaxBackoff)
	}
}

// exchange sends the request on conn and reads until the final response.
// dropped is true when the connection ended first (or was taken for dead):
// the caller connects again and resends.
func (c *Client) exchange(ctx context.Context, conn net.Conn, req Request, line []byte, opt CallOptions) (resp Response, dropped bool) {
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	alive := c.clk.AfterFunc(c.cfg.Liveness, func() { _ = conn.Close() })
	defer alive.Stop()

	if _, err := conn.Write(line); err != nil {
		return Response{}, true
	}
	br := bufio.NewReaderSize(conn, 64<<10)
	for {
		b, err := readLine(br)
		if err != nil {
			if errors.Is(err, ErrTooLarge) {
				return c.fail(req, CodeInternal, "the engine sent a line over the limit", ""), false
			}
			return Response{}, true
		}
		alive.Reset(c.cfg.Liveness)
		var r Response
		if err := json.Unmarshal(b, &r); err != nil {
			return c.fail(req, CodeInternal, "the engine sent a line that is not JSON: "+err.Error(), ""), false
		}
		if r.ID != "" && r.ID != req.ID {
			continue // not ours
		}
		if r.Partial {
			if opt.OnPartial != nil {
				opt.OnPartial(r.Data)
			}
			continue
		}
		return r, false
	}
}
