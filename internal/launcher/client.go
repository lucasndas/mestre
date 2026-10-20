package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/lucasndas/mestre/internal/terminal"
)

// Packed is what goes with a start-pty request besides its fields: the
// secret in the request, or descriptors.
type Packed struct {
	Secret []byte
	Roles  []string
	Files  []*os.File
	// Sent is called when the request was sent, whatever the answer: the
	// descriptors are the launcher's now.
	Sent func()
	// Failed is called when the terminal did not start, to undo what Pack
	// made (the socket file).
	Failed func()
}

// Client is the engine side: it implements terminal.Spawner by asking the
// mestre-launcher, which starts the mestre-pty with the account of the
// agents and ends the tree of a terminal.
type Client struct {
	// Dial opens a connection to the launcher.
	Dial func(ctx context.Context) (net.Conn, error)
	// Pack prepares the secret (and, on Linux, the listening socket) of a
	// terminal: PackFDs on Linux, PackInline on Windows.
	Pack func(spec terminal.SpawnSpec) (Packed, error)
	// NoKill: the system ends the tree by itself (Windows: the Job Object of
	// the mestre-pty), so KillCgroup has nothing to ask.
	NoKill bool
}

var _ terminal.Spawner = (*Client)(nil)

// PackInline puts the secret in the request (Windows).
func PackInline(spec terminal.SpawnSpec) (Packed, error) {
	return Packed{Secret: spec.Secret}, nil
}

// Spawn asks the launcher to start the mestre-pty of spec.ID, version
// spec.Version (spec.Binary is not used: the launcher takes the binary from
// its own folder).
func (c *Client) Spawn(ctx context.Context, spec terminal.SpawnSpec) (terminal.Spawned, error) {
	if err := terminal.ValidateID(spec.ID); err != nil {
		return terminal.Spawned{}, err
	}
	if err := ValidateVersion(spec.Version); err != nil {
		return terminal.Spawned{}, err
	}
	p, err := c.Pack(spec)
	if err != nil {
		return terminal.Spawned{}, err
	}
	resp, err := c.exchange(ctx, Request{
		Op: OpStartPTY, ID: spec.ID, Version: spec.Version, Socket: spec.SocketPath,
		Secret: p.Secret, Files: p.Roles,
	}, p.Files)
	if p.Sent != nil {
		p.Sent()
	}
	if err != nil {
		if p.Failed != nil {
			p.Failed()
		}
		return terminal.Spawned{}, fmt.Errorf("start mestre-pty %s: %w", spec.ID, err)
	}
	return terminal.Spawned{
		ProcID:    terminal.ProcID{PID: resp.PID, Start: resp.Start},
		CgroupDir: resp.CgroupDir, LogonSID: resp.Logon,
	}, nil
}

// KillCgroup asks the launcher to end the tree of terminal id.
func (c *Client) KillCgroup(ctx context.Context, id string) error {
	if c.NoKill {
		return nil
	}
	if err := terminal.ValidateID(id); err != nil {
		return err
	}
	if _, err := c.exchange(ctx, Request{Op: OpKill, ID: id}, nil); err != nil {
		return fmt.Errorf("kill terminal %s: %w", id, err)
	}
	return nil
}

var _ terminal.TreeKiller = (*Client)(nil)

// ErrNoKill is returned by KillTree where the launcher does not end
// processes (Windows: the Job Object of the mestre-pty ends the tree, and the
// engine holds the right to end the mestre-pty).
var ErrNoKill = errors.New("this launcher does not end processes")

// KillTree asks the launcher to end the mestre-pty p of terminal id and the
// sessions of its agent, with the right of the account of the agents; the
// launcher checks the start identity of every process before it signals.
func (c *Client) KillTree(ctx context.Context, id string, p terminal.ProcID, sessions []int) error {
	if c.NoKill {
		return ErrNoKill
	}
	if err := terminal.ValidateID(id); err != nil {
		return err
	}
	req := Request{Op: OpKill, ID: id, PID: p.PID, Start: p.Start, Sessions: sessions}
	if err := validateKillTarget(req); err != nil {
		return err
	}
	if req.PID == 0 {
		return errors.New("kill by identity: no process")
	}
	if _, err := c.exchange(ctx, req, nil); err != nil {
		return fmt.Errorf("kill the processes of terminal %s: %w", id, err)
	}
	return nil
}

// exchange sends one request and reads the answer.
func (c *Client) exchange(ctx context.Context, req Request, files []*os.File) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	nc, err := c.Dial(ctx)
	if err != nil {
		return Response{}, fmt.Errorf("connect to the mestre-launcher: %w", err)
	}
	conn := NewConn(nc)
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	b, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	if err := conn.WriteFrame(b, files); err != nil {
		return Response{}, ctxOr(ctx, fmt.Errorf("send the request: %w", err))
	}
	frame, extra, err := conn.ReadFrame()
	closeAll(extra)
	if err != nil {
		return Response{}, ctxOr(ctx, fmt.Errorf("read the answer: %w", err))
	}
	var resp Response
	if err := json.Unmarshal(frame, &resp); err != nil {
		return Response{}, fmt.Errorf("malformed answer: %w", err)
	}
	if !resp.OK {
		return Response{}, &Error{Code: resp.Code, Msg: resp.Error}
	}
	return resp, nil
}

// ctxOr reports the end of ctx rather than the error it caused on the
// connection.
func ctxOr(ctx context.Context, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		return errors.Join(cerr, err)
	}
	return err
}
