package terminal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// maxKillRounds bounds the rounds of killSession: each round kills what the
// previous one found plus whatever those processes forked meanwhile.
const maxKillRounds = 50

// exitWaitMs bounds the wait for one killed process to be gone. A process in
// uninterruptible sleep may take longer, and the next round reports it.
const exitWaitMs = 10_000

// treeScope limits whom killTree may signal. The zero value of a field is
// "no limit" only through uid -1 and since "".
type treeScope struct {
	// uid, when not -1, is the account of the agents: only processes whose
	// real user ID is this are signalled, whatever the PID, the identity and
	// the session say. The launcher sets it, so a wrong request can end a
	// process of the agents and nothing else.
	uid int
	// since is the start identity of the mestre-pty. A member of a session
	// that started before it cannot be of this terminal, and is left alone.
	since string
}

// killTree is SIGKILL to the mestre-pty p (after checking its identity) and
// then to every process that still has one of the sessions, repeated until
// none is left. Every signal goes through a pidfd opened before the last
// check of identity, so a PID reused in between is never hit.
func (s linuxSystem) killTree(ctx context.Context, p ProcID, sessions []int, sc treeScope) error {
	if err := s.killMestrePTY(ctx, p, &sessions, sc); err != nil {
		return err
	}
	var errs []error
	for _, sid := range sessions {
		if sid > 1 {
			if err := killSession(ctx, sid, sc); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// killMestrePTY kills p, if it is still that process, and adds the sessions of
// its children to sessions first: once it is gone they are adopted by init.
func (s linuxSystem) killMestrePTY(ctx context.Context, p ProcID, sessions *[]int, sc treeScope) error {
	pidfd, err := unix.PidfdOpen(p.PID, 0)
	if err != nil {
		if errors.Is(err, unix.ESRCH) {
			return nil
		}
		return fmt.Errorf("pidfd_open %d: %w", p.PID, err)
	}
	defer unix.Close(pidfd)
	// Identity after the pidfd: the pidfd cannot point to another process
	// than the one identified.
	if p.Start != "" {
		if start, err := s.Identify(p.PID); err != nil || start != p.Start {
			return nil
		}
	}
	if sc.uid >= 0 && procUID(p.PID) != sc.uid {
		return fmt.Errorf("process %d is not of the account of the agents: not ended", p.PID)
	}
	*sessions = append(*sessions, childSessions(p.PID)...)
	if err := unix.PidfdSendSignal(pidfd, unix.SIGKILL, nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
		return fmt.Errorf("kill mestre-pty %d: %w", p.PID, err)
	}
	return s.WaitExit(ctx, p.PID)
}

// killSession is SIGKILL to every process that still has the session,
// repeated until none is left, waiting for each to die on its pidfd.
func killSession(ctx context.Context, sid int, sc treeScope) error {
	for range maxKillRounds {
		if err := ctx.Err(); err != nil {
			return err
		}
		var pidfds []int
		var errs []error
		for _, pid := range sessionMembers(sid) {
			fd, err := signalMember(pid, sid, sc)
			if err != nil {
				errs = append(errs, err)
			}
			if fd >= 0 {
				pidfds = append(pidfds, fd)
			}
		}
		for _, fd := range pidfds {
			waitPidfd(fd)
			_ = unix.Close(fd)
		}
		if err := errors.Join(errs...); err != nil {
			return err
		}
		if len(pidfds) == 0 {
			return nil
		}
	}
	return fmt.Errorf("session %d still has processes after %d rounds", sid, maxKillRounds)
}

// signalMember kills pid if it is still a member of session sid that sc
// allows, and returns the pidfd to wait on (-1 when nothing was signalled).
func signalMember(pid, sid int, sc treeScope) (int, error) {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		if errors.Is(err, unix.ESRCH) {
			return -1, nil
		}
		return -1, fmt.Errorf("pidfd_open %d: %w", pid, err)
	}
	// The pidfd pins the process: checked now, it is the one that is hit.
	if !isMember(pid, sid, sc) {
		_ = unix.Close(fd)
		return -1, nil
	}
	if err := unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0); err != nil {
		_ = unix.Close(fd)
		if errors.Is(err, unix.ESRCH) {
			return -1, nil
		}
		return -1, fmt.Errorf("kill %d of session %d: %w", pid, sid, err)
	}
	return fd, nil
}

// waitPidfd blocks until the process behind fd is gone (it becomes readable).
func waitPidfd(fd int) {
	pfd := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for {
		if _, err := unix.Poll(pfd, exitWaitMs); !errors.Is(err, unix.EINTR) {
			return
		}
	}
}

// isMember reports whether pid is alive, in session sid, and inside the scope.
func isMember(pid, sid int, sc treeScope) bool {
	f, err := statFields(pid)
	if err != nil || len(f) < 20 || f[0][0] == 'Z' || f[0][0] == 'X' {
		return false
	}
	if s, err := strconv.Atoi(string(f[3])); err != nil || s != sid {
		return false
	}
	if sc.uid >= 0 && procUID(pid) != sc.uid {
		return false
	}
	if sc.since != "" {
		boot, err := bootID()
		if err != nil || !notBefore(sc.since, boot+":"+string(f[19])) {
			return false
		}
	}
	return true
}

// notBefore reports whether the process with start identity id started at or
// after the one with identity since (same boot, ticks compared).
func notBefore(since, id string) bool {
	sb, st, ok1 := strings.Cut(since, ":")
	ib, it, ok2 := strings.Cut(id, ":")
	if !ok1 || !ok2 || sb != ib {
		return false
	}
	a, err1 := strconv.ParseUint(st, 10, 64)
	b, err2 := strconv.ParseUint(it, 10, 64)
	return err1 == nil && err2 == nil && b >= a
}

// procUID is the real user ID of pid from /proc/<pid>/status, or -1. The
// owner of /proc/<pid> is not used: a process that is not dumpable, as the
// mestre-pty is, belongs to root there.
func procUID(pid int) int {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "Uid:"); ok {
			if f := strings.Fields(rest); len(f) > 0 {
				if uid, err := strconv.Atoi(f[0]); err == nil {
					return uid
				}
			}
			return -1
		}
	}
	return -1
}

// KillTreeOf is KillTree for the launcher: it ends the mestre-pty p and the
// processes of the sessions, but only those owned by uid (the account of the
// agents), and of the sessions only the ones that started after the
// mestre-pty. The launcher calls it from a thread that has taken the
// account's user ID, because it has no CAP_KILL (see launcher.LinuxBackend).
// p.Start is required: nothing is ended on a PID alone.
func KillTreeOf(ctx context.Context, p ProcID, sessions []int, uid int) error {
	if p.PID <= 1 || p.Start == "" {
		return errors.New("kill by identity: needs the PID and the start identity of the mestre-pty")
	}
	if uid <= 0 {
		return errors.New("kill by identity: needs the user ID of the account of the agents")
	}
	return linuxSystem{}.killTree(ctx, p, sessions, treeScope{uid: uid, since: p.Start})
}
