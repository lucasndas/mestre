//go:build windows

package winsvc

import (
	"context"
	"net"
	"os"
	"path/filepath"

	"github.com/lucasndas/mestre/internal/launcher"
	"github.com/lucasndas/mestre/internal/terminal"
)

// NewLauncherClient is the Spawner of the engine on Windows for the
// instance: it asks the mestre-launcher at \\.\pipe\mestre-<inst>-launcher,
// and puts the folder of the sockets of the mestre-pty in order (its ACL, see
// PTYDirACL) before each terminal.
func NewLauncherClient(instance string) *launcher.Client {
	return NewLauncherClientWith(PipeName(instance), &PTYDirACL{AgentAccount: AgentAccountName(instance)})
}

// NewLauncherClientAt is NewLauncherClient for a pipe by name and a folder
// of sockets with the ACL of the test account (the tests). The tree of a
// terminal ends with the Job Object of its mestre-pty, so there is nothing
// to ask the launcher to kill.
func NewLauncherClientAt(pipe string) *launcher.Client { return NewLauncherClientWith(pipe, nil) }

// NewLauncherClientWith is NewLauncherClientAt with the ACL the folder of the
// sockets gets before each terminal; nil leaves the folder as it is.
func NewLauncherClientWith(pipe string, dir *PTYDirACL) *launcher.Client {
	return &launcher.Client{
		Dial:   func(ctx context.Context) (net.Conn, error) { return DialPipe(ctx, pipe) },
		Pack:   func(spec terminal.SpawnSpec) (launcher.Packed, error) { return packSecret(dir, spec) },
		NoKill: true,
	}
}

// packSecret prepares the folder and puts the secret in the request. The
// mestre-pty creates the socket itself; the engine only clears the one a
// dead terminal left. Without an ACL to apply, the folder is only made.
func packSecret(dir *PTYDirACL, spec terminal.SpawnSpec) (launcher.Packed, error) {
	folder := filepath.Dir(spec.SocketPath)
	if dir != nil {
		if err := dir.Ensure(folder); err != nil {
			return launcher.Packed{}, err
		}
	} else if err := os.MkdirAll(folder, 0o700); err != nil {
		return launcher.Packed{}, err
	}
	_ = os.Remove(spec.SocketPath)
	return launcher.PackInline(spec)
}
