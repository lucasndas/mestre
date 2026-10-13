//go:build windows

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

// control is one line of JSON on the control pipe (Administrators only).
type control struct {
	Op       string   `json:"op"` // setup, type, wait, output, teardown
	DACL     bool     `json:"dacl,omitempty"`
	Term     string   `json:"term,omitempty"`
	Line     string   `json:"line,omitempty"`
	Labels   []string `json:"labels,omitempty"`
	TimeoutS float64  `json:"timeout_s,omitempty"`
}

type controlResp struct {
	Error   string               `json:"error,omitempty"`
	Log     []string             `json:"log,omitempty"`
	Results map[string]*identity `json:"results,omitempty"`
	Waited  string               `json:"waited,omitempty"`
	Output  string               `json:"output,omitempty"`
}

// probeMsg is what a probe sends on the identity pipe.
type probeMsg struct {
	Label   string `json:"label"`
	Note    string `json:"note,omitempty"`
	SelfPID uint32 `json:"self_pid"`
}

type terminal struct {
	name        string
	breakawayOK bool
	pid         uint32
	created     int64
	proc, job   windows.Handle
	hpc         windows.Handle
	ptyIn       windows.Handle
	ptyOut      windows.Handle
	outMu       sync.Mutex
	out         []byte
}

var (
	stateMu  sync.Mutex
	changed  = make(chan struct{})
	terms    []*terminal
	results  = map[string]*identity{}
	token    windows.Token
	ticket   string
	logon    *windows.SID
	winsta   windows.Handle
	desktop  windows.Handle
	ownSID   *windows.SID
	agentSID *windows.SID
)

// terminalsSnapshot is safe to call from the identity handlers.
func terminalsSnapshot() []*terminal {
	stateMu.Lock()
	defer stateMu.Unlock()
	return append([]*terminal(nil), terms...)
}

func rootTerminal(pid uint32, created int64) string {
	for _, t := range terminalsSnapshot() {
		if t.pid == pid && t.created == created {
			return t.name
		}
	}
	return ""
}

func notifyLocked() {
	close(changed)
	changed = make(chan struct{})
}

type handler struct{}

func runService() error {
	if f, err := os.OpenFile(filepath.Join(logDir, "callerid.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		log.SetOutput(f)
	}
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	return svc.Run(serviceName, handler{})
}

func (handler) Execute(_ []string, req <-chan svc.ChangeRequest, st chan<- svc.Status) (bool, uint32) {
	st <- svc.Status{State: svc.StartPending}
	if u, err := windows.GetCurrentProcessToken().GetTokenUser(); err == nil {
		ownSID, _ = u.User.Sid.Copy()
	}
	agentSID, _, _, _ = windows.LookupSID("", agentUser)
	log.Printf("start: own SID %v, agent SID %v", ownSID, agentSID)
	go serve(controlPipe, "D:P(A;;GA;;;SY)(A;;GA;;;BA)", handleControl)
	// mestre-agente may connect to the identity pipe; Administrators too, for
	// the "outside any terminal" case.
	idSDDL := "D:P(A;;GA;;;SY)(A;;GA;;;BA)"
	if agentSID != nil {
		idSDDL += "(A;;GRGW;;;" + agentSID.String() + ")"
	}
	go serve(identityPipe, idSDDL, handleIdentity)
	st <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for c := range req {
		switch c.Cmd {
		case svc.Interrogate:
			st <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			teardown()
			log.Printf("stop")
			st <- svc.Status{State: svc.StopPending}
			return false, 0
		}
	}
	return false, 0
}

func newPipe(name, sddl string) (windows.Handle, error) {
	// The service's own SID: new instances of a pipe need
	// FILE_CREATE_PIPE_INSTANCE on the first instance's DACL.
	if ownSID != nil {
		sddl += "(A;;GA;;;" + ownSID.String() + ")"
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return 0, err
	}
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	return windows.CreateNamedPipe(wstr(name), windows.PIPE_ACCESS_DUPLEX,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		windows.PIPE_UNLIMITED_INSTANCES, 64<<10, 64<<10, 0, sa)
}

func serve(name, sddl string, handle func(windows.Handle, *os.File)) {
	for {
		h, err := newPipe(name, sddl)
		if err != nil {
			log.Printf("pipe %s: %v", name, err)
			return
		}
		if err := windows.ConnectNamedPipe(h, nil); err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
			windows.CloseHandle(h)
			continue
		}
		go func() {
			f := os.NewFile(uintptr(h), name)
			defer f.Close()
			handle(h, f)
			_ = windows.FlushFileBuffers(h)
			_ = windows.DisconnectNamedPipe(h)
		}()
	}
}

