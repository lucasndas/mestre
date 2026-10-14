//go:build linux

package main

import (
	"bytes"
	"errors"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// orphanSweep ends the descendants of a hook that left its process group
// (`setsid cmd &`) and so escaped the group kill. The dispatcher marks itself
// the subreaper of its descendants (PR_SET_CHILD_SUBREAPER): a process whose
// parent died is adopted by it, not by init, so when the hook ends every
// descendant is still under the dispatcher in /proc, whatever its group or
// session, and the sweep walks the tree from the dispatcher and kills what
// was not there before the hook started.
//
// What still escapes is a process that something outside the tree starts on
// behalf of the hook (systemd-run, a container runtime, at, cron): it was
// never a descendant (docs/provedores/despachante-de-hooks.md#uma-chamada).
// `mestre hook` is a process of one call, so being a subreaper costs nothing.
type orphanSweep struct{ before map[int]string }

func newOrphanSweep() *orphanSweep {
	_ = unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0)
	s := &orphanSweep{before: map[int]string{}}
	for pid, p := range procTree(os.Getpid()) {
		s.before[pid] = p.start
	}
	return s
}

// kill ends every descendant that is new since the sweep was made, until
// none is left (a killed process leaves its children to the dispatcher,
// which the next pass finds).
func (s *orphanSweep) kill() {
	for range 16 {
		killed := 0
		for pid, p := range procTree(os.Getpid()) {
			if start, old := s.before[pid]; (old && start == p.start) || p.state == 'Z' {
				continue
			}
			killPID(pid, p.start)
			killed++
		}
		if killed == 0 {
			return
		}
	}
}

// killPID sends SIGKILL through a pidfd, after checking that the PID still
// is the process (by its start time) that was found in the tree, so a PID
// reused in the meantime is never hit; without pidfd_open (before Linux 5.3)
// it falls back to the PID.
func killPID(pid int, start string) {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		if errors.Is(err, unix.ENOSYS) {
			_ = unix.Kill(pid, unix.SIGKILL)
		}
		return
	}
	defer unix.Close(fd)
	if p, ok := readStat(pid); !ok || p.start != start {
		return
	}
	_ = unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0)
}

// procStat is what the sweep reads of /proc/<pid>/stat.
type procStat struct {
	ppid  int
	state byte
	start string
}

func readStat(pid int) (procStat, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return procStat{}, false
	}
	// The command may hold spaces and parentheses: the fields start after
	// the last ')'; f[0] is the state (field 3), f[1] the parent (4) and
	// f[19] the start time (22).
	end := bytes.LastIndexByte(b, ')')
	if end < 0 {
		return procStat{}, false
	}
	f := bytes.Fields(b[end+1:])
	if len(f) < 20 || len(f[0]) == 0 {
		return procStat{}, false
	}
	ppid, err := strconv.Atoi(string(f[1]))
	if err != nil {
		return procStat{}, false
	}
	return procStat{ppid: ppid, state: f[0][0], start: string(f[19])}, true
}

// procTree is the processes under root (not root), by PID.
func procTree(root int) map[int]procStat {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	all := map[int]procStat{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if st, ok := readStat(pid); ok {
			all[pid] = st
		}
	}
	out := map[int]procStat{}
	for pid, st := range all {
		for p, n := pid, 0; n < 4096; n++ {
			cur, ok := all[p]
			if !ok || cur.ppid <= 1 {
				break
			}
			if cur.ppid == root {
				out[pid] = st
				break
			}
			p = cur.ppid
		}
	}
	return out
}
