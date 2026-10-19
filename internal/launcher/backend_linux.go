package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/lucasndas/mestre/internal/terminal"
)

// LinuxBackend starts the mestre-pty as the account of the agents, inside
// the cgroup of its terminal.
type LinuxBackend struct {
	// CgroupBase is the delegated cgroup the launcher creates term-<id> in;
	// empty when there is none (the mestre-pty then only gets a new
	// session and the terminal has no cgroup to end).
	CgroupBase string
	// Credential is the account of the agents (setuid, setgid, no
	// supplementary groups). Nil starts it as the user of this process: the
	// full suite, which has no CAP_SETUID.
	Credential *syscall.Credential
	// AllowSelf lets a nil Credential start the mestre-pty as the account of
	// the launcher; without it a nil Credential refuses every start (the
	// account of the launcher owns the vault). Only the tests set it.
	AllowSelf bool
	// Env is the environment of the mestre-pty; the engine sends the one of
	// the agent in the start message, so this one is minimal.
	Env []string
	// System tells the start identity of the process.
	System terminal.System
	// Suite is the value of MESTRE_TEST_SUITE ("" in production); in the
	// fast suite Start refuses with terminal.ErrExternalProcessInFastSuite.
	Suite string
}

var _ Backend = (*LinuxBackend)(nil)

// DefaultEnv is the environment of a mestre-pty the launcher starts.
var DefaultEnv = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8"}

// Start creates the cgroup, then the mestre-pty already inside it
// (CLONE_INTO_CGROUP), in a session of its own, with the listening socket
// on descriptor 3, the secret on 4 and the readiness pipe on 5, and waits
// for it to say the socket is its own.
func (b *LinuxBackend) Start(ctx context.Context, r StartRequest) (Started, error) {
	if err := terminal.GuardSuite(b.Suite); err != nil {
		return Started{}, err
	}
	if b.Credential == nil && !b.AllowSelf {
		return Started{}, errors.New("launcher: no credential for the agents: the mestre-pty would run as the account of the launcher")
	}
	if err := checkFiles(r); err != nil {
		return Started{}, Refused("%v", err)
	}
	var cgDir string
	var cgFile *os.File
	if b.CgroupBase != "" {
		var err error
		if cgDir, cgFile, err = terminal.CreateCgroup(b.CgroupBase, r.ID); err != nil {
			return Started{}, err
		}
		defer cgFile.Close()
	}
	undo := func() {
		if cgDir != "" {
			_ = os.Remove(cgDir)
		}
	}
	rr, rw, err := os.Pipe() // the mestre-pty says when the socket is its own
	if err != nil {
		undo()
		return Started{}, fmt.Errorf("ready pipe: %w", err)
	}
	defer rr.Close()
	defer rw.Close()

	cmd := exec.Command(r.Binary, "--id", r.ID, "--socket", r.Socket, "--listen-fd", "3", "--secret-fd", "4", "--ready-fd", "5")
	cmd.Env = b.Env
	if cmd.Env == nil {
		cmd.Env = DefaultEnv
	}
	cmd.Dir = "/"
	cmd.ExtraFiles = []*os.File{r.Files[0], r.Files[1], rw}
	if len(r.Files) == 3 {
		cmd.Stderr = r.Files[2]
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Credential: b.Credential}
	if cgFile != nil {
		cmd.SysProcAttr.UseCgroupFD, cmd.SysProcAttr.CgroupFD = true, int(cgFile.Fd())
	}
	if err := cmd.Start(); err != nil {
		undo()
		return Started{}, fmt.Errorf("start mestre-pty %s: %w", r.Binary, err)
	}
	go func() { _ = cmd.Wait() }() // reaps it; it normally outlives the launcher
	pid := cmd.Process.Pid
	_ = rw.Close()
	if err := terminal.WaitReady(ctx, rr); err != nil {
		b.reap(ctx, cmd, cgDir)
		return Started{}, fmt.Errorf("mestre-pty %d did not become ready: %w", pid, err)
	}
	start, err := b.System.Identify(pid)
	if err != nil {
		b.reap(ctx, cmd, cgDir)
		return Started{}, fmt.Errorf("identify mestre-pty %d: %w", pid, err)
	}
	return Started{PID: pid, Start: start, CgroupDir: cgDir}, nil
}

// reap ends a mestre-pty that did not make it, and its cgroup.
func (b *LinuxBackend) reap(ctx context.Context, cmd *exec.Cmd, cgDir string) {
	_ = cmd.Process.Kill()
	if cgDir != "" {
		// Not ctx: it may be the reason for being here.
		_ = terminal.KillCgroupDir(context.WithoutCancel(ctx), cgDir)
	}
}

// Kill is cgroup.kill on term-<id>; without cgroups there is nothing to do.
func (b *LinuxBackend) Kill(ctx context.Context, id string) error {
	if b.CgroupBase == "" {
		return nil
	}
	return terminal.KillCgroupDir(ctx, filepath.Join(b.CgroupBase, "term-"+id))
}

// checkFiles checks that the descriptors are what their roles say, so that
// nothing but a socket, a pipe and a regular file reaches the mestre-pty.
func checkFiles(r StartRequest) error {
	if len(r.Files) < 2 || len(r.Files) > 3 {
		return fmt.Errorf("%d descriptors", len(r.Files))
	}
	if v, err := unix.GetsockoptInt(int(r.Files[0].Fd()), unix.SOL_SOCKET, unix.SO_ACCEPTCONN); err != nil || v != 1 {
		return errors.New("the first descriptor is not a listening socket")
	}
	if fi, err := r.Files[1].Stat(); err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
		return errors.New("the second descriptor is not a pipe")
	}
	if len(r.Files) == 3 {
		if fi, err := r.Files[2].Stat(); err != nil || !fi.Mode().IsRegular() {
			return errors.New("the third descriptor is not a regular file")
		}
	}
	return nil
}
