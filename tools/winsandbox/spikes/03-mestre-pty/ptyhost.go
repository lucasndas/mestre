//go:build windows

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	pty "github.com/aymanbagabas/go-pty"
	"github.com/charmbracelet/x/vt"
	"golang.org/x/sys/windows"
)

const (
	bufferSize = 1 << 20 // output ring buffer (docs: 1 MiB)
	replayMax  = 256 << 10
	// A ptyhost whose agent never starts gives up after this.
	startDeadline = 10 * time.Minute
	// The restricted default DACL of a hardened ptyhost: threads and other
	// unnamed objects created afterwards are SYSTEM's; the owner (mestre-agente)
	// keeps only SYNCHRONIZE.
	hardenedDefaultDACL = "D:(A;;GA;;;SY)(A;;0x100000;;;OW)"
	hardenedThreadDACL  = "D:P(A;;GA;;;SY)(A;;0x100000;;;OW)"
)

var hostIDPattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

type host struct {
	id     string
	secret []byte
	log    *log.Logger
	pty    pty.Pty
	job    windows.Handle
	ls     string // logon session of this ptyhost (one per terminal)
	harden string

	agentToken windows.Token // hardened: a child token with the original default DACL

	mu       sync.Mutex
	emu      *vt.Emulator
	out      []byte
	base     int64 // offset of out[0]
	total    int64
	cmd      *pty.Cmd
	agentPID uint32
	agentCT  int64
	agentH   windows.Handle
	exitCode *int
	started  chan struct{}

	connMu sync.Mutex
	conn   net.Conn // the authenticated engine; a new one replaces it
	done   chan struct{}
	once   sync.Once
}

func runHost(args []string) error {
	fs := flag.NewFlagSet("ptyhost", flag.ExitOnError)
	id := fs.String("id", "", "terminal id")
	harden := fs.Bool("harden", false, "restrict the DACL of this process's threads (plan for item (d))")
	_ = fs.Parse(args)
	if !hostIDPattern.MatchString(*id) {
		return errors.New("bad -id")
	}
	_ = os.MkdirAll(hostLogDir, 0o755)
	lf, err := os.OpenFile(filepath.Join(hostLogDir, *id+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer lf.Close()
	h := &host{id: *id, log: log.New(lf, "", log.LstdFlags|log.Lmicroseconds), started: make(chan struct{}), done: make(chan struct{})}
	err = h.run(*harden)
	if err != nil {
		h.log.Printf("fatal: %v", err)
	}
	return err
}

func (h *host) run(harden bool) error {
	// The secret: 32 bytes on stdin, an inherited pipe; never in argv or env.
	h.secret = make([]byte, 32)
	if _, err := io.ReadFull(os.Stdin, h.secret); err != nil {
		return fmt.Errorf("read secret from stdin: %w", err)
	}
	os.Stdin.Close()
	t := windows.GetCurrentProcessToken()
	h.ls, _ = logonSession(t)
	user := "?"
	if u, err := t.GetTokenUser(); err == nil {
		user = accountName(u.User.Sid)
	}
	var sess uint32
	_ = windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &sess)
	inJob, _ := isProcessInJob(windows.CurrentProcess(), 0)
	h.log.Printf("start: pid %d, user %s, session %d, logon session %s, in a job: %v, secret read from stdin (%d bytes)",
		windows.GetCurrentProcessId(), user, sess, h.ls, inJob, len(h.secret))
	if harden {
		h.harden = h.hardenSelf()
		h.log.Printf("harden: %s", h.harden)
	}

	// Job: KILL_ON_JOB_CLOSE, no breakaway, anonymous, DACL for SYSTEM only
	// (this process keeps the full-access handle from the creation).
	jsa, err := securityAttributes("D:P(A;;GA;;;SY)")
	if err != nil {
		return err
	}
	if h.job, err = windows.CreateJobObject(jsa, nil); err != nil {
		return fmt.Errorf("CreateJobObject: %w", err)
	}
	var li windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	li.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(h.job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&li)), uint32(unsafe.Sizeof(li))); err != nil {
		return fmt.Errorf("SetInformationJobObject: %w", err)
	}

	if h.pty, err = pty.New(); err != nil {
		return fmt.Errorf("go-pty New: %w", err)
	}
	if err := h.pty.Resize(cols, rows); err != nil {
		return fmt.Errorf("go-pty Resize: %w", err)
	}
	h.emu = vt.NewEmulator(cols, rows)
	go h.pumpOutput()
	go h.pumpReplies()
	h.log.Printf("ConPTY %dx%d (go-pty %s), job with KILL_ON_JOB_CLOSE", cols, rows, h.pty.Name())

	path := sockPath(h.id)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("listen %s: %w", path, err)
	}
	h.log.Printf("listening on %s; socket file SD %s", path, pathSD(path))
	go h.accept(ln)

	select {
	case <-h.started:
	case <-h.done:
	case <-time.After(startDeadline):
		h.log.Printf("no start in %v: exiting", startDeadline)
		h.shutdown()
	}
	<-h.done
	ln.Close()
	h.log.Printf("exit")
	return nil
}