func handleIdentity(h windows.Handle, f *os.File) {
	var pid uint32
	pidErr := windows.GetNamedPipeClientProcessId(h, &pid)
	line, err := bufio.NewReader(f).ReadBytes('\n')
	if err != nil {
		log.Printf("identity read: %v", err)
		return
	}
	var m probeMsg
	_ = json.Unmarshal(line, &m)
	if pidErr != nil {
		log.Printf("identity %s: GetNamedPipeClientProcessId: %v", m.Label, pidErr)
		return
	}
	id := identify(pid, m.Label, m.Note)
	if m.SelfPID != pid {
		id.Note += fmt.Sprintf(" [self-reported pid %d differs]", m.SelfPID)
	}
	b, _ := json.Marshal(id)
	log.Printf("identity: %s", b)
	stateMu.Lock()
	results[m.Label] = id
	notifyLocked()
	stateMu.Unlock()
	_, _ = f.Write(append(b, '\n'))
}

func handleControl(_ windows.Handle, f *os.File) {
	line, err := bufio.NewReader(f).ReadBytes('\n')
	if err != nil {
		return
	}
	var c control
	var resp controlResp
	if err := json.Unmarshal(line, &c); err != nil {
		resp.Error = err.Error()
	} else {
		log.Printf("control: %s", strings.TrimSpace(string(line)))
		switch c.Op {
		case "setup":
			resp.Log, err = setup(c.DACL)
		case "type":
			err = typeLine(c.Term, c.Line)
		case "wait":
			resp.Results, resp.Waited = waitResults(c.Labels, time.Duration(c.TimeoutS*float64(time.Second)))
		case "output":
			resp.Output, err = output(c.Term)
		case "teardown":
			teardown()
		default:
			err = fmt.Errorf("unknown op %q", c.Op)
		}
		if err != nil {
			resp.Error = err.Error()
		}
	}
	b, _ := json.Marshal(resp)
	_, _ = f.Write(append(b, '\n'))
}

func waitResults(labels []string, d time.Duration) (map[string]*identity, string) {
	start := time.Now()
	deadline := start.Add(d)
	for {
		stateMu.Lock()
		out := map[string]*identity{}
		for _, l := range labels {
			if r, ok := results[l]; ok {
				out[l] = r
			}
		}
		ch := changed
		stateMu.Unlock()
		left := time.Until(deadline)
		if len(out) == len(labels) || left <= 0 {
			return out, time.Since(start).Round(time.Millisecond).String()
		}
		t := time.NewTimer(left)
		select {
		case <-ch:
			t.Stop()
		case <-t.C:
		}
	}
}

