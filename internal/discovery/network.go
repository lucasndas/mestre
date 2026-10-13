package discovery

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
)

// PacketConn is a UDP socket. Addresses are "ip:port".
type PacketConn interface {
	// ReadFrom blocks until a datagram arrives or the socket is closed.
	ReadFrom(p []byte) (n int, from string, err error)
	WriteTo(p []byte, to string) error
	Close() error
}

// PacketNetwork is where the datagrams come from: SystemPacketNetwork in
// production and in the full suite, the fake network of
// internal/testkit/fakenet (through internal/testkit/discorig) in the fast
// suite.
type PacketNetwork interface {
	// ListenShared binds the discovery port on every address. Other sockets,
	// of the other instances of the machine, share it: each one receives
	// every broadcast.
	ListenShared(port int) (PacketConn, error)
	// Open binds a free port, for the requests and their unicast answers.
	Open() (PacketConn, error)
	// Broadcasts are the addresses a request goes to: the limited broadcast
	// and the one of each active interface, all with port.
	Broadcasts(port int) []string
}

// ErrNetworkInFastSuite is the fast-suite guard of this layer
// (docs/qualidade/testes.md#regras-e-erros).
var ErrNetworkInFastSuite = errors.New("real network in the fast suite: use the fake network (internal/testkit/fakenet)")

// SystemPacketNetwork is UDP through the operating system.
type SystemPacketNetwork struct {
	// Suite is the value of MESTRE_TEST_SUITE ("" in production); the fast
	// suite is refused.
	Suite string
	// Targets, when set, replace the broadcast addresses of the interfaces
	// (a test that cannot rely on the broadcast of the machine it runs on).
	Targets []string
}

var _ PacketNetwork = SystemPacketNetwork{}

func (s SystemPacketNetwork) guard() error {
	if s.Suite == "fast" {
		return ErrNetworkInFastSuite
	}
	return nil
}

// Broadcasts implements PacketNetwork.
func (s SystemPacketNetwork) Broadcasts(port int) []string {
	if len(s.Targets) > 0 {
		return s.Targets
	}
	out := []string{net.JoinHostPort("255.255.255.255", strconv.Itoa(port))}
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	var addrs []netip.Prefix
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagBroadcast == 0 {
			continue
		}
		as, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range as {
			if ipn, ok := a.(*net.IPNet); ok {
				if p, ok := prefixOf(ipn); ok {
					addrs = append(addrs, p)
				}
			}
		}
	}
	for _, b := range broadcastsOf(addrs) {
		out = append(out, net.JoinHostPort(b.String(), strconv.Itoa(port)))
	}
	return out
}

func prefixOf(n *net.IPNet) (netip.Prefix, bool) {
	ip, ok := netip.AddrFromSlice(n.IP)
	if !ok {
		return netip.Prefix{}, false
	}
	ones, bits := n.Mask.Size()
	if bits == 0 {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(ip.Unmap(), ones), true
}

// broadcastsOf returns the directed broadcast address of each IPv4 prefix
// that has one (a /31 or /32 has not), without repeating any.
func broadcastsOf(prefixes []netip.Prefix) []netip.Addr {
	var out []netip.Addr
	seen := map[netip.Addr]bool{}
	for _, p := range prefixes {
		a := p.Addr()
		if !a.Is4() || p.Bits() > 30 || p.Bits() < 0 || a.IsLoopback() {
			continue
		}
		b := a.As4()
		host := 32 - p.Bits()
		for i := 3; i >= 0 && host > 0; i-- {
			bits := min(8, host)
			b[i] |= byte(1<<bits - 1)
			host -= bits
		}
		bc := netip.AddrFrom4(b)
		if !seen[bc] {
			seen[bc] = true
			out = append(out, bc)
		}
	}
	return out
}

// hostPort joins an address and a port.
func hostPort(host string, port int) string { return net.JoinHostPort(host, strconv.Itoa(port)) }

// splitAddr reads "host:port".
func splitAddr(addr string) (string, int, error) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, fmt.Errorf("discovery: address %q: %w", addr, err)
	}
	port, err := strconv.Atoi(p)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("discovery: address %q: bad port", addr)
	}
	return h, port, nil
}
