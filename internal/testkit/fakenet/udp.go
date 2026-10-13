package fakenet

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"sync"
)

// UDP of the fake network. A datagram goes to the sockets bound to the port
// of its destination. A broadcast address ("255.255.255.255" or any IPv4
// ending in ".255") reaches every socket bound to that port on every host,
// the sender's host included, as on a local network. A unicast datagram
// reaches one socket: when several sockets share the port (SO_REUSEADDR and
// SO_REUSEPORT on a real system) the first one that was bound takes it. A
// partition between two hosts drops their datagrams; BlockBroadcast is the
// firewall that drops every broadcast.

// ErrAddressInUse is returned when a port is taken by a socket that does not
// share it.
var ErrAddressInUse = errors.New("fakenet: address in use")

// ErrPacketConnClosed is returned by a closed PacketConn.
var ErrPacketConnClosed = net.ErrClosed

const datagramQueue = 64

type datagram struct {
	from string
	data []byte
}

type udpState struct {
	conns        map[string][]*PacketConn // by host
	nextPort     int
	broadcastOff bool
	lose         int
	sent         int
}

// PacketConn is a fake UDP socket.
type PacketConn struct {
	net    *Network
	host   string
	port   int
	shared bool
	in     chan datagram
	closed chan struct{}
	once   sync.Once
}

// ListenPacket binds a socket of host to port; port 0 picks a free one.
// shared says the socket accepts other sockets on the same port (all of them
// must be shared).
func (n *Network) ListenPacket(host string, port int, shared bool) (*PacketConn, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.udp.conns == nil {
		n.udp.conns = map[string][]*PacketConn{}
		n.udp.nextPort = 50000
	}
	host = n.resolve(host)
	if port == 0 {
		n.udp.nextPort++
		port = n.udp.nextPort
	}
	for _, c := range n.udp.conns[host] {
		if c.port == port && (!c.shared || !shared) {
			return nil, fmt.Errorf("fakenet: listen udp %s:%d: %w", host, port, ErrAddressInUse)
		}
	}
	c := &PacketConn{net: n, host: host, port: port, shared: shared, in: make(chan datagram, datagramQueue), closed: make(chan struct{})}
	n.udp.conns[host] = append(n.udp.conns[host], c)
	return c, nil
}

// LocalAddr is "ip:port", with the host name as the ip when it has none.
func (c *PacketConn) LocalAddr() string {
	c.net.mu.Lock()
	defer c.net.mu.Unlock()
	return net.JoinHostPort(c.net.addrOf(c.host), strconv.Itoa(c.port))
}

// ReadFrom blocks until a datagram arrives or the socket is closed.
func (c *PacketConn) ReadFrom(p []byte) (int, string, error) {
	select {
	case d := <-c.in:
		return copy(p, d.data), d.from, nil
	case <-c.closed:
		return 0, "", ErrPacketConnClosed
	}
}

// WriteTo sends p to "ip:port" (or a host name), or to a broadcast address.
func (c *PacketConn) WriteTo(p []byte, to string) error {
	select {
	case <-c.closed:
		return ErrPacketConnClosed
	default:
	}
	toHost, portStr, err := net.SplitHostPort(to)
	if err != nil {
		return fmt.Errorf("fakenet: write udp to %q: %w", to, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("fakenet: write udp to %q: %w", to, err)
	}
	n := c.net
	n.mu.Lock()
	defer n.mu.Unlock()
	n.udp.sent++
	if n.udp.lose > 0 {
		n.udp.lose--
		return nil
	}
	d := datagram{from: net.JoinHostPort(n.addrOf(c.host), strconv.Itoa(c.port)), data: append([]byte(nil), p...)}
	if isBroadcast(toHost) {
		if n.udp.broadcastOff {
			return nil
		}
		for host, conns := range n.udp.conns {
			if host != c.host && n.partitions[pair(c.host, host)] {
				continue
			}
			for _, s := range conns {
				if s.port == port {
					s.deliver(d)
				}
			}
		}
		return nil
	}
	host := n.resolve(toHost)
	if host != c.host && n.partitions[pair(c.host, host)] {
		return nil
	}
	for _, s := range n.udp.conns[host] {
		if s.port == port {
			s.deliver(d) // one socket: the first bound
			break
		}
	}
	return nil
}

func (c *PacketConn) deliver(d datagram) {
	select {
	case <-c.closed:
	case c.in <- d:
	default: // the queue is full: the datagram is lost, as on a real socket
	}
}

// Close unbinds the socket and wakes a ReadFrom in progress.
func (c *PacketConn) Close() error {
	c.once.Do(func() {
		close(c.closed)
		n := c.net
		n.mu.Lock()
		defer n.mu.Unlock()
		conns := n.udp.conns[c.host]
		for i, s := range conns {
			if s == c {
				n.udp.conns[c.host] = append(conns[:i:i], conns[i+1:]...)
				break
			}
		}
	})
	return nil
}

func isBroadcast(host string) bool {
	a, err := netip.ParseAddr(host)
	if err != nil || !a.Is4() {
		return false
	}
	return a.As4()[3] == 255
}

// BlockBroadcast drops (or lets through again) every broadcast datagram of
// the network: the firewall or the router that does not forward it.
func (n *Network) BlockBroadcast(blocked bool) {
	n.mu.Lock()
	n.udp.broadcastOff = blocked
	n.mu.Unlock()
}

// LoseNextDatagrams drops the next k datagrams sent on the network, whatever
// their destination.
func (n *Network) LoseNextDatagrams(k int) {
	n.mu.Lock()
	n.udp.lose = k
	n.mu.Unlock()
}

// Datagrams is how many datagrams were sent on the network.
func (n *Network) Datagrams() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.udp.sent
}

// ListenPacket binds a socket of this host.
func (h Host) ListenPacket(port int, shared bool) (*PacketConn, error) {
	return h.n.ListenPacket(h.name, port, shared)
}

// Broadcasts are the broadcast addresses of this host for port: the limited
// one and, when the host has an IPv4 address, the one of its /24.
func (h Host) Broadcasts(port int) []string {
	h.n.mu.Lock()
	defer h.n.mu.Unlock()
	out := []string{net.JoinHostPort("255.255.255.255", strconv.Itoa(port))}
	if a, err := netip.ParseAddr(h.n.ips[h.name]); err == nil && a.Is4() {
		b := a.As4()
		b[3] = 255
		out = append(out, net.JoinHostPort(netip.AddrFrom4(b).String(), strconv.Itoa(port)))
	}
	return out
}
