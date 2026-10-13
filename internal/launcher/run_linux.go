package launcher

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/pkg/clock"
)

// RunConfig is what the Linux launcher of an instance needs; the service
// reads it from the configuration of the instance.
type RunConfig struct {
	// SocketPath is where it listens (/run/mestre/<inst>/launcher.sock).
	SocketPath string
	// BinDir is the folder of installed binaries, <version>/mestre-pty.
	BinDir string
	// SocketDir is the folder of the sockets of the terminals
	// (/run/mestre/<inst>/pty).
	SocketDir string
	// Agent is the account the mestre-pty runs as (mestre-agente).
	// Empty means the user of this process, for the full suite.
	Agent string
	// Engine is who may ask: its uid and the cgroup of its unit.
	Engine PeerPolicy
	// CgroupBase is the delegated cgroup term-<id> is created in; see
	// ResolveCgroupBase. Empty runs without cgroups.
	CgroupBase string
	// Env overrides DefaultEnv for the mestre-pty.
	Env    []string
	Audit  func(Event)
	Logger *slog.Logger
	Clock  clock.Clock
}

// Prepare builds the server and opens its socket (0600). Its Backend can be
// swapped by wrapping, which is what the tests do.
func Prepare(cfg RunConfig) (*Server, net.Listener, error) {
	cred, err := LookupAgent(cfg.Agent)
	if err != nil {
		return nil, nil, err
	}
	srv, err := NewServer(Config{
		Backend: &LinuxBackend{CgroupBase: cfg.CgroupBase, Credential: cred, Env: cfg.Env, System: terminal.NewSystem()},
		Check:   cfg.Engine.Check,
		BinDir:  cfg.BinDir, SocketDir: cfg.SocketDir,
		Audit: cfg.Audit, Logger: cfg.Logger, Clock: cfg.Clock,
	})
	if err != nil {
		return nil, nil, err
	}
	ln, err := ListenUnix(cfg.SocketPath)
	if err != nil {
		return nil, nil, err
	}
	return srv, ln, nil
}

// Run is the mestre-launcher on Linux: it drops the ambient capabilities
// (it keeps CAP_SETUID and CAP_SETGID for itself, never for what it
// starts) and serves until ctx ends.
func Run(ctx context.Context, cfg RunConfig) error {
	if err := DropAmbient(); err != nil {
		return err
	}
	srv, ln, err := Prepare(cfg)
	if err != nil {
		return err
	}
	return srv.Serve(ctx, ln)
}

// ListenUnix binds the socket with mode 0600.
func ListenUnix(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("folder of the launcher socket: %w", err)
	}
	_ = os.Remove(path) // a socket of the previous launcher
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("chmod %s: %w", path, err)
	}
	return ln, nil
}

// LookupAgent returns the credential to start the mestre-pty with: the
// account name, with no supplementary groups. It returns nil, meaning "do
// not switch", for an empty name or for the account of this very process.
func LookupAgent(name string) (*syscall.Credential, error) {
	if name == "" {
		return nil, nil
	}
	u, err := user.Lookup(name)
	if err != nil {
		return nil, fmt.Errorf("agent account %s: %w", name, err)
	}
	uid, err1 := strconv.ParseUint(u.Uid, 10, 32)
	gid, err2 := strconv.ParseUint(u.Gid, 10, 32)
	if err := errors.Join(err1, err2); err != nil {
		return nil, fmt.Errorf("agent account %s: %w", name, err)
	}
	if uid == 0 {
		return nil, fmt.Errorf("agent account %s is root", name)
	}
	if int(uid) == os.Geteuid() && int(gid) == os.Getegid() {
		return nil, nil
	}
	return &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}, nil
}

// DropAmbient empties the ambient capability set of every thread. systemd
// gives CAP_SETUID and CAP_SETGID to the service as ambient, and an ambient
// capability survives the exec of the mestre-pty: dropping it keeps them
// for the launcher alone (permitted and effective), which needs them only
// to switch accounts in the child before the exec.
func DropAmbient() error {
	// PR_CAP_AMBIENT is per thread; all threads must do it.
	if _, _, errno := syscall.AllThreadsSyscall6(unix.SYS_PRCTL, unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0, 0); errno != 0 {
		return fmt.Errorf("drop the ambient capabilities: %w", errno)
	}
	return nil
}

const cgroupRoot = "/sys/fs/cgroup"

// ResolveCgroupBase returns the cgroup the launcher creates the cgroups of
// the terminals in: the one systemd delegated to its unit (Delegate=yes).
// With DelegateSubgroup=launcher (systemd 254 or later) the process is
// already in <unit>/launcher and the base is the unit; otherwise it moves
// itself into <unit>/launcher, because cgroup v2 keeps processes only in
// the leaves.
func ResolveCgroupBase() (string, error) {
	rel, err := terminal.ProcCgroup(os.Getpid())
	if err != nil {
		return "", fmt.Errorf("%w: %v", terminal.ErrNoCgroup, err)
	}
	var base string
	if filepath.Base(rel) == "launcher" {
		base = filepath.Join(cgroupRoot, filepath.Dir(rel))
	} else {
		base = filepath.Join(cgroupRoot, rel)
		leaf := filepath.Join(base, "launcher")
		if err := os.Mkdir(leaf, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("%w: %v", terminal.ErrNoCgroup, err)
		}
		if err := os.WriteFile(filepath.Join(leaf, "cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
			return "", fmt.Errorf("%w: move into %s: %v", terminal.ErrNoCgroup, leaf, err)
		}
	}
	probe, err := os.MkdirTemp(base, "probe-")
	if err != nil {
		return "", fmt.Errorf("%w: %v", terminal.ErrNoCgroup, err)
	}
	if err := os.Remove(probe); err != nil {
		return "", fmt.Errorf("%w: %v", terminal.ErrNoCgroup, err)
	}
	return base, nil
}
