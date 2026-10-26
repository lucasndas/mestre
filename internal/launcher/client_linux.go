package launcher

import (
	"context"
	"net"
	"os"

	"github.com/mestre-ia/mestre/internal/terminal"
)

// NewClient is the Spawner of the engine on Linux: it asks the launcher at
// the Unix socket path (/run/mestre/<inst>/launcher.sock).
func NewClient(path string) *Client {
	return &Client{
		Dial: func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
		Pack: PackFDs,
	}
}

// PackFDs makes what a mestre-pty needs on Linux and passes it as
// descriptors: the socket of the terminal, already listening (mode 0600, so
// the account of the agents never crosses the folder), the pipe that holds
// the secret, and the log of the mestre-pty when the engine wants one.
func PackFDs(spec terminal.SpawnSpec) (Packed, error) {
	lf, err := terminal.ListenSocket(spec.SocketPath)
	if err != nil {
		return Packed{}, err
	}
	sr, err := terminal.SecretPipe(spec.Secret)
	if err != nil {
		_ = lf.Close()
		_ = os.Remove(spec.SocketPath)
		return Packed{}, err
	}
	p := Packed{Roles: []string{RoleListen, RoleSecret}, Files: []*os.File{lf, sr}}
	if spec.LogPath != "" {
		logf, err := os.OpenFile(spec.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			closeAll(p.Files)
			_ = os.Remove(spec.SocketPath)
			return Packed{}, err
		}
		p.Roles = append(p.Roles, RoleLog)
		p.Files = append(p.Files, logf)
	}
	p.Sent = func() { closeAll(p.Files) }
	p.Failed = func() { _ = os.Remove(spec.SocketPath) }
	return p, nil
}
