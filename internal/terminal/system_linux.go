package terminal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// linuxSystem is the System of Linux.
type linuxSystem struct{}

// NewSystem returns the System of this operating system.
func NewSystem() System { return linuxSystem{} }

// bootID is read once per call: it changes only across reboots, and a PID
// recorded before a reboot must never match a process after it.
func bootID() (string, error) {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", fmt.Errorf("read boot id: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// statFields returns the fields of /proc/<pid>/stat after the command name
// (which may hold spaces and parentheses): state, ppid, pgrp, session, ...
func statFields(pid int) ([][]byte, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoProcess
		}
		return nil, err
	}
	end := bytes.LastIndexByte(b, ')')
	if end < 0 {
		return nil, fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	return bytes.Fields(b[end+1:]), nil
}

func (linuxSystem) Identify(pid int) (string, error) {
	f, err := statFields(pid)
	if err != nil {
		return "", err
	}
	// f[0] is the state (field 3); the start time is field 22, f[19].
	if len(f) < 20 {
		return "", fmt.Errorf("short /proc/%d/stat", pid)
	}
	if f[0][0] == 'Z' || f[0][0] == 'X' {
		return "", ErrNoProcess
	}
	boot, err := bootID()
	if err != nil {
		return "", err
	}
	return boot + ":" + string(f[19]), nil
}

func (linuxSystem) PeerPID(c net.Conn) (int, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("peer PID: %T is not a Unix socket", c)
	}
	rc, err := uc.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *unix.Ucred
	var sockErr error
	if err := rc.Control(func(fd uintptr) {
		cred, sockErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if sockErr != nil {
		return 0, fmt.Errorf("SO_PEERCRED: %w", sockErr)
	}
	return int(cred.Pid), nil
}

// WaitExit waits on a pidfd, which becomes readable when the process
// ends: no polling, and it works for processes that are not children.
func (linuxSystem) WaitExit(ctx context.Context, pid int) error {
	fd, err := unix.PidfdOpen(pid, unix.PIDFD_NONBLOCK)
	if err != nil {
		if errors.Is(err, unix.ESRCH) {
			return nil
		}
		return fmt.Errorf("pidfd_open %d: %w", pid, err)
	}
	f := os.NewFile(uintptr(fd), "pidfd")
	defer f.Close()
	stop := context.AfterFunc(ctx, func() { _ = f.Close() })
	defer stop()
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	err = rc.Read(func(fd uintptr) bool {
		pfd := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, _ := unix.Poll(pfd, 0)
		return n > 0
	})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("wait for %d: %w", pid, err)
	}
	return nil
}

// KillTree ends the mestre-pty and everything of its terminal that stayed in
// the sessions: the sessions of its children are collected first, because
// once it is gone they are adopted by init.
func (s linuxSystem) KillTree(ctx context.Context, p ProcID, sessions []int) error {
	pidfd, err := unix.PidfdOpen(p.PID, 0)
	if err != nil && !errors.Is(err, unix.ESRCH) {
		return fmt.Errorf("pidfd_open %d: %w", p.PID, err)
	}
	if err == nil {
		defer unix.Close(pidfd)
		// Identity after the pidfd: the pidfd cannot point to another
		// process than the one identified.
		if p.Start != "" {
			if start, ierr := s.Identify(p.PID); ierr != nil || start != p.Start {
				pidfd = -1
			}
		}
		if pidfd >= 0 {
			sessions = append(sessions, childSessions(p.PID)...)
			if err := unix.PidfdSendSignal(pidfd, unix.SIGKILL, nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
				return fmt.Errorf("kill mestre-pty %d: %w", p.PID, err)
			}
			if err := s.WaitExit(ctx, p.PID); err != nil {
				return err
			}
		}
	}
	var errs []error
	for _, sid := range sessions {
		if sid > 1 {
			if err := killSession(sid); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// childSessions returns the session IDs of the direct children of pid.
func childSessions(pid int) []int {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range ents {
		c, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		f, err := statFields(c)
		if err != nil || len(f) < 4 {
			continue
		}
		if ppid, _ := strconv.Atoi(string(f[1])); ppid == pid {
			if sid, err := strconv.Atoi(string(f[3])); err == nil {
				out = append(out, sid)
			}
		}
	}
	return out
}

// killSession is SIGKILL to every process that still has the session,
// repeated until none is left, waiting for each to die.
func killSession(sid int) error {
	const maxSweeps = 50
	for range maxSweeps {
		members := sessionMembers(sid)
		if len(members) == 0 {
			return nil
		}
		for _, pid := range members {
			if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				return fmt.Errorf("kill %d of session %d: %w", pid, sid, err)
			}
		}
		for _, pid := range members {
			if err := (linuxSystem{}).WaitExit(context.Background(), pid); err != nil {
				return err
			}
		}
	}
	return fmt.Errorf("session %d still has processes after %d rounds", sid, maxSweeps)
}

// sessionMembers returns the PIDs of the live processes of session sid.
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
		f, err := statFields(pid)
		if err != nil || len(f) < 4 || f[0][0] == 'Z' || f[0][0] == 'X' {
			continue
		}
		if s, err := strconv.Atoi(string(f[3])); err == nil && s == sid {
			out = append(out, pid)
		}
	}
	return out
}
