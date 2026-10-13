// Command mestre-pty holds one terminal: the PTY (ConPTY on Windows), the
// agent, the emulator and the buffer. It survives restarts of the engine,
// which reconnects to its socket (docs/arquitetura/terminal.md#mestre-pty).
//
// The engine starts it with:
//
//	--id        terminal id (for the log)
//	--socket    path of the Unix socket; created here unless --listen-fd is set
//	--listen-fd Linux: the descriptor of the socket the engine already made
//	--secret-fd descriptor (handle on Windows) of the pipe that carries the
//	            32-byte secret of the handshake: never argv, never the environment
//	--ready-fd  optional: descriptor of a pipe to write one byte to once the
//	            socket belongs to this process (Linux; see signalReady)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/terminal/ptyd"
	"github.com/lucasndas/mestre/internal/terminal/ptywire"
	"github.com/lucasndas/mestre/pkg/clock"
)

// version is the version of this mestre-pty (SemVer, independent of the
// engine's); the release sets it with -ldflags "-X main.version=...".
var version = "1.0.0"

// protoVersion is the protocol version announced; 0 means the current one.
// Only the ptylegacy build of the tests changes it.
var protoVersion = 0

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "mestre-pty:", err)
		os.Exit(1)
	}
}

func run(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("mestre-pty", flag.ContinueOnError)
	id := fs.String("id", "", "terminal id")
	socket := fs.String("socket", "", "socket path")
	listenFD := fs.Int("listen-fd", 0, "descriptor of the listening socket")
	secretFD := fs.Int("secret-fd", 0, "descriptor of the pipe with the secret")
	readyFD := fs.Int("ready-fd", 0, "descriptor of the pipe to signal readiness on")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" || *secretFD == 0 || (*socket == "" && *listenFD == 0) {
		return errors.New("usage: mestre-pty --id ID --socket PATH [--listen-fd N] --secret-fd N")
	}
	if err := ptyd.Harden(); err != nil {
		return err
	}
	secret, err := readSecret(*secretFD)
	if err != nil {
		return err
	}
	ln, err := ptyd.Listen(*socket, *listenFD)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	if *readyFD != 0 {
		if err := signalReady(*readyFD); err != nil {
			return err
		}
	}
	log := logging.New(stderr, logging.NewMasker(), logging.Options{}).With("terminal", *id)

	var srv *ptyd.Server
	sess := ptyd.NewSession(ptyd.SessionConfig{
		Runner:  &ptyd.PTYRunner{Suite: os.Getenv(ptywire.EnvTestSuite)},
		Clock:   clock.Real{},
		OnEvent: func(m ptywire.Msg) { srv.Broadcast(m) },
		Logger:  log,
	})
	srv = ptyd.NewServer(ptyd.ServerConfig{
		Secret: secret, Session: sess, Clock: clock.Real{}, Version: version,
		PID: os.Getpid(), ProtoVersion: protoVersion, Logger: log,
	})
	log.Info("mestre-pty ready", "version", version, "pid", os.Getpid())
	err = srv.Serve(context.Background(), ln)
	sess.Close()
	log.Info("mestre-pty done")
	return err
}

// signalReady tells the engine the socket is ready to be connected to. On
// Linux the engine must wait for it: a connection made before this process
// listened on the inherited socket would record the engine itself as the
// owner of the listening end, and SO_PEERCRED would show the engine's PID.
func signalReady(fd int) error {
	f := os.NewFile(uintptr(fd), "ready")
	if f == nil {
		return fmt.Errorf("ready descriptor %d is not open", fd)
	}
	defer f.Close()
	if _, err := f.Write([]byte{'R'}); err != nil {
		return fmt.Errorf("signal ready: %w", err)
	}
	return nil
}

// readSecret reads the secret the engine wrote to the inherited pipe.
func readSecret(fd int) ([]byte, error) {
	f := os.NewFile(uintptr(fd), "secret")
	if f == nil {
		return nil, fmt.Errorf("secret descriptor %d is not open", fd)
	}
	defer f.Close()
	secret := make([]byte, ptywire.SecretSize)
	if _, err := io.ReadFull(f, secret); err != nil {
		return nil, fmt.Errorf("read secret: %w", err)
	}
	return secret, nil
}
