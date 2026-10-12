//go:build linux

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// Mode is how the engine decides which terminal a caller belongs to.
type Mode int

// The modes compared by the spike.
const (
	// ModeTree is the default of seguranca.md: climb the tree until the
	// root process of a registered terminal.
	ModeTree Mode = iota
	// ModeTreeSession also accepts a process of the chain whose session ID
	// is the terminal's (the terminal root is a session leader, setsid).
	ModeTreeSession
	// ModeSubreaper is ModeTree with PR_SET_CHILD_SUBREAPER on the terminal
	// root, so orphans are reparented to it instead of to init.
	ModeSubreaper
	// ModeCgroup starts each terminal in its own cgroup (CLONE_INTO_CGROUP)
	// and reads /proc/<pid>/cgroup.
	ModeCgroup
)

func (m Mode) String() string {
	return [...]string{"tree", "tree+sid", "subreaper", "cgroup"}[m]
}

// soPeerPidfd is SO_PEERPIDFD (Linux 6.5+; same value on all architectures).
const soPeerPidfd = 77

// Terminal is a registered terminal: its root process and owner.
type Terminal struct {
	ID, AgentID string
	PID         int
	Start       uint64
	Cgroup      string // cgroup v2 path, ModeCgroup only
}

// Result is the decision about one connection.
type Result struct {
	Label      string
	Identified bool
	Terminal   string
	Reason     string
	PeerPID    int
	PidfdErr   error
	Chain      []Stat
}

type request struct {
	AgentID string `json:"agent_id"`
	Label   string `json:"label"`
}

type response struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

// Engine is a minimal local channel: accept, identify, answer.
type Engine struct {
	Mode           Mode
	Proc           Proc
	Sock           string
	BeforeIdentify func() // test hook, between accept and identification
	Results        chan Result

	ln    *net.UnixListener
	mu    sync.Mutex
	terms []Terminal
}

// NewEngine listens on sock.
func NewEngine(mode Mode, sock string) (*Engine, error) {
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		return nil, err
	}
	e := &Engine{Mode: mode, Proc: SysProc{}, Sock: sock, Results: make(chan Result, 16), ln: ln}
	go e.serve()
	return e, nil
}

// Close stops accepting.
func (e *Engine) Close() { e.ln.Close() }

func (e *Engine) serve() {
	for {
		c, err := e.ln.AcceptUnix()
		if err != nil {
			return
		}
		go e.handle(c)
	}
}

// peer reads SO_PEERCRED and SO_PEERPIDFD.
func peer(c *net.UnixConn) (*syscall.Ucred, int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return nil, -1, err
	}
	var cred *syscall.Ucred
	var cerr, perr error
	pidfd := -1
	err = raw.Control(func(fd uintptr) {
		cred, cerr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		pidfd, perr = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, soPeerPidfd)
	})
	if err == nil {
		err = cerr
	}
	if err != nil {
		return nil, -1, err
	}
	if perr != nil {
		return cred, -1, perr
	}
	return cred, pidfd, nil
}

func (e *Engine) handle(c *net.UnixConn) {
	defer c.Close()
	res := Result{}
	cred, pidfd, err := peer(c)
	if cred == nil {
		res.Reason = "peer: " + err.Error()
	} else {
		res.PeerPID, res.PidfdErr = int(cred.Pid), err
	}
	if e.BeforeIdentify != nil {
		e.BeforeIdentify()
	}
	var term Terminal
	var idErr error
	if cred != nil {
		term, res.Chain, idErr = e.Identify(int(cred.Pid))
		// The /proc reads are about the caller only if it was not reaped
		// meanwhile (its PID could have been reused): check the pidfd.
		if pidfd >= 0 {
			if idErr == nil && pidfdPID(pidfd) != int(cred.Pid) {
				idErr = ErrCallerGone
			}
			syscall.Close(pidfd)
		}
	}
	// The real engine identifies before reading; the spike reads anyway to
	// learn the scenario label.
	line, rerr := bufio.NewReader(c).ReadBytes('\n')
	var req request
	if rerr == nil {
		rerr = json.Unmarshal(line, &req)
	}
	res.Label = req.Label
	switch {
	case res.Reason != "":
	case idErr != nil:
		res.Reason = idErr.Error()
	case rerr != nil:
		res.Reason = "request: " + rerr.Error()
	case req.AgentID != term.AgentID:
		res.Reason = fmt.Sprintf("agent_id %q is not the owner of terminal %s (%q)", req.AgentID, term.ID, term.AgentID)
	default:
		res.Identified, res.Terminal = true, term.ID
	}
	b, _ := json.Marshal(response{OK: res.Identified, Reason: res.Reason})
	_, _ = c.Write(append(b, '\n'))
	e.Results <- res
}

