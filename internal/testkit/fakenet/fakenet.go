// Package fakenet is the in-memory network of the fast suite
// (docs/qualidade/dubles.md#rede-falsa): Listen and Dial by name, no
// sockets, usable inside testing/synctest, with injected failures. Real TLS
// runs on top of its net.Conn. UDP broadcast, fake mDNS and the Citadel IP
// change and isolating a whole machine arrive with discovery and the Citadel
// and Towers in M2.
package fakenet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/testkit/memio"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Errors seen by code under test.
var (
	ErrConnectionRefused = errors.New("fakenet: connection refused")
	ErrUnreachable       = errors.New("fakenet: host unreachable (partition)")
	ErrConnectionReset   = errors.New("fakenet: connection reset")
)

// Network is one fake network.
type Network struct {
	clk clock.Clock

	mu         sync.Mutex
	listeners  map[string]*listener
	conns      []*Conn
	partitions map[[2]string]bool
	halfOpen   map[[2]string]bool
	latency    time.Duration
	nextPort   int
}

// New returns an empty network driven by clk.
func New(clk clock.Clock) *Network {
	return &Network{
		clk:        clk,
		listeners:  map[string]*listener{},
		partitions: map[[2]string]bool{},
		halfOpen:   map[[2]string]bool{},
		nextPort:   40000,
	}
}

// Addr is a fake address "host:port".
type Addr string

func (Addr) Network() string  { return "fakenet" }
func (a Addr) String() string { return string(a) }

func hostOf(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

func pair(a, b string) [2]string {
	if a > b {
		a, b = b, a
	}
	return [2]string{a, b}
}

// Listen listens on addr ("host:port").
func (n *Network) Listen(addr string) (net.Listener, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.listeners[addr]; ok {
		return nil, fmt.Errorf("fakenet: listen %s: address in use", addr)
	}
	l := &listener{net: n, addr: Addr(addr), accept: make(chan *Conn), closed: make(chan struct{})}
	n.listeners[addr] = l
	return l, nil
}

// Dial connects host from to addr.
func (n *Network) Dial(ctx context.Context, from, addr string) (net.Conn, error) {
	n.mu.Lock()
	if n.partitions[pair(from, hostOf(addr))] {
		n.mu.Unlock()
		return nil, &net.OpError{Op: "dial", Net: "fakenet", Addr: Addr(addr), Err: ErrUnreachable}
	}
	l, ok := n.listeners[addr]
	if !ok {
		n.mu.Unlock()
		return nil, &net.OpError{Op: "dial", Net: "fakenet", Addr: Addr(addr), Err: ErrConnectionRefused}
	}
	n.nextPort++
	local := Addr(fmt.Sprintf("%s:%d", from, n.nextPort))
	c2s, s2c := memio.New(n.clk, false), memio.New(n.clk, false)
	c2s.SetLatency(n.latency)
	s2c.SetLatency(n.latency)
	if n.halfOpen[pair(from, hostOf(addr))] {
		c2s.SetBlackhole(true)
		s2c.SetBlackhole(true)
	}
	client := &Conn{net: n, local: local, remote: Addr(addr), r: s2c, w: c2s}
	server := &Conn{net: n, local: Addr(addr), remote: local, r: c2s, w: s2c}
	client.peer, server.peer = server, client
	n.conns = append(n.conns, client, server)
	n.mu.Unlock()

	select {
	case l.accept <- server:
		return client, nil
	case <-l.closed:
		return nil, &net.OpError{Op: "dial", Net: "fakenet", Addr: Addr(addr), Err: ErrConnectionRefused}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// DialContext returns a dialer bound to host from, for http.Transport and
// TLS clients.
func (n *Network) DialContext(from string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, _, addr string) (net.Conn, error) { return n.Dial(ctx, from, addr) }
}

// Cut resets every open connection between hosts a and b.
func (n *Network) Cut(a, b string) {
	for _, c := range n.between(a, b) {
		c.r.Break(ErrConnectionReset)
		c.w.Break(ErrConnectionReset)
	}
}

// Partition cuts a and b and refuses new dials between them until Heal.
func (n *Network) Partition(a, b string) {
	n.mu.Lock()
	n.partitions[pair(a, b)] = true
	n.mu.Unlock()
	n.Cut(a, b)
}

// Heal undoes Partition and HalfOpen between a and b.
func (n *Network) Heal(a, b string) {
	n.mu.Lock()
	delete(n.partitions, pair(a, b))
	delete(n.halfOpen, pair(a, b))
	n.mu.Unlock()
	for _, c := range n.between(a, b) {
		c.w.SetBlackhole(false)
	}
}

// HalfOpen silently drops everything sent between a and b on the open
// connections, without any end-of-stream signal.
func (n *Network) HalfOpen(a, b string) {
	n.mu.Lock()
	n.halfOpen[pair(a, b)] = true
	n.mu.Unlock()
	for _, c := range n.between(a, b) {
		c.w.SetBlackhole(true)
	}
}

// SetLatency delays every byte sent on connections opened from now on.
func (n *Network) SetLatency(d time.Duration) {
	n.mu.Lock()
	n.latency = d
	n.mu.Unlock()
}

func (n *Network) between(a, b string) []*Conn {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []*Conn
	for _, c := range n.conns {
		h1, h2 := hostOf(c.local.String()), hostOf(c.remote.String())
		if pair(h1, h2) == pair(a, b) {
			out = append(out, c)
		}
	}
	return out
}

type listener struct {
	net    *Network
	addr   Addr
	accept chan *Conn
	once   sync.Once
	closed chan struct{}
}

func (l *listener) Accept() (net.Conn, error) {
	select {
	case c := <-l.accept:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *listener) Close() error {
	l.once.Do(func() {
		close(l.closed)
		l.net.mu.Lock()
		delete(l.net.listeners, string(l.addr))
		l.net.mu.Unlock()
	})
	return nil
}

func (l *listener) Addr() net.Addr { return l.addr }

// Conn is one end of a fake connection.
type Conn struct {
	net           *Network
	local, remote Addr
	r, w          *memio.Pipe
	peer          *Conn
}

var _ net.Conn = (*Conn)(nil)

func (c *Conn) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	return n, wrapTimeout(err)
}

func (c *Conn) Write(b []byte) (int, error) { return c.w.Write(b) }

// Close sends end-of-stream to the peer and stops local reads.
func (c *Conn) Close() error {
	c.w.CloseWrite()
	c.r.CloseRead()
	return nil
}

func (c *Conn) LocalAddr() net.Addr  { return c.local }
func (c *Conn) RemoteAddr() net.Addr { return c.remote }

func (c *Conn) SetDeadline(t time.Time) error { return c.SetReadDeadline(t) }

func (c *Conn) SetReadDeadline(t time.Time) error {
	c.r.SetReadDeadline(t)
	return nil
}

// SetWriteDeadline is a no-op: writes never block.
func (c *Conn) SetWriteDeadline(time.Time) error { return nil }

func wrapTimeout(err error) error {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return &net.OpError{Op: "read", Net: "fakenet", Err: err}
	}
	return err
}
