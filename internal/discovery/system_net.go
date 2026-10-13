package discovery

import (
	"context"
	"fmt"
	"net"
	"net/netip"
)

type systemConn struct{ c *net.UDPConn }

func (s systemConn) ReadFrom(p []byte) (int, string, error) {
	n, from, err := s.c.ReadFromUDPAddrPort(p)
	if err != nil {
		return 0, "", err
	}
	return n, from.String(), nil
}

func (s systemConn) WriteTo(p []byte, to string) error {
	ap, err := netip.ParseAddrPort(to)
	if err != nil {
		return fmt.Errorf("discovery: write to %q: %w", to, err)
	}
	_, err = s.c.WriteToUDPAddrPort(p, ap)
	return err
}

func (s systemConn) Close() error { return s.c.Close() }

// ListenShared implements PacketNetwork: 0.0.0.0:port with SO_REUSEADDR (and
// SO_REUSEPORT on Linux), so the instances of a machine share the port.
func (s SystemPacketNetwork) ListenShared(port int) (PacketConn, error) {
	if err := s.guard(); err != nil {
		return nil, err
	}
	lc := net.ListenConfig{Control: shareControl}
	pc, err := lc.ListenPacket(context.Background(), "udp4", hostPort("0.0.0.0", port))
	if err != nil {
		return nil, fmt.Errorf("discovery: listen udp %d: %w", port, err)
	}
	return systemConn{pc.(*net.UDPConn)}, nil
}

// Open implements PacketNetwork.
func (s SystemPacketNetwork) Open() (PacketConn, error) {
	if err := s.guard(); err != nil {
		return nil, err
	}
	var lc net.ListenConfig
	pc, err := lc.ListenPacket(context.Background(), "udp4", "0.0.0.0:0")
	if err != nil {
		return nil, fmt.Errorf("discovery: open udp: %w", err)
	}
	return systemConn{pc.(*net.UDPConn)}, nil
}
