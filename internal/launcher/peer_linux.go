package launcher

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/mestre-ia/mestre/internal/terminal"
)

// PeerPolicy accepts only the engine of the instance: the account (uid) it
// runs as, read by the kernel with SO_PEERCRED, and the cgroup of its
// process (the unit mestre@<inst>), read from /proc. The tests configure
// the uid and the cgroup of the test process.
type PeerPolicy struct {
	UID uint32
	// Cgroup is the cgroup v2 path of the engine, as in /proc/<pid>/cgroup
	// ("/system.slice/mestre@principal.service"); a process in a cgroup
	// below it is accepted too.
	Cgroup string
}

// Check is the PeerCheck of the Linux launcher.
func (p PeerPolicy) Check(c net.Conn) (string, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return "?", fmt.Errorf("%T is not a Unix socket", c)
	}
	rc, err := uc.SyscallConn()
	if err != nil {
		return "?", err
	}
	var cred *unix.Ucred
	var sockErr, pidfdErr error
	pidfd := -1
	if err := rc.Control(func(fd uintptr) {
		cred, sockErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if sockErr == nil {
			// Right after the credentials, before anything is read from /proc.
			var n int
			if n, pidfdErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_PEERPIDFD); pidfdErr == nil {
				pidfd = n
			}
		}
	}); err != nil {
		return "?", err
	}
	if sockErr != nil {
		return "?", fmt.Errorf("SO_PEERCRED: %w", sockErr)
	}
	if pidfd >= 0 {
		defer unix.Close(pidfd)
	}
	who := fmt.Sprintf("pid=%d uid=%d", cred.Pid, cred.Uid)
	if pidfdErr != nil && !errors.Is(pidfdErr, unix.ENOPROTOOPT) {
		return who, fmt.Errorf("the peer was already gone when the connection was accepted (SO_PEERPIDFD: %w)", pidfdErr)
	}
	if cred.Uid != p.UID {
		return who, fmt.Errorf("uid %d is not the account of the engine (%d)", cred.Uid, p.UID)
	}
	if p.Cgroup == "" {
		return who, errors.New("no cgroup configured for the engine")
	}
	cg, err := terminal.ProcCgroup(int(cred.Pid))
	if err != nil {
		return who, fmt.Errorf("cgroup of process %d: %w", cred.Pid, err)
	}
	who += " cgroup=" + cg
	if cg != p.Cgroup && !strings.HasPrefix(cg, strings.TrimSuffix(p.Cgroup, "/")+"/") {
		return who, fmt.Errorf("cgroup %s is not the one of the engine (%s)", cg, p.Cgroup)
	}
	// Last: the pidfd must still point to the PID whose cgroup was read, so
	// the process was not reaped, and its PID not reused, meanwhile. A kernel
	// older than 6.5 has no SO_PEERPIDFD and the uid and cgroup are all there is.
	if pidfd >= 0 {
		got, err := pidfdPID(pidfd)
		if err != nil {
			return who, fmt.Errorf("the peer was reaped while it was checked: %w", err)
		}
		if got != int(cred.Pid) {
			return who, fmt.Errorf("the pidfd of the peer points to PID %d, not %d", got, cred.Pid)
		}
	}
	return who, nil
}

// pidfdPID is the PID a pidfd points to, from /proc/self/fdinfo, which shows
// -1 once the process was reaped.
func pidfdPID(fd int) (int, error) {
	b, err := os.ReadFile("/proc/self/fdinfo/" + strconv.Itoa(fd))
	if err != nil {
		return 0, fmt.Errorf("read fdinfo of the pidfd: %w", err)
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "Pid:"); ok {
			pid, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil || pid <= 0 {
				return 0, errors.New("the process was reaped")
			}
			return pid, nil
		}
	}
	return 0, errors.New("the pidfd shows no PID")
}
