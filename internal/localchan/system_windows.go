package localchan

import (
	"errors"
	"fmt"
	"net"

	"golang.org/x/sys/windows"

	"github.com/mestre-ia/mestre/internal/terminal"
)

// NewSystemIdentifier returns the identifier of Windows.
func NewSystemIdentifier(cfg SystemConfig) Identifier {
	sys := cfg.System
	if sys == nil {
		sys = terminal.NewSystem()
	}
	return &JobIdentifier{Peers: NewPeerSource(sys), Terminals: cfg.Terminals, Jobs: cfg.Jobs}
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
		p.Gone = errors.Is(err, terminal.ErrNoProcess)
		return p, fmt.Errorf("open the caller %d: %w", pid, err)
	}
	p.Start = start
	if p.StartedAt, err = FiletimeToTime(start); err != nil {
		return p, err
	}
	// The user of the process counts for the caps of the server, as the
	// UID on Linux. The terminal (Job Object) is asked of the mestre-pty
	// only after the request, so no group is known here.
	if sid, err := peerSID(pid); err == nil {
		p.UID = sid
	}
	return p, nil
}

// peerSID is the SID of the user of the process pid, from its token.
func peerSID(pid int) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err != nil {
		return "", err
	}
	defer func() { _ = tok.Close() }()
	u, err := tok.GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String(), nil
}
