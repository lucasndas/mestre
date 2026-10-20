//go:build linux

package leakcheck

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

type entry struct {
	pid, ppid int
	cmd       string
}

// table reads /proc: the live processes of the user, other than this one (a
// zombie has no command line and is not live).
func table() []entry {
	dirs, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	uid, self := uint32(os.Getuid()), os.Getpid()
	var out []entry
	for _, d := range dirs {
		pid, err := strconv.Atoi(d.Name())
		if err != nil || pid == self {
			continue
		}
		if fi, err := d.Info(); err != nil {
			continue
		} else if st, ok := fi.Sys().(*syscall.Stat_t); !ok || st.Uid != uid {
			continue
		}
		raw, err := os.ReadFile("/proc/" + d.Name() + "/cmdline")
		if err != nil || len(raw) == 0 {
			continue
		}
		stat, err := os.ReadFile("/proc/" + d.Name() + "/stat")
		if err != nil {
			continue
		}
		// pid (comm) state ppid ...: comm may hold spaces and parentheses.
		s := string(stat)
		i := strings.LastIndexByte(s, ')')
		if i < 0 {
			continue
		}
		f := strings.Fields(s[i+1:])
		if len(f) < 2 || f[0] == "Z" || f[0] == "X" {
			continue
		}
		ppid, _ := strconv.Atoi(f[1])
		out = append(out, entry{pid, ppid, strings.TrimSpace(strings.ReplaceAll(string(raw), "\x00", " "))})
	}
	return out
}

func find(needles []string) []Proc {
	var ns []string
	for _, n := range needles {
		if n != "" {
			ns = append(ns, strings.TrimRight(n, "/")+"/")
		}
	}
	if len(ns) == 0 {
		return nil
	}
	all := table()
	children := map[int][]entry{}
	for _, e := range all {
		children[e.ppid] = append(children[e.ppid], e)
	}
	seen := map[int]bool{}
	var out []Proc
	var add func(e entry)
	add = func(e entry) {
		if seen[e.pid] {
			return
		}
		seen[e.pid] = true
		out = append(out, Proc{PID: e.pid, PPID: e.ppid, Cmdline: e.cmd})
		for _, c := range children[e.pid] {
			add(c)
		}
	}
	for _, e := range all {
		for _, n := range ns {
			if strings.Contains(e.cmd, n) {
				add(e)
				break
			}
		}
	}
	return out
}

func kill(ps []Proc) {
	for _, p := range ps {
		_ = syscall.Kill(p.PID, syscall.SIGKILL)
	}
}

// alive returns the processes of ps still in the table.
func alive(ps []Proc) []Proc {
	now := map[int]string{}
	for _, e := range table() {
		now[e.pid] = e.cmd
	}
	var out []Proc
	for _, p := range ps {
		if cmd, ok := now[p.PID]; ok && cmd == p.Cmdline {
			out = append(out, p)
		}
	}
	return out
}
