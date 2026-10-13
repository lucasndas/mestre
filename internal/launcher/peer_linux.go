package launcher

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lucasndas/mestre/internal/terminal"
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
	var sockErr error
	if err := rc.Control(func(fd uintptr) {
		cred, sockErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return "?", err
	}
	if sockErr != nil {
		return "?", fmt.Errorf("SO_PEERCRED: %w", sockErr)
	}
	who := fmt.Sprintf("pid=%d uid=%d", cred.Pid, cred.Uid)
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
	return who, nil
}
