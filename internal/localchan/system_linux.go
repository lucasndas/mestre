package localchan

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lucasndas/mestre/internal/terminal"
)

// NewSystemIdentifier returns the identifier of Linux.
func NewSystemIdentifier(cfg SystemConfig) Identifier {
	sys := cfg.System
	if sys == nil {
		sys = terminal.NewSystem()
	}
	return &ProcIdentifier{
		Peers: NewPeerSource(sys), Proc: NewProc(sys), Terminals: cfg.Terminals, Clock: cfg.Clock,
		Mode: cfg.Mode, CgroupBase: cfg.CgroupBase,
	}
}

// systemPeers reads the peer with SO_PEERCRED and SO_PEERPIDFD.
type systemPeers struct{ sys terminal.System }

// NewPeerSource returns the PeerSource of Linux.
func NewPeerSource(sys terminal.System) PeerSource { return systemPeers{sys} }

func (s systemPeers) Peer(c net.Conn) (Peer, error) {
	pid, err := s.sys.PeerPID(c)
	if err != nil {
		return Peer{}, err
	}
	p := Peer{PID: pid}
	if start, err := s.sys.Identify(pid); err == nil {
		p.Start = start
	}
	fd, err := peerPidfd(c)
	switch {
	case err == nil:
		p.Pidfd = fd
	case errors.Is(err, unix.ENOPROTOOPT):
		p.PidfdUnsupported = true
	}
	return p, nil
}

// peerPidfd asks for the pidfd of the process that connected.
func peerPidfd(c net.Conn) (Pidfd, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return nil, fmt.Errorf("%T is not a Unix socket", c)
	}
	rc, err := uc.SyscallConn()
	if err != nil {
		return nil, err
	}
	var fd int
	var sockErr error
	if err := rc.Control(func(s uintptr) {
		fd, sockErr = unix.GetsockoptInt(int(s), unix.SOL_SOCKET, unix.SO_PEERPIDFD)
	}); err != nil {
		return nil, err
	}
	if sockErr != nil {
		return nil, fmt.Errorf("SO_PEERPIDFD: %w", sockErr)
	}
	return &procPidfd{f: os.NewFile(uintptr(fd), "peer-pidfd")}, nil
}

// procPidfd is a pidfd read through /proc/self/fdinfo, which shows the PID
// the descriptor points to, or -1 once the process was reaped.
type procPidfd struct{ f *os.File }

func (p *procPidfd) PID() (int, error) {
	b, err := os.ReadFile("/proc/self/fdinfo/" + strconv.Itoa(int(p.f.Fd())))
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

func (p *procPidfd) Close() error { return p.f.Close() }

// systemProc reads the real /proc.
type systemProc struct{ sys terminal.System }

// NewProc returns the Proc of Linux, the real /proc.
func NewProc(sys terminal.System) Proc { return systemProc{sys} }

func (systemProc) Cgroup(pid int) (string, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if p, ok := strings.CutPrefix(line, "0::"); ok {
			return p, nil
		}
	}
	return "", errors.New("no cgroup v2 entry")
}

func (systemProc) Stat(pid int) (ProcStat, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ProcStat{}, err
	}
	// The command name may hold spaces and parentheses: the fields start
	// after the last ')'. f[0] is the state (field 3), f[1] the parent
	// (field 4), f[19] the start time (field 22).
	end := bytes.LastIndexByte(b, ')')
	if end < 0 {
		return ProcStat{}, fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	f := bytes.Fields(b[end+1:])
	if len(f) < 20 {
		return ProcStat{}, fmt.Errorf("short /proc/%d/stat", pid)
	}
	ppid, err := strconv.Atoi(string(f[1]))
	if err != nil {
		return ProcStat{}, fmt.Errorf("parent in /proc/%d/stat: %w", pid, err)
	}
	start, err := strconv.ParseUint(string(f[19]), 10, 64)
	if err != nil {
		return ProcStat{}, fmt.Errorf("start time in /proc/%d/stat: %w", pid, err)
	}
	return ProcStat{PPID: ppid, Start: start}, nil
}

func (s systemProc) Identify(pid int) (string, error) { return s.sys.Identify(pid) }
