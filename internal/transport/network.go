package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
)

// Network is where connections come from: SystemNetwork in production and
// the full suite, the fake network of internal/testkit/fakenet (Host) in the
// fast suite.
type Network interface {
	Listen(addr string) (net.Listener, error)
	DialContext(ctx context.Context, addr string) (net.Conn, error)
}

// ErrNetworkInFastSuite is the fast-suite guard of this layer
// (docs/qualidade/testes.md#regras-e-erros).
var ErrNetworkInFastSuite = errors.New("real network in the fast suite: use the fake network (internal/testkit/fakenet)")

// SystemNetwork is TCP through the operating system.
type SystemNetwork struct {
	// Suite is the value of MESTRE_TEST_SUITE ("" in production); the fast
	// suite is refused.
	Suite string
}

var _ Network = SystemNetwork{}

func (s SystemNetwork) guard() error {
	if s.Suite == "fast" {
		return ErrNetworkInFastSuite
	}
	return nil
}

// Listen listens on TCP addr.
func (s SystemNetwork) Listen(addr string) (net.Listener, error) {
	if err := s.guard(); err != nil {
		return nil, err
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("transport: listen %s: %w", addr, err)
	}
	return ln, nil
}

// DialContext connects to TCP addr. The time limit comes from the
// cancellation of ctx, not from its Deadline: the Deadline of a context made by
// clock.WithTimeout is a time of the injected clock, which the operating system
// would read as wall time (a clock that is behind makes every dial fail at
// once with an i/o timeout, a clock that is ahead never times out).
func (s SystemNetwork) DialContext(ctx context.Context, addr string) (net.Conn, error) {
	if err := s.guard(); err != nil {
		return nil, err
	}
	dctx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	defer cancel(nil)
	stop := context.AfterFunc(ctx, func() { cancel(context.Cause(ctx)) })
	defer stop()
	var d net.Dialer
	conn, err := d.DialContext(dctx, "tcp", addr)
	if err != nil && ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	return conn, err
}