// setup gets mestre-agente's token from MestreSpikeLauncher (the adopted
// token handoff) and opens two terminals: T1 in a job without
// JOB_OBJECT_LIMIT_BREAKAWAY_OK, T2 in a job with it.
func setup(dacl bool) ([]string, error) {
	teardown()
	var logs []string
	logf := func(format string, a ...any) {
		s := fmt.Sprintf(format, a...)
		log.Print(s)
		logs = append(logs, s)
	}
	if err := enablePrivileges("SeAssignPrimaryTokenPrivilege", "SeIncreaseQuotaPrivilege"); err != nil {
		logf("enable privileges: %v", err)
	}
	resp, err := callLauncher(launcherReq{Op: "logon"})
	if err != nil {
		return logs, fmt.Errorf("logon by %s: %w", launcherSvc, err)
	}
	stateMu.Lock()
	token, ticket = windows.Token(resp.Token), resp.Ticket
	stateMu.Unlock()
	logf("token from %s, ticket %s", launcherSvc, resp.Ticket)
	if d, err := defaultDACL(token); err == nil {
		logf("token default DACL: %s", aclSDDL(d))
	}
	if dacl {
		// PROCESS_QUERY_LIMITED_INFORMATION | SYNCHRONIZE for the service.
		if err := grantInDefaultDACL(token, ownSID, windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE); err != nil {
			logf("default DACL change failed: %v", err)
		} else if d, err := defaultDACL(token); err == nil {
			logf("token default DACL now: %s", aclSDDL(d))
		}
	}
	if logon, err = logonSID(token); err == nil {
		winsta, desktop = processWindowStation(), threadDesktop()
		e1 := setObjectACE(winsta, logon, winstaAllAccess, true)
		e2 := setObjectACE(desktop, logon, desktopAllAccess, true)
		logf("logon SID %s on window station and desktop: %v %v", logon, e1, e2)
	}
	env := mergeEnv(resp.Env, []string{"TERM=xterm-256color", "PROMPT=$G$S"})
	for _, spec := range []struct {
		name string
		ok   bool
	}{{"T1", false}, {"T2", true}} {
		t, err := startTerminal(spec.name, spec.ok, env)
		if err != nil {
			return logs, err
		}
		stateMu.Lock()
		terms = append(terms, t)
		stateMu.Unlock()
		logf("%s: root cmd.exe pid %d, job breakaway_ok=%v", t.name, t.pid, t.breakawayOK)
	}
	return logs, nil
}

func startTerminal(name string, breakawayOK bool, env []string) (*terminal, error) {
	t := &terminal{name: name, breakawayOK: breakawayOK}
	var inR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &t.ptyIn, nil, 0); err != nil {
		return nil, err
	}
	if err := windows.CreatePipe(&t.ptyOut, &outW, nil, 0); err != nil {
		return nil, err
	}
	err := windows.CreatePseudoConsole(windows.Coord{X: 200, Y: 50}, inR, outW, 0, &t.hpc)
	windows.CloseHandle(inR)
	windows.CloseHandle(outW)
	if err != nil {
		return nil, fmt.Errorf("CreatePseudoConsole: %w", err)
	}
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, err
	}
	defer attrs.Delete()
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&t.hpc)), unsafe.Sizeof(t.hpc)); err != nil {
		return nil, err
	}
	si := windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = windows.STARTF_USESTDHANDLES
	var pi windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_SUSPENDED)
	cmd := `C:\Windows\System32\cmd.exe /q /k`
	if err := windows.CreateProcessAsUser(token, nil, wstr(cmd), nil, nil, false, flags, envBlock(env), wstr(workDir), &si.StartupInfo, &pi); err != nil {
		return nil, fmt.Errorf("CreateProcessAsUser: %w", err)
	}
	defer windows.CloseHandle(pi.Thread)
	t.proc, t.pid = pi.Process, pi.ProcessId
	if t.job, err = windows.CreateJobObject(nil, nil); err != nil {
		windows.TerminateProcess(pi.Process, 1)
		return nil, err
	}
	var li windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	li.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if breakawayOK {
		li.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
	}
	if _, err := windows.SetInformationJobObject(t.job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&li)), uint32(unsafe.Sizeof(li))); err != nil {
		windows.TerminateProcess(pi.Process, 1)
		return nil, err
	}
	if err := windows.AssignProcessToJobObject(t.job, pi.Process); err != nil {
		windows.TerminateProcess(pi.Process, 1)
		return nil, err
	}
	if t.created, err = processCreated(pi.Process); err != nil {
		windows.TerminateProcess(pi.Process, 1)
		return nil, err
	}
	if _, err := windows.ResumeThread(pi.Thread); err != nil {
		return nil, err
	}
	go t.pump()
	return t, nil
}

