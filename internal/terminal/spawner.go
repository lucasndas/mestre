package terminal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/lucasndas/mestre/internal/terminal/ptywire"
)

// SpawnSpec is what it takes to start the mestre-pty of a terminal.
type SpawnSpec struct {
	ID string
	// Version is the mestre-pty version chosen for this terminal; the
	// launcher spawners start the installed binary of that version, and
	// ignore Binary.
	Version string
	// Binary is the mestre-pty of that version, for the direct spawner.
	Binary string
	// SocketPath is where the terminal listens. On Linux the spawner
	// creates and binds it (mode 0600) and hands the listening descriptor
	// to the mestre-pty; on Windows the mestre-pty creates it.
	SocketPath string
	// Secret is delivered through an inherited pipe, never argv or the
	// environment.
	Secret []byte
	// LogPath receives the stderr of the mestre-pty; empty discards it.
	LogPath string
}

// Spawned is the mestre-pty that started.
type Spawned struct {
	ProcID
	// CgroupDir is the cgroup of the terminal (Linux), empty without one.
	CgroupDir string
	// LogonSID is the logon SID of the terminal (Windows), told by the
	// launcher that made the token; empty elsewhere.
	LogonSID string
}

// Spawner starts the mestre-pty and ends what it started. Who may do it, and
// with which account, is the spawner's business: the engine never starts
// processes as another account itself. The direct implementation of this
// package starts it as the current user (the full suite and development);
// the launcher services (Linux and Windows) implement the same interface
// for production, with the account of the agents and, on Linux, the
// delegated cgroup.
type Spawner interface {
	// Spawn starts the mestre-pty of spec.ID, inside the cgroup of the
	// terminal when there is one.
	Spawn(ctx context.Context, spec SpawnSpec) (Spawned, error)
	// KillCgroup ends every process of the cgroup of terminal id and
	// removes it (cgroup.kill, which also reaches whoever left the session
	// with setsid -f), waiting until it is empty. It does nothing, and
	// succeeds, when the spawner uses no cgroups.
	KillCgroup(ctx context.Context, id string) error
}

// ErrNoCgroup means this process has no delegated cgroup v2 to create
// terminal cgroups in (no systemd Delegate=yes, or LIN-11 of the checklist
// failed), or the system has none (Windows groups the tree in a Job Object).
var ErrNoCgroup = errors.New("no delegated cgroup v2")

// maxSocketPath is the room a Unix socket path has (sun_path is 108 bytes,
// 104 on some systems); a longer path would fail at bind with an obscure
// error.
const maxSocketPath = 100

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// ValidateID refuses ids that cannot name a socket file and a cgroup.
func ValidateID(id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("terminal id %q: use letters, digits, '-' and '_' (at most 64)", id)
	}
	return nil
}

// SocketPath is where terminal id listens in dir.
func SocketPath(dir, id string) (string, error) {
	if err := ValidateID(id); err != nil {
		return "", err
	}
	p := filepath.Join(dir, id+".sock")
	if len(p) > maxSocketPath {
		return "", fmt.Errorf("socket path %q is %d bytes, the limit is %d: use a shorter folder", p, len(p), maxSocketPath)
	}
	return p, nil
}

// DirectSpawner starts the mestre-pty as the user of this process.
type DirectSpawner struct {
	// Suite is the value of MESTRE_TEST_SUITE: the fast suite refuses.
	Suite  string
	System System
	// CgroupBase is a cgroup v2 directory this process may create children
	// in (Linux, delegated); empty means the mestre-pty only gets a new
	// session. Ignored on Windows.
	CgroupBase string
	// Env is the environment of the mestre-pty; nil means this process's.
	Env []string
}

var _ Spawner = (*DirectSpawner)(nil)

func (d *DirectSpawner) guard(spec SpawnSpec) error {
	if err := ptywire.GuardSuite(d.Suite); err != nil {
		return fmt.Errorf("start mestre-pty %s: %w", spec.ID, err)
	}
	return ValidateID(spec.ID)
}

func (d *DirectSpawner) env() []string {
	if d.Env != nil {
		return d.Env
	}
	return os.Environ()
}

func (d *DirectSpawner) logFile(spec SpawnSpec) (*os.File, error) {
	if spec.LogPath == "" {
		return nil, nil
	}
	f, err := os.OpenFile(spec.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open mestre-pty log: %w", err)
	}
	return f, nil
}

// SecretPipe returns the read end of a pipe that already holds secret and is
// closed for writing.
func SecretPipe(secret []byte) (*os.File, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("secret pipe: %w", err)
	}
	if _, err := w.Write(secret); err != nil {
		_ = r.Close()
		_ = w.Close()
		return nil, fmt.Errorf("write secret: %w", err)
	}
	_ = w.Close()
	return r, nil
}

func itoa(n uintptr) string { return strconv.FormatUint(uint64(n), 10) } //nolint:unused // used by the Windows build