// hardenSelf restricts the DACL of the threads that exist and, through the
// token's default DACL, of those Go creates later. The agent gets a child
// token with the original default DACL, so its processes stay as before.
func (h *host) hardenSelf() string {
	var t windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_ADJUST_DEFAULT|windows.TOKEN_DUPLICATE|windows.TOKEN_ASSIGN_PRIMARY, &t); err != nil {
		return "OpenProcessToken: " + err.Error()
	}
	defer t.Close()
	var err error
	if h.agentToken, err = createRestrictedToken(t); err != nil {
		return err.Error()
	}
	acl, err := daclFromSDDL(hardenedDefaultDACL)
	if err != nil {
		return err.Error()
	}
	if err := setDefaultDACL(t, acl); err != nil {
		return err.Error()
	}
	tacl, err := daclFromSDDL(hardenedThreadDACL)
	if err != nil {
		return err.Error()
	}
	tids, err := threadIDs(windows.GetCurrentProcessId())
	if err != nil {
		return err.Error()
	}
	fixed := 0
	var errs []string
	for _, tid := range tids {
		th, err := windows.OpenThread(windows.WRITE_DAC, false, tid)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%d: %v", tid, err))
			continue
		}
		err = windows.SetSecurityInfo(th, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, tacl, nil)
		windows.CloseHandle(th)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%d: %v", tid, err))
			continue
		}
		fixed++
	}
	d, _, _ := defaultDACL(t)
	return fmt.Sprintf("default DACL now %s; %d/%d threads restricted %v; agent gets a child token", aclSDDL(d), fixed, len(tids), errs)
}

func (h *host) pumpOutput() {
	buf := make([]byte, 32<<10)
	for {
		n, err := h.pty.Read(buf)
		if n > 0 {
			h.mu.Lock()
			h.out = append(h.out, buf[:n]...)
			h.total += int64(n)
			if over := len(h.out) - bufferSize; over > 0 {
				h.out = append([]byte(nil), h.out[over:]...)
				h.base += int64(over)
			}
			_, _ = h.emu.Write(buf[:n])
			h.mu.Unlock()
		}
		if err != nil {
			h.log.Printf("ConPTY output ended: %v", err)
			return
		}
	}
}

