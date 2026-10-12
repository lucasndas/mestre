//go:build linux

package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Stat is the part of /proc/<pid>/stat the identification needs.
type Stat struct {
	PID, PPID, PGID, SID int
	State                byte
	Start                uint64 // clock ticks since boot (field 22)
	Comm                 string
}

func (s Stat) String() string {
	return fmt.Sprintf("%d(%s ppid=%d sid=%d start=%d %c)", s.PID, s.Comm, s.PPID, s.SID, s.Start, s.State)
}

// Proc reads process information; the tests replace it to force races.
type Proc interface {
	Stat(pid int) (Stat, error)
}

// SysProc reads the real /proc.
type SysProc struct{}

// Stat reads /proc/<pid>/stat.
func (SysProc) Stat(pid int) (Stat, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return Stat{}, err
	}
	return ParseStat(b)
}

// ParseStat parses one /proc/<pid>/stat line. comm may hold spaces and
// parentheses, so the fixed fields start after the last ')'.
func ParseStat(b []byte) (Stat, error) {
	open, end := bytes.IndexByte(b, '('), bytes.LastIndexByte(b, ')')
	if open < 0 || end < open {
		return Stat{}, errors.New("stat: no comm")
	}
	pid, err := strconv.Atoi(string(bytes.TrimSpace(b[:open])))
	if err != nil {
		return Stat{}, err
	}
	f := strings.Fields(string(b[end+1:]))
	if len(f) < 20 { // f[0] is field 3 (state); starttime is field 22
		return Stat{}, errors.New("stat: short line")
	}
	s := Stat{PID: pid, Comm: string(b[open+1 : end]), State: f[0][0]}
	ints := []*int{&s.PPID, &s.PGID, &s.SID}
	for i, p := range ints {
		if *p, err = strconv.Atoi(f[1+i]); err != nil {
			return Stat{}, err
		}
	}
	if s.Start, err = strconv.ParseUint(f[19], 10, 64); err != nil {
		return Stat{}, err
	}
	return s, nil
}

// Errors of the identification.
var (
	ErrNotInTerminal = errors.New("not in a terminal tree")
	ErrRace          = errors.New("process tree changed during the walk")
	ErrCallerGone    = errors.New("caller exited before identification")
)

const (
	maxDepth   = 128
	maxRetries = 3
)

// Walk climbs from pid towards init and returns the first process for which
// match is true. A parent that started after its child is a reused PID (the
// real parent died during the walk): the walk restarts from pid, whose ppid
// then points to the reaper. Every Stat read is returned as the chain.
func Walk(p Proc, pid int, match func(Stat) bool) (Stat, []Stat, error) {
	var chain []Stat
	for try := 0; try < maxRetries; try++ {
		chain = chain[:0]
		cur, err := p.Stat(pid)
		if err != nil {
			return Stat{}, nil, fmt.Errorf("%w: %v", ErrCallerGone, err)
		}
		restart := false
		for depth := 0; depth < maxDepth; depth++ {
			chain = append(chain, cur)
			if match(cur) {
				return cur, chain, nil
			}
			if cur.PPID <= 1 {
				return Stat{}, chain, ErrNotInTerminal
			}
			parent, err := p.Stat(cur.PPID)
			if err != nil || parent.Start > cur.Start {
				restart = true // parent gone or its PID reused
				break
			}
			cur = parent
		}
		if !restart {
			return Stat{}, chain, ErrNotInTerminal
		}
	}
	return Stat{}, chain, ErrRace
}

// Cgroup returns the cgroup v2 path of pid ("0::<path>" in /proc/<pid>/cgroup).
func Cgroup(pid int) (string, error) {
	f, err := os.Open("/proc/" + strconv.Itoa(pid) + "/cgroup")
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if p, ok := strings.CutPrefix(sc.Text(), "0::"); ok {
			return p, nil
		}
	}
	return "", errors.New("no cgroup v2 entry")
}

// pidfdPID returns the PID a pidfd refers to, or -1 once the process has
// been reaped (the "Pid:" line of /proc/self/fdinfo/<fd>).
func pidfdPID(fd int) int {
	b, err := os.ReadFile("/proc/self/fdinfo/" + strconv.Itoa(fd))
	if err != nil {
		return -1
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "Pid:"); ok {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return -1
			}
			return n
		}
	}
	return -1
}
