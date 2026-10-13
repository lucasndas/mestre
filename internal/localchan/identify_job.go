package localchan

import (
	"context"
	"errors"
	"net"
	"time"
)

// JobIdentifier identifies callers on Windows: the PID on the other end of
// the socket, rejected if the process started after the connection was
// accepted (a reused PID), and the Job Object of the terminal of the
// character, asked of its mestre-pty (docs/arquitetura/seguranca.md). The
// logic is the same on every system so the fast suite tests it with fakes;
// the peer (SIO_AF_UNIX_GETPEERPID) and the Job Object are Windows code.
type JobIdentifier struct {
	Peers     PeerSource
	Terminals Terminals
	Jobs      JobAsker
}

var _ Identifier = (*JobIdentifier)(nil)

// Accept reads the peer before anything else happens to the connection.
func (i *JobIdentifier) Accept(c net.Conn, acceptedAt time.Time) (Peer, error) {
	p, err := i.Peers.Peer(c)
	p.AcceptedAt = acceptedAt
	return p, err
}

// Identify asks the mestre-pty of each live terminal of the character
// whether the caller is in its Job Object.
func (i *JobIdentifier) Identify(ctx context.Context, p Peer, agentID string) (Identity, error) {
	if agentID == "" {
		return Identity{}, refuseErr("no character to check", ErrNoAgent)
	}
	if p.PID <= 0 || p.Start == "" {
		return Identity{}, refuse("the peer has no PID")
	}
	if !p.StartedAt.IsZero() && p.StartedAt.After(p.AcceptedAt) {
		return Identity{}, refuse("the caller started after the connection was accepted: its PID was reused")
	}
	terms := i.Terminals.OfCharacter(agentID)
	if len(terms) == 0 {
		return Identity{}, refuse("the character has no live terminal")
	}
	var errs []error
	for _, t := range terms {
		in, err := i.Jobs.InJob(ctx, t.ID, ProcRef{PID: p.PID, Start: p.Start})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if in {
			return Identity{AgentID: agentID, TerminalID: t.ID, PID: p.PID}, nil
		}
	}
	if len(errs) > 0 {
		return Identity{}, refuseErr("the Job Object of the terminal could not be asked", errors.Join(errs...))
	}
	return Identity{}, refuse("the caller is not in the Job Object of a terminal of the character")
}
