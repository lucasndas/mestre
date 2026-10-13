package terminal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

// Spawn starts the mestre-pty as the user of this process, detached from
// the console and from the process group of the engine. The secret travels
// in an inherited pipe whose handle value is passed in --secret-fd; no other
// handle is inherited. The mestre-pty creates the socket itself. In
// production the mestre-launcher starts it, with the token of mestre-agente,
// out of every Job Object of the engine (internal/winsvc, spike 5).
func (d *DirectSpawner) Spawn(ctx context.Context, spec SpawnSpec) (Spawned, error) {
	if err := d.guard(spec); err != nil {
		return Spawned{}, err
	}
	if err := ctx.Err(); err != nil {
		return Spawned{}, err
	}
	if err := os.MkdirAll(filepath.Dir(spec.SocketPath), 0o700); err != nil {
		return Spawned{}, fmt.Errorf("socket folder: %w", err)
	}
	_ = os.Remove(spec.SocketPath) // a stale socket of a dead terminal
	sr, err := SecretPipe(spec.Secret)
	if err != nil {
		return Spawned{}, err
	}
	defer sr.Close()
	logf, err := d.logFile(spec)
	if err != nil {
		return Spawned{}, err
	}
	if logf != nil {
		defer logf.Close()
	}
	h := syscall.Handle(sr.Fd())
	cmd := exec.Command(spec.Binary, "--id", spec.ID, "--socket", spec.SocketPath, "--secret-fd", itoa(uintptr(h)))
	cmd.Env = d.env()
	if logf != nil {
		cmd.Stderr = logf
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags:              windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
		AdditionalInheritedHandles: []syscall.Handle{h},
	}
	if err := cmd.Start(); err != nil {
		return Spawned{}, fmt.Errorf("start mestre-pty %s: %w", spec.Binary, err)
	}
	go func() { _ = cmd.Wait() }()
	pid := cmd.Process.Pid
	start, err := d.System.Identify(pid)
	if err != nil {
		return Spawned{}, fmt.Errorf("identify mestre-pty %d: %w", pid, err)
	}
	return Spawned{ProcID: ProcID{PID: pid, Start: start}}, nil
}

// KillCgroup does nothing on Windows: the Job Object of the mestre-pty ends
// with it.
func (d *DirectSpawner) KillCgroup(context.Context, string) error { return nil }