func (t *terminal) pump() {
	buf := make([]byte, 32<<10)
	for {
		var n uint32
		err := windows.ReadFile(t.ptyOut, buf, &n, nil)
		if n > 0 {
			t.outMu.Lock()
			t.out = append(t.out, buf[:n]...)
			t.outMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func findTerminal(name string) (*terminal, error) {
	for _, t := range terminalsSnapshot() {
		if t.name == name {
			return t, nil
		}
	}
	return nil, fmt.Errorf("no terminal %q", name)
}

func typeLine(name, line string) error {
	t, err := findTerminal(name)
	if err != nil {
		return err
	}
	var n uint32
	return windows.WriteFile(t.ptyIn, []byte(line+"\r"), &n, nil)
}

func output(name string) (string, error) {
	t, err := findTerminal(name)
	if err != nil {
		return "", err
	}
	t.outMu.Lock()
	defer t.outMu.Unlock()
	return strip(t.out), nil
}

func teardown() {
	stateMu.Lock()
	ts := terms
	terms = nil
	results = map[string]*identity{}
	tok, tk, lg := token, ticket, logon
	token, ticket, logon = 0, "", nil
	stateMu.Unlock()
	for _, t := range ts {
		windows.ClosePseudoConsole(t.hpc)
		_ = windows.TerminateJobObject(t.job, 1)
		for _, h := range []windows.Handle{t.job, t.proc, t.ptyIn, t.ptyOut} {
			windows.CloseHandle(h)
		}
	}
	if lg != nil {
		_ = setObjectACE(winsta, lg, winstaAllAccess, false)
		_ = setObjectACE(desktop, lg, desktopAllAccess, false)
	}
	if tk != "" {
		_, _ = callLauncher(launcherReq{Op: "release", Ticket: tk})
	}
	if tok != 0 {
		tok.Close()
	}
	if len(ts) > 0 {
		log.Printf("teardown: %d terminals closed", len(ts))
	}
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

// Launcher protocol of tools/winsandbox/spikes/01-agent-service (one JSON line
// each way on \\.\pipe\mestre-spike-MestreSpikeLauncher).

type launcherReq struct {
	Op     string `json:"op"`
	Ticket string `json:"ticket,omitempty"`
}

type launcherResp struct {
	Error  string   `json:"error,omitempty"`
	Token  uint64   `json:"token,omitempty"`
	Env    []string `json:"env,omitempty"`
	Ticket string   `json:"ticket,omitempty"`
}

func callLauncher(req launcherReq) (launcherResp, error) {
	var resp launcherResp
	if err := callJSON(launcherPipe, req, &resp); err != nil {
		return resp, err
	}
	if resp.Error != "" {
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}

// callJSON sends one JSON line on a pipe and reads one back.
func callJSON(pipe string, req, resp any) error {
	f, err := openPipe(pipe, 10*time.Second)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(req)
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	line, err := bufio.NewReader(f).ReadBytes('\n')
	if err != nil {
		return err
	}
	return json.Unmarshal(line, resp)
}

// openPipe connects to a pipe, waiting (bounded) while all instances are busy.
func openPipe(name string, d time.Duration) (*os.File, error) {
	deadline := time.Now().Add(d)
	for {
		f, err := os.OpenFile(name, os.O_RDWR, 0)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, windows.ERROR_PIPE_BUSY) || time.Now().After(deadline) {
			return nil, fmt.Errorf("connect to %s: %w", name, err)
		}
		waitNamedPipe(name, time.Second)
	}
}
