package ptyd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"syscall"

	"github.com/aymanbagabas/go-pty"
	"golang.org/x/sys/unix"
)

// maxSweeps bounds the rounds of killSession: each round kills what the
// previous one found plus whatever those processes forked meanwhile.
const maxSweeps = 50

// sysAgent is the Linux part of an agent: the agent is the leader of a new
// session (go-pty does the setsid), so its PID is also the session and the
// process group ID, and the child dies with the mestre-pty
// (PR_SET_PDEATHSIG). With a cgroup the engine kills the tree through
// cgroup.kill; without one, the session is all that groups it
// (docs/arquitetura/terminal.md#criação).
type sysAgent struct{}

// prepare sets how the child is born.
func prepare(cmd *pty.Cmd, _ bool) (*sysAgent, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	return &sysAgent{}, nil
}

func (*sysAgent) abort() {}

// started closes the slave end held by this process: the child holds its
// own copy, and the master only sees the end of the output (EIO) once every
// process that has the terminal is gone.
func (*sysAgent) started(_ *pty.Cmd, p pty.Pty) error {
	if u, ok := p.(pty.UnixPty); ok {
		_ = u.Slave().Close()
	}
	return nil
}

func (*sysAgent) release() {}

// signal is SIGTERM to the process group of the session.
func (*sysAgent) signal(a *ptyAgent) error {
	if err := syscall.Kill(-a.PID(), syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("terminate session %d: %w", a.PID(), err)
	}
	return nil
}

// kill is SIGKILL to the group and to every process that still has the
// session, repeated until none is left. It cannot reach a process that left
// the session (setsid -f): only the cgroup does.
func (*sysAgent) kill(a *ptyAgent) error {
	_ = syscall.Kill(-a.PID(), syscall.SIGKILL)
	return killSession(a.PID())
}

// inTree: there is no Job Object on Linux; the engine identifies a caller
// by its cgroup.
func (*sysAgent) inTree(int, string) (bool, error) { return false, ErrNoJob }

// endOfOutput turns the EIO the master returns when the last holder of the
// slave is gone into io.EOF.
func endOfOutput(err error) error {
	if errors.Is(err, syscall.EIO) || errors.Is(err, fs.ErrClosed) {
		return io.EOF
	}
	return err
}

func exitInfo(ps *os.ProcessState) ExitInfo {
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return ExitInfo{Code: 128 + int(ws.Signal()), Signal: unix.SignalName(ws.Signal())}
	}
	return ExitInfo{Code: ps.ExitCode()}
}

// sessionMembers returns the PIDs of the live (non-zombie) processes whose
// session ID is sid, read from /proc.
func sessionMembers(sid int) []int {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue // exited meanwhile
		}
		state, s, ok := parseStatSession(b)
		if ok && s == sid && state != 'Z' && state != 'X' {
			out = append(out, pid)
		}
	}
	return out
}

// parseStatSession reads the state and session ID from a /proc/<pid>/stat
// line. comm may hold spaces and parentheses, so the fields start after the
// last ')'.
func parseStatSession(b []byte) (state byte, sid int, ok bool) {
	end := bytes.LastIndexByte(b, ')')
	if end < 0 {
		return 0, 0, false
	}
	f := bytes.Fields(b[end+1:])
	if len(f) < 4 || len(f[0]) == 0 { // state, ppid, pgrp, session
		return 0, 0, false
	}
	sid, err := strconv.Atoi(string(f[3]))
	if err != nil {
		return 0, 0, false
	}
	return f[0][0], sid, true
}

// killSession is SIGKILL to every process that still has the session,
// repeated until none is left, waiting for each to die (pidfd, no polling).
func killSession(sid int) error {
	for range maxSweeps {
		members := sessionMembers(sid)
		if len(members) == 0 {
			return nil
		}
		var pidfds []int
		for _, pid := range members {
			if fd, err := unix.PidfdOpen(pid, 0); err == nil {
				pidfds = append(pidfds, fd)
			}
			if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				closeAll(pidfds)
				return fmt.Errorf("kill %d of session %d: %w", pid, sid, err)
			}
		}
		for _, fd := range pidfds {
			pfd := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			for {
				if _, err := unix.Poll(pfd, -1); !errors.Is(err, unix.EINTR) {
					break
				}
			}
		}
		closeAll(pidfds)
	}
	return fmt.Errorf("session %d still has processes after %d rounds", sid, maxSweeps)
}

func closeAll(fds []int) {
	for _, fd := range fds {
		_ = unix.Close(fd)
	}
}
