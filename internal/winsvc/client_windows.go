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
// instance: it asks the mestre-launcher at \\.\pipe\mestre-<inst>-launcher.
func NewLauncherClient(instance string) *launcher.Client {
	return NewLauncherClientAt(PipeName(instance))
}

// NewLauncherClientAt is NewLauncherClient for a pipe by name (the tests).
// The tree of a terminal ends with the Job Object of its mestre-pty, so
// there is nothing to ask the launcher to kill.
func NewLauncherClientAt(pipe string) *launcher.Client {
	return &launcher.Client{
		Dial:   func(ctx context.Context) (net.Conn, error) { return DialPipe(ctx, pipe) },
		Pack:   packSecret,
		NoKill: true,
	}
}

// packSecret prepares the folder and puts the secret in the request. The
// mestre-pty creates the socket itself; the engine only clears the one a
// dead terminal left.
func packSecret(spec terminal.SpawnSpec) (launcher.Packed, error) {
	if err := os.MkdirAll(filepath.Dir(spec.SocketPath), 0o700); err != nil {
		return launcher.Packed{}, err
	}
	_ = os.Remove(spec.SocketPath)
	return launcher.PackInline(spec)
}
