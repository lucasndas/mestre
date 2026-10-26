package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/mestre-ia/mestre/internal/terminal"
)

var _ ProcKiller = (*LinuxBackend)(nil)

// KillProcs ends the mestre-pty p and the sessions of its agent when there is
// no cgroup to kill (or in addition to it): the engine runs as `mestre` and
// has no CAP_KILL, so it cannot signal what `mestre-agente` runs. The
// launcher has CAP_SETUID: on a thread of its own, locked, it takes the user
// ID of the account of the agents, and a signal from that thread is the one
// of a process of the same account. Nothing is ended unless its start
// identity matches (the mestre-pty), or it is a process of that account in
// one of the sessions, started after the mestre-pty (terminal.KillTreeOf).
func (b *LinuxBackend) KillProcs(ctx context.Context, _ string, p terminal.ProcID, sessions []int) error {
	return asUser(b.agentUID(), func() error { return terminal.KillTreeOf(ctx, p, sessions, b.agentUID()) })
}

// agentUID is the user ID the mestre-pty runs as: the one of the credential,
// or the one of this process when it does not switch accounts (the full
// suite).
func (b *LinuxBackend) agentUID() int {
	if b.Credential != nil {
		return int(b.Credential.Uid)
	}
	return os.Geteuid()
}

// asUser runs f on a locked OS thread whose effective user ID is uid. The
// thread alone changes (a raw setresuid, not the Go function that changes
// every thread); the launcher keeps CAP_SETUID in its effective set, because
// the change is from one non-root user to another. If the thread cannot be
// put back, it is never unlocked: it ends with the goroutine, and no other
// code runs on it.
func asUser(uid int, f func() error) error {
	self := os.Geteuid()
	if uid == self {
		return f()
	}
	runtime.LockOSThread()
	if err := setThreadEUID(uid); err != nil {
		runtime.UnlockOSThread()
		return fmt.Errorf("take the user ID %d of the account of the agents: %w", uid, err)
	}
	err := f()
	if rerr := setThreadEUID(self); rerr != nil {
		// Leave the thread locked: it dies with this goroutine.
		return errors.Join(err, fmt.Errorf("give back the user ID of the launcher: %w", rerr))
	}
	runtime.UnlockOSThread()
	return err
}

// setThreadEUID is setresuid(-1, uid, -1) on the calling thread only.
func setThreadEUID(uid int) error {
	if _, _, errno := syscall.RawSyscall(unix.SYS_SETRESUID, ^uintptr(0), uintptr(uid), ^uintptr(0)); errno != 0 {
		return errno
	}
	return nil
}