// pumpReplies sends the emulator's answers to terminal queries (cursor
// position, device attributes) back to the program, as a terminal does. The
// emulator writes them to a synchronous pipe: without a reader, Write blocks.
func (h *host) pumpReplies() {
	buf := make([]byte, 4096)
	for {
		n, err := h.emu.Read(buf)
		if n > 0 {
			_, _ = h.pty.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func (h *host) accept(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			select {
			case <-h.done:
			default:
				h.log.Printf("accept: %v", err)
			}
			return
		}
		go h.serve(c)
	}
}

func (h *host) serve(c net.Conn) {
	defer c.Close()
	peer, perr := peerPID(c)
	l := newLineConn(c)
	if err := hostHandshake(l, h.secret, windows.GetCurrentProcessId()); err != nil {
		h.log.Printf("connection from pid %d (peer pid err %v) refused: %v", peer, perr, err)
		return
	}
	h.log.Printf("engine connected: peer pid %d (err %v), handshake ok", peer, perr)
	h.connMu.Lock()
	if h.conn != nil {
		h.conn.Close() // a restarted engine replaces the old connection
	}
	h.conn = c
	h.connMu.Unlock()
	for {
		var req hostReq
		if err := l.recv(&req, 0); err != nil {
			h.log.Printf("engine connection ended: %v", err)
			return
		}
		resp, exit := h.handle(req)
		if err := l.send(resp); err != nil {
			h.log.Printf("send: %v", err)
		}
		if exit {
			h.shutdown()
			return
		}
	}
}

func (h *host) shutdown() { h.once.Do(func() { close(h.done) }) }

func (h *host) handle(req hostReq) (resp hostResp, exit bool) {
	switch req.Op {
	case "start":
		resp = h.start(req)
	case "screen":
		h.mu.Lock()
		resp.Text = h.emu.String()
		h.mu.Unlock()
	case "input":
		if _, err := h.pty.Write(req.Data); err != nil {
			resp.Error = err.Error()
		}
	case "replay":
		h.mu.Lock()
		from := max(req.From, h.base)
		resp.Lost = req.From < h.base
		end := min(int64(len(h.out)), from-h.base+replayMax)
		resp.Data = append([]byte(nil), h.out[from-h.base:end]...)
		resp.From, resp.To = from, h.base+end
		h.mu.Unlock()
	case "status":
		resp.Status = h.status()
	case "injob":
		resp.In, resp.Error = h.inJob(req.PID, req.Created)
	case "stop":
		resp = h.stop(req)
		exit = req.Mode == "kill"
	default:
		resp.Error = "unknown op " + req.Op
	}
	return resp, exit
}

func (h *host) start(req hostReq) hostResp {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cmd != nil {
		return hostResp{Error: "already started"}
	}
	c := h.pty.Command(req.Path, req.Args...)
	c.Env = mergeEnv(os.Environ(), append([]string{"TERM=xterm-256color"}, req.Env...))
	c.Dir = req.Dir
	// go-pty closes the thread handle after CreateProcess, so the process is
	// resumed with NtResumeProcess once it is in the job.
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED, Token: syscall.Token(h.agentToken)}
	if err := c.Start(); err != nil {
		return hostResp{Error: "start: " + err.Error()}
	}
	pid := uint32(c.Process.Pid)
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME|
		windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		_ = c.Process.Kill()
		return hostResp{Error: "OpenProcess(agent): " + err.Error()}
	}
	if err := windows.AssignProcessToJobObject(h.job, ph); err != nil {
		windows.TerminateProcess(ph, 1)
		return hostResp{Error: "AssignProcessToJobObject: " + err.Error()}
	}
	ct, _ := processCreated(ph)
	if err := ntResumeProcess(ph); err != nil {
		windows.TerminateProcess(ph, 1)
		return hostResp{Error: err.Error()}
	}
	h.cmd, h.agentPID, h.agentCT, h.agentH = c, pid, ct, ph
	close(h.started)
	h.log.Printf("agent started suspended, put in the job, resumed: pid %d, %s %v", pid, req.Path, req.Args)
	go func() {
		err := c.Wait()
		code := 0
		if c.ProcessState != nil {
			code = c.ProcessState.ExitCode()
		}
		h.mu.Lock()
		h.exitCode = &code
		h.mu.Unlock()
		h.log.Printf("agent pid %d exited: code %d (%v)", pid, code, err)
	}()
	return hostResp{PID: pid, Created: ct}
}

func (h *host) inJob(pid uint32, created int64) (bool, string) {
	ph, ct, err := openCreated(pid, 0)
	if err != nil {
		return false, "OpenProcess: " + err.Error()
	}
	defer windows.CloseHandle(ph)
	if created != 0 && ct != created {
		return false, "creation time differs (PID reused)"
	}
	in, err := isProcessInJob(ph, h.job)
	if err != nil {
		return false, err.Error()
	}
	return in, ""
}

