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

// DialContext connects to TCP addr.
func (s SystemNetwork) DialContext(ctx context.Context, addr string) (net.Conn, error) {
	if err := s.guard(); err != nil {
		return nil, err
	}
	var d net.Dialer
	return d.DialContext(ctx, "tcp", addr)
}
