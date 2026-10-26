package terminal

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/mestre-ia/mestre/internal/privdir"
)

// Spawn creates the socket (mode 0600, folder 0700), writes the secret to a
// pipe and starts the mestre-pty in a new session, with the listening
// descriptor on fd 3 and the secret pipe on fd 4. With CgroupBase it is born
// inside the cgroup term-<id> (CLONE_INTO_CGROUP), so neither it nor any
// child ever runs outside it.
func (d *DirectSpawner) Spawn(ctx context.Context, spec SpawnSpec) (Spawned, error) {
	if err := d.guard(spec); err != nil {
		return Spawned{}, err
	}
	if err := ctx.Err(); err != nil {
		return Spawned{}, err
	}
	var cgDir string
	var cgFile *os.File
	if d.CgroupBase != "" {
		var err error
		if cgDir, cgFile, err = CreateCgroup(d.CgroupBase, spec.ID); err != nil {
			return Spawned{}, err
		}
		defer cgFile.Close()
	}
	fail := func(err error) (Spawned, error) {
		if cgDir != "" {
			_ = os.Remove(cgDir)
		}
		return Spawned{}, err
	}

	lf, err := ListenSocket(spec.SocketPath)
	if err != nil {
		return fail(err)
	}
	defer lf.Close()
	sr, err := SecretPipe(spec.Secret)
	if err != nil {
		return fail(err)
	}
	defer sr.Close()
	rr, rw, err := os.Pipe() // the mestre-pty says when the socket is its own
	if err != nil {
		return fail(fmt.Errorf("ready pipe: %w", err))
	}
	defer rr.Close()
	defer rw.Close()
	logf, err := d.logFile(spec)
	if err != nil {
		return fail(err)
	}
	if logf != nil {
		defer logf.Close()
	}

	cmd := exec.Command(spec.Binary, "--id", spec.ID, "--socket", spec.SocketPath, "--listen-fd", "3", "--secret-fd", "4", "--ready-fd", "5")
	cmd.Env = d.env()
	cmd.ExtraFiles = []*os.File{lf, sr, rw}
	if logf != nil {
		cmd.Stderr = logf
	}
	// A session of its own: it must outlive this process and ignore the
	// signals sent to the terminal this one runs in.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if cgFile != nil {
		cmd.SysProcAttr.UseCgroupFD, cmd.SysProcAttr.CgroupFD = true, int(cgFile.Fd())
	}
	if err := cmd.Start(); err != nil {
		_ = os.Remove(spec.SocketPath)
		return fail(fmt.Errorf("start mestre-pty %s: %w", spec.Binary, err))
	}
	go func() { _ = cmd.Wait() }() // reaps it; it normally outlives us
	pid := cmd.Process.Pid
	_ = rw.Close()
	if err := WaitReady(ctx, rr); err != nil {
		_ = os.Remove(spec.SocketPath)
		return fail(fmt.Errorf("mestre-pty %d did not become ready: %w", pid, err))
	}
	start, err := d.System.Identify(pid)
	if err != nil {
		return Spawned{}, fmt.Errorf("identify mestre-pty %d: %w", pid, err)
	}
	return Spawned{ProcID: ProcID{PID: pid, Start: start}, CgroupDir: cgDir}, nil
}

// WaitReady waits for the byte the mestre-pty writes once it listens on the
// inherited socket (see signalReady in cmd/mestre-pty). It ends early if
// the process dies (end of file) or ctx ends.
func WaitReady(ctx context.Context, r *os.File) error {
	done := make(chan error, 1)
	go func() {
		var b [1]byte
		_, err := r.Read(b[:])
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = r.Close()
		return ctx.Err()
	}
}

// ListenSocket binds the socket and returns a file for the listening
// descriptor; the socket file stays when this process lets go of it.
func ListenSocket(path string) (*os.File, error) {
	if err := privdir.Ensure(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("socket folder: %w", err)
	}
	_ = os.Remove(path) // a stale socket of a dead terminal
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}
	ul := ln.(*net.UnixListener)
	ul.SetUnlinkOnClose(false)
	defer ul.Close()
	if err := os.Chmod(path, 0o600); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("chmod %s: %w", path, err)
	}
	f, err := ul.File()
	if err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("listening descriptor: %w", err)
	}
	return f, nil
}

// KillCgroup is cgroup.kill on term-<id>.
func (d *DirectSpawner) KillCgroup(ctx context.Context, id string) error {
	if d.CgroupBase == "" {
		return nil
	}
	if err := ValidateID(id); err != nil {
		return err
	}
	return KillCgroupDir(ctx, filepath.Join(d.CgroupBase, "term-"+id))
}
