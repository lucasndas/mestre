package localchan

import (
	"fmt"
	"net"

	"github.com/lucasndas/mestre/internal/terminal"
)

// NewSystemIdentifier returns the identifier of Windows.
func NewSystemIdentifier(cfg SystemConfig) Identifier {
	sys := cfg.System
	if sys == nil {
		sys = terminal.NewSystem()
	}
	return &JobIdentifier{Peers: NewPeerSource(sys), Terminals: cfg.Terminals, Jobs: cfg.Jobs, Clock: cfg.Clock}
}

// systemPeers reads the peer with SIO_AF_UNIX_GETPEERPID.
type systemPeers struct{ sys terminal.System }

// NewPeerSource returns the PeerSource of Windows.
func NewPeerSource(sys terminal.System) PeerSource { return systemPeers{sys} }

func (s systemPeers) Peer(c net.Conn) (Peer, error) {
	pid, err := s.sys.PeerPID(c)
	if err != nil {
		return Peer{}, err
	}
	p := Peer{PID: pid}
	start, err := s.sys.Identify(pid)
	if err != nil {
		return p, fmt.Errorf("open the caller %d: %w", pid, err)
	}
	p.Start = start
	if p.StartedAt, err = FiletimeToTime(start); err != nil {
		return p, err
	}
	return p, nil
}