// Identify finds the terminal of pid according to the mode.
func (e *Engine) Identify(pid int) (Terminal, []Stat, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Mode == ModeCgroup {
		cg, err := Cgroup(pid)
		if err != nil {
			return Terminal{}, nil, fmt.Errorf("%w: %v", ErrCallerGone, err)
		}
		for _, t := range e.terms {
			if cg == t.Cgroup || strings.HasPrefix(cg, t.Cgroup+"/") {
				return t, nil, nil
			}
		}
		return Terminal{}, nil, fmt.Errorf("%w: cgroup %s", ErrNotInTerminal, cg)
	}
	var found Terminal
	_, chain, err := Walk(e.Proc, pid, func(s Stat) bool {
		for _, t := range e.terms {
			root := s.PID == t.PID && s.Start == t.Start
			session := e.Mode == ModeTreeSession && s.SID == t.PID
			if root || session {
				found = t
				return true
			}
		}
		return false
	})
	return found, chain, err
}

// Spec is a terminal to start.
type Spec struct {
	ID, AgentID, Script, Dir string
	CgroupBase               string // absolute cgroup v2 directory, ModeCgroup only
}

// Running is a started terminal.
type Running struct {
	Terminal
	cmd    *exec.Cmd
	stdin  interface{ Close() error }
	cgDir  string
	waited chan error
}

// Launch starts the terminal as the real engine would: new session, the
// root registered before anything can call back (under e.mu). The script
// ends by reading stdin, so the root lives until Stop.
func (e *Engine) Launch(s Spec) (*Running, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	script := "export SPIKE_ROOT=$$\n" + s.Script + "\nread -r _\n"
	cmd := exec.Command("/bin/bash", "-c", script)
	if e.Mode == ModeSubreaper {
		cmd = exec.Command(self, "spike-shim", script)
	}
	cmd.Dir = s.Dir
	cmd.Env = append(cleanEnv(),
		"SPIKE_BIN="+self, "SPIKE_SOCK="+e.Sock, "SPIKE_DIR="+s.Dir, "MESTRE_AGENT_ID="+s.AgentID)
	logf, err := os.Create(filepath.Join(s.Dir, s.ID+".log"))
	if err != nil {
		return nil, err
	}
	defer logf.Close()
	cmd.Stdout, cmd.Stderr = logf, logf
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	r := &Running{cmd: cmd, stdin: stdin, waited: make(chan error, 1)}
	if e.Mode == ModeCgroup {
		r.cgDir = filepath.Join(s.CgroupBase, "term-"+s.ID)
		if err := os.Mkdir(r.cgDir, 0o755); err != nil {
			return nil, err
		}
		dir, err := os.Open(r.cgDir)
		if err != nil {
			return nil, err
		}
		defer dir.Close()
		cmd.SysProcAttr.UseCgroupFD, cmd.SysProcAttr.CgroupFD = true, int(dir.Fd())
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() { r.waited <- cmd.Wait() }()
	st, err := e.Proc.Stat(cmd.Process.Pid) // our unreaped child: PID is stable
	if err != nil {
		return nil, err
	}
	r.Terminal = Terminal{ID: s.ID, AgentID: s.AgentID, PID: st.PID, Start: st.Start}
	if r.cgDir != "" {
		r.Cgroup = strings.TrimPrefix(r.cgDir, "/sys/fs/cgroup")
	}
	e.terms = append(e.terms, r.Terminal)
	return r, nil
}

// Stop ends the root (stdin closed), then kills what is left: the session
// group, or the whole cgroup in ModeCgroup.
func (r *Running) Stop(ctx context.Context) error {
	r.stdin.Close()
	var err error
	select {
	case err = <-r.waited:
	case <-ctx.Done():
		_ = syscall.Kill(-r.PID, syscall.SIGKILL)
		err = <-r.waited
	}
	if r.cgDir != "" {
		if kerr := killCgroup(ctx, r.cgDir); kerr != nil {
			return kerr
		}
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return nil // killed or non-zero: the scenario result is what matters
	}
	return err
}

func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "MESTRE_") && !strings.HasPrefix(kv, "SPIKE_") && !strings.HasPrefix(kv, "TMUX") {
			env = append(env, kv)
		}
	}
	return env
}
