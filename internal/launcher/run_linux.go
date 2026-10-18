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
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/localchan"
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
	// Agent is the account the mestre-pty runs as (mestre-agente, or
	// mestre-agente-<inst>). It is required, and it is never the account of the
	// launcher: that one owns the vault and the database
	// (docs/arquitetura/componentes.md#iniciador-mestre-launcher).
	Agent string
	// Instance is the instance the launcher serves: unless AllowSelf, Agent
	// must be the account of the agents of that instance,
	// localchan.AgentAccount(Instance), never one of another instance.
	Instance string
	// Accounts replaces the account database of the system; the tests set it.
	Accounts Accounts
	// AllowSelf lets the mestre-pty run as the user of this process: Agent may
	// be empty or be that user. Only the tests set it (the full suite starts a
	// launcher as the user who runs it); the service never does.
	AllowSelf bool
	// Engine is who may ask: its uid and the cgroup of its unit.
	Engine PeerPolicy
	// CgroupBase is the delegated cgroup term-<id> is created in; see
	// ResolveCgroupBase. Empty runs without cgroups.
	CgroupBase string
	// Env overrides DefaultEnv for the mestre-pty.
	Env []string
	// Suite is the value of MESTRE_TEST_SUITE (os.Getenv in main; "" in
	// production): in the fast suite the launcher starts nothing.
	Suite  string
	Audit  func(Event)
	Logger *slog.Logger
	Clock  clock.Clock
}

// Prepare builds the server and opens its socket (0600). Its Backend can be
// swapped by wrapping, which is what the tests do.
func Prepare(cfg RunConfig) (*Server, net.Listener, error) {
	if !cfg.AllowSelf {
		if want := localchan.AgentAccount(cfg.Instance); cfg.Agent != want {
			return nil, nil, fmt.Errorf("launcher: the agent account %q is not the one of the instance %q (%s)", cfg.Agent, cfg.Instance, want)
		}
	}
	accounts := cfg.Accounts
	if accounts.Lookup == nil {
		accounts = SystemAccounts()
	}
	cred, err := accounts.Agent(cfg.Agent, cfg.AllowSelf)
	if err != nil {
		return nil, nil, err
	}
	srv, err := NewServer(Config{
		Backend: &LinuxBackend{CgroupBase: cfg.CgroupBase, Credential: cred, AllowSelf: cfg.AllowSelf, Env: cfg.Env, System: terminal.NewSystem(), Suite: cfg.Suite},
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

// Run is the mestre-launcher on Linux: it drops the ambient and inheritable
// capabilities (it keeps CAP_SETUID and CAP_SETGID, permitted and effective,
// for itself, never for what it starts) and serves until ctx ends.
func Run(ctx context.Context, cfg RunConfig) error {
	if err := DropAmbient(); err != nil {
		return err
	}
	if err := DropInheritable(); err != nil {
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

// Accounts is how the launcher reads the accounts of the system and who it is
// itself; the tests replace it.
type Accounts struct {
	// Lookup finds an account by name (os/user.Lookup).
	Lookup func(name string) (*user.User, error)
	// UID and GID are the effective ones of this process.
	UID, GID int
}

// SystemAccounts is the account database of this system and this process.
func SystemAccounts() Accounts {
	return Accounts{Lookup: user.Lookup, UID: os.Geteuid(), GID: os.Getegid()}
}

// LookupAgent is Agent on the accounts of this system.
func LookupAgent(name string, allowSelf bool) (*syscall.Credential, error) {
	return SystemAccounts().Agent(name, allowSelf)
}

// Agent returns the credential to start the mestre-pty with: the account
// name, with no supplementary groups. It never accepts root, an account with
// the group root, or the owner of the vault and the database (hook.OwnerEngine),
// whoever the launcher runs as: a launcher that runs as root must not hand
// the agent the account of the engine either. It returns nil, meaning "do
// not switch", for an empty name or for the account of this very process, but
// only when allowSelf says so (the tests): the launcher of a service runs as
// the account that owns the vault, and a mestre-pty started as it would give
// the agent that account, so there it is an error.
func (a Accounts) Agent(name string, allowSelf bool) (*syscall.Credential, error) {
	if name == "" {
		if !allowSelf {
			return nil, errors.New("launcher: no account for the agents: the mestre-pty would run as the account of the launcher")
		}
		return nil, nil
	}
	u, err := a.Lookup(name)
	if err != nil {
		return nil, fmt.Errorf("agent account %s: %w", name, err)
	}
	uid, err1 := strconv.ParseUint(u.Uid, 10, 32)
	gid, err2 := strconv.ParseUint(u.Gid, 10, 32)
	if err := errors.Join(err1, err2); err != nil {
		return nil, fmt.Errorf("agent account %s: %w", name, err)
	}
	if uid == 0 || gid == 0 {
		return nil, fmt.Errorf("agent account %s is root or has the group root", name)
	}
	if !allowSelf {
		if int(uid) == a.UID {
			return nil, fmt.Errorf("agent account %s is the account of the launcher itself, which owns the vault", name)
		}
		owner, err := a.Lookup(hook.OwnerEngine)
		var unknown user.UnknownUserError
		switch {
		case errors.As(err, &unknown):
			// no account of the engine on this machine: nothing to protect from it
		case err != nil:
			return nil, fmt.Errorf("account of the engine %s: %w", hook.OwnerEngine, err)
		case owner.Uid == u.Uid:
			return nil, fmt.Errorf("agent account %s is the owner of the vault (%s)", name, hook.OwnerEngine)
		case owner.Gid == u.Gid:
			return nil, fmt.Errorf("agent account %s has the group of the engine (%s)", name, hook.OwnerEngine)
		}
		if int(gid) == a.GID {
			return nil, fmt.Errorf("agent account %s has the group of the launcher itself", name)
		}
	}
	if int(uid) == a.UID && int(gid) == a.GID {
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

// DropInheritable empties the inheritable capability set of every thread.
// systemd puts the capabilities it gives as ambient in the inheritable set
// too, and a child inherits it. Without file capabilities that gives the
// child nothing, but nothing the launcher starts should carry a trace of
// CAP_SETUID and CAP_SETGID, not even in a set that only a file with
// inheritable capabilities could turn into power. The permitted and effective
// sets, which the launcher needs to switch accounts in the child, stay.
func DropInheritable() error {
	hdr := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var data [2]unix.CapUserData
	if err := unix.Capget(&hdr, &data[0]); err != nil {
		return fmt.Errorf("read the capabilities: %w", err)
	}
	data[0].Inheritable, data[1].Inheritable = 0, 0
	// capset is per thread; all threads must do it.
	if _, _, errno := syscall.AllThreadsSyscall(unix.SYS_CAPSET, uintptr(unsafe.Pointer(&hdr)), uintptr(unsafe.Pointer(&data[0])), 0); errno != 0 {
		return fmt.Errorf("drop the inheritable capabilities: %w", errno)
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