func (h *host) status() *hostStatus {
	st := &hostStatus{HostPID: windows.GetCurrentProcessId(), LogonSession: h.ls, Harden: h.harden, SocketSD: pathSD(sockPath(h.id))}
	t := windows.GetCurrentProcessToken()
	if u, err := t.GetTokenUser(); err == nil {
		st.User = accountName(u.User.Sid)
	}
	_ = windows.ProcessIdToSessionId(st.HostPID, &st.Session)
	st.HostInAJob, _ = isProcessInJob(windows.CurrentProcess(), 0)
	h.mu.Lock()
	st.AgentPID, st.AgentCreated, st.ExitCode, st.OutTotal = h.agentPID, h.agentCT, h.exitCode, h.total
	h.mu.Unlock()
	st.AgentAlive = st.AgentPID != 0 && st.ExitCode == nil
	snap := snapshot()
	inJob := jobPIDs(h.job)
	for _, pid := range inJob {
		st.Job = append(st.Job, snap[pid])
	}
	procs, _ := sessionProcesses(h.ls)
	for _, p := range procs {
		if p.PID != st.HostPID && !slices.Contains(inJob, p.PID) {
			st.OutsideJob = append(st.OutsideJob, p)
		}
	}
	return st
}

// stop: "close" is ClosePseudoConsole (closing step 2); "kill" is
// TerminateJobObject (step 3), with sweep also every process left in this
// terminal's logon session (what escaped through WMI); then the host exits.
func (h *host) stop(req hostReq) hostResp {
	var resp hostResp
	logf := func(format string, a ...any) {
		s := fmt.Sprintf(format, a...)
		h.log.Print(s)
		resp.Log = append(resp.Log, s)
	}
	switch req.Mode {
	case "close":
		err := h.pty.Close()
		logf("ClosePseudoConsole (go-pty Close): err=%v", err)
		start := time.Now()
		left := h.waitJobEmpty(10 * time.Second)
		logf("job after close: %d active (waited %v, deadline 10s)", left, time.Since(start).Round(time.Millisecond))
		for _, pid := range jobPIDs(h.job) {
			resp.Left = append(resp.Left, snapshot()[pid])
		}
	case "kill":
		before := jobPIDs(h.job)
		snap := snapshot()
		for _, pid := range before {
			resp.Killed = append(resp.Killed, snap[pid])
		}
		err := windows.TerminateJobObject(h.job, 1)
		start := time.Now()
		left := h.waitJobEmpty(5 * time.Second)
		logf("TerminateJobObject: err=%v, %d processes before, %d active after (waited %v, deadline 5s)", err, len(before), left, time.Since(start).Round(time.Millisecond))
		if req.Sweep {
			procs, denied := sessionProcesses(h.ls)
			self := windows.GetCurrentProcessId()
			for _, p := range procs {
				if p.PID == self {
					continue
				}
				ph, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, p.PID)
				if err == nil {
					// A process the job already ended may still be listed
					// while it exits; TerminateProcess denies it.
					if ev, _ := windows.WaitForSingleObject(ph, 0); ev == windows.WAIT_OBJECT_0 {
						windows.CloseHandle(ph)
						continue
					}
					err = windows.TerminateProcess(ph, 1)
					windows.CloseHandle(ph)
				}
				logf("sweep logon session %s: terminate pid %d %s: err=%v", h.ls, p.PID, p.Image, err)
				resp.Killed = append(resp.Killed, p)
			}
			logf("sweep: %d processes in the logon session besides the host; %d processes not readable", len(procs)-1, denied)
		}
		_ = h.pty.Close()
	default:
		resp.Error = "mode must be close or kill"
	}
	return resp
}

func (h *host) waitJobEmpty(d time.Duration) uint32 {
	deadline := time.Now().Add(d)
	for {
		n, err := jobActive(h.job)
		if err != nil || n == 0 || time.Now().After(deadline) {
			return n
		}
		// Bounded poll of the job's counter; the spike has no completion port.
		time.Sleep(50 * time.Millisecond)
	}
}

func securityAttributes(sddl string) (*windows.SecurityAttributes, error) {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, err
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}, nil
}

func mergeEnv(env, extra []string) []string {
	out := append([]string(nil), env...)
	for _, kv := range extra {
		k, _, _ := strings.Cut(kv, "=")
		replaced := false
		for i, e := range out {
			if ek, _, _ := strings.Cut(e, "="); strings.EqualFold(ek, k) {
				out[i], replaced = kv, true
			}
		}
		if !replaced {
			out = append(out, kv)
		}
	}
	return out
}
