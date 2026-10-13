package terminal

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"
)

// sioAFUnixGetPeerPID is SIO_AF_UNIX_GETPEERPID: _WSAIOR(IOC_VENDOR, 256).
const sioAFUnixGetPeerPID = 0x58000100

// waitSliceMs is how long WaitForSingleObject blocks before the context is
// looked at again.
const waitSliceMs = 100

// windowsSystem is the System of Windows.
type windowsSystem struct{}

// NewSystem returns the System of this operating system.
func NewSystem() System { return windowsSystem{} }

// creationTime returns the creation time of the process behind h, and
// whether it is still running.
func creationTime(h windows.Handle) (start string, running bool, err error) {
	var c, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(h, &c, &e, &k, &u); err != nil {
		return "", false, err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return "", false, err
	}
	t := uint64(c.HighDateTime)<<32 | uint64(c.LowDateTime)
	return strconv.FormatUint(t, 10), code == 259, nil // 259 is STILL_ACTIVE
}

func openProcess(access uint32, pid int) (windows.Handle, error) {
	h, err := windows.OpenProcess(access, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return 0, ErrNoProcess
		}
		return 0, fmt.Errorf("open process %d: %w", pid, err)
	}
	return h, nil
}

func (windowsSystem) Identify(pid int) (string, error) {
	h, err := openProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	start, running, err := creationTime(h)
	if err != nil {
		return "", err
	}
	if !running {
		return "", ErrNoProcess
	}
	return start, nil
}

func (windowsSystem) PeerPID(c net.Conn) (int, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("peer PID: %T is not a Unix socket", c)
	}
	rc, err := uc.SyscallConn()
	if err != nil {
		return 0, err
	}
	var pid uint32
	var ioErr error
	if err := rc.Control(func(fd uintptr) {
		var n uint32
		ioErr = windows.WSAIoctl(windows.Handle(fd), sioAFUnixGetPeerPID, nil, 0,
			(*byte)(unsafe.Pointer(&pid)), uint32(unsafe.Sizeof(pid)), &n, nil, 0)
	}); err != nil {
		return 0, err
	}
	if ioErr != nil {
		return 0, fmt.Errorf("SIO_AF_UNIX_GETPEERPID: %w", ioErr)
	}
	return int(pid), nil
}

func (windowsSystem) WaitExit(ctx context.Context, pid int) error {
	h, err := openProcess(windows.SYNCHRONIZE, pid)
	if err != nil {
		if errors.Is(err, ErrNoProcess) {
			return nil
		}
		return err
	}
	defer windows.CloseHandle(h)
	for {
		ev, err := windows.WaitForSingleObject(h, waitSliceMs)
		if err != nil {
			return fmt.Errorf("wait for %d: %w", pid, err)
		}
		if ev == windows.WAIT_OBJECT_0 {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

// KillTree ends the mestre-pty; the Job Object it holds (KILL_ON_JOB_CLOSE)
// takes the agent tree with it. The identity is checked on the very handle
// that is terminated, so a reused PID is never touched.
func (s windowsSystem) KillTree(ctx context.Context, p ProcID, _ []int) error {
	h, err := openProcess(windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, p.PID)
	if err != nil {
		if errors.Is(err, ErrNoProcess) {
			return nil
		}
		return err
	}
	defer windows.CloseHandle(h)
	start, running, err := creationTime(h)
	if err != nil {
		return err
	}
	if !running || (p.Start != "" && start != p.Start) {
		return nil
	}
	if err := windows.TerminateProcess(h, 1); err != nil {
		return fmt.Errorf("terminate mestre-pty %d: %w", p.PID, err)
	}
	return s.WaitExit(ctx, p.PID)
}
