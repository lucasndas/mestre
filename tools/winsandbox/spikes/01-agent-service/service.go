//go:build windows

package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
)

// Frames on the session pipes: 1 byte kind, 4 bytes little-endian length,
// payload.
const (
	frOutput  = 'o' // service ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ client: terminal bytes
	frLog     = 'l' // service ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ client: one event line
	frStarted = 's' // service ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ client: startInfo JSON
	frExit    = 'x' // service ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ client: exitInfo JSON (main process ended)
	frPIDs    = 'P' // service ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ client: pidsInfo JSON
	frInput   = 'i' // client ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ service: bytes to type
	frResize  = 'r' // client ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ service: cols, rows (uint16 LE each)
	frClose   = 'c' // client ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ service: ClosePseudoConsole (closing step 2)
	frKill    = 'k' // client ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ service: TerminateJobObject (closing step 3)
	frQuery   = 'p' // client ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â ÃƒÂ¢Ã¢â€šÂ¬Ã¢â€žÂ¢ service: list the job's processes
)

type startInfo struct {
	PID          uint32   `json:"pid"`
	Session      uint32   `json:"session"`
	LogonSession string   `json:"logon_session"`
	ProfileDir   string   `json:"profile_dir"`
	Env          []string `json:"env"`
}

type exitInfo struct {
	Code  uint32 `json:"code"`
	Error string `json:"error,omitempty"`
}

type pidsInfo struct {
	PIDs []uint32 `json:"pids"`
}

// request is one line of JSON on the control pipe.
type request struct {
	Op   string   `json:"op"` // "info" or "open"
	Path string   `json:"path"`
	Args []string `json:"args"`
	Dir  string   `json:"dir"`
	Env  []string `json:"env"`
	Cols uint16   `json:"cols"`
	Rows uint16   `json:"rows"`
	// Experiments: skip LoadUserProfile, skip the window station/desktop
	// ACEs, set HOME to the loaded profile.
	NoProfile     bool `json:"no_profile"`
	NoWinsta      bool `json:"no_winsta"`
	HomeIsProfile bool `json:"home_is_profile"`
	// Experiment (narrow plan B): ask MestreSpikeLauncher (LocalSystem) for a
	// logged-on token with the profile loaded, then create the process here.
	TokenFromLauncher bool   `json:"token_from_launcher"`
	Ticket            string `json:"ticket"` // op release
}

type response struct {
	Error string    `json:"error,omitempty"`
	Info  *selfInfo `json:"info,omitempty"`
	In    string    `json:"in,omitempty"`
	Out   string    `json:"out,omitempty"`
	// op logon (launcher): token handle duplicated into the caller, the
	// environment of the loaded profile and the ticket to release it.
	Token  uint64   `json:"token,omitempty"`
	Env    []string `json:"env,omitempty"`
	Ticket string   `json:"ticket,omitempty"`
}

type selfInfo struct {
	Service       string   `json:"service"`
	User          string   `json:"user"`
	SID           string   `json:"sid"`
	Session       uint32   `json:"session"`
	LogonSession  string   `json:"logon_session"`
	WindowStation string   `json:"window_station"`
	Desktop       string   `json:"desktop"`
	Privileges    []string `json:"privileges"`
	Error         string   `json:"error,omitempty"`
}

func pipeName(service string) string { return `\\.\pipe\mestre-spike-` + service }

// pipeSDDL lets only SYSTEM and Administrators connect.
const pipeSDDL = "D:P(A;;GA;;;SY)(A;;GA;;;BA)"

type handler struct{ name string }

func runService(name string) error {
	f, err := os.OpenFile(filepath.Join(logDir, name+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		log.SetOutput(f)
	}
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	return svc.Run(name, handler{name})
}

func (h handler) Execute(_ []string, req <-chan svc.ChangeRequest, st chan<- svc.Status) (bool, uint32) {
	st <- svc.Status{State: svc.StartPending}
	info := describeSelf(h.name)
	b, _ := json.Marshal(info)
	log.Printf("start: %s", b)
	go serveControl(h.name)
	st <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for c := range req {
		switch c.Cmd {
		case svc.Interrogate:
			st <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			log.Printf("stop")
			st <- svc.Status{State: svc.StopPending}
			return false, 0
		}
	}
	return false, 0
}

func describeSelf(name string) *selfInfo {
	in := &selfInfo{Service: name}
	t := windows.GetCurrentProcessToken()
	if u, err := t.GetTokenUser(); err == nil {
		in.SID = u.User.Sid.String()
		in.User = accountName(u.User.Sid)
	}
	_ = windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &in.Session)
	in.LogonSession = logonSessionID(t)
	in.WindowStation = userObjectName(processWindowStation())
	in.Desktop = userObjectName(threadDesktop())
	p, err := tokenPrivileges(t)
	if err != nil {
		in.Error = err.Error()
	}
	in.Privileges = p
	return in
}

func newPipe(name string, access uint32) (windows.Handle, error) {
	// The service's own SID too: new instances of a pipe need
	// FILE_CREATE_PIPE_INSTANCE on the first instance's DACL.
	sddl := pipeSDDL
	if u, err := windows.GetCurrentProcessToken().GetTokenUser(); err == nil {
		sddl += "(A;;GA;;;" + u.User.Sid.String() + ")"
	}
	// The mestre service may call the launcher (token handoff experiment).
	if n, _, _, err := windows.LookupSID("", `NT SERVICE\`+mainSvc); err == nil {
		sddl += "(A;;GA;;;" + n.String() + ")"
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return 0, err
	}
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	h, err := windows.CreateNamedPipe(wstr(name), access,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		windows.PIPE_UNLIMITED_INSTANCES, 64<<10, 64<<10, 0, sa)
	if err != nil {
		return 0, fmt.Errorf("CreateNamedPipe %s: %w", name, err)
	}
	return h, nil
}

func connectPipe(h windows.Handle) error {
	err := windows.ConnectNamedPipe(h, nil)
	if err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		return err
	}
	return nil
}

func serveControl(name string) {
	for {
		h, err := newPipe(pipeName(name), windows.PIPE_ACCESS_DUPLEX)
		if err != nil {
			log.Printf("control pipe: %v", err)
			return
		}
		if err := connectPipe(h); err != nil {
			log.Printf("connect: %v", err)
			windows.CloseHandle(h)
			continue
		}
		go handleControl(name, os.NewFile(uintptr(h), "control"))
	}
}

func handleControl(name string, f *os.File) {
	defer f.Close()
	line, err := bufio.NewReader(f).ReadBytes('\n')
	if err != nil {
		log.Printf("control read: %v", err)
		return
	}
	var req request
	var resp response
	if err := json.Unmarshal(line, &req); err != nil {
		resp.Error = err.Error()
	} else {
		log.Printf("request: %s", strings.TrimSpace(string(line)))
		switch req.Op {
		case "info":
			resp.Info = describeSelf(name)
		case "open":
			resp.In, resp.Out, err = openSession(name, req)
			if err != nil {
				resp.Error = err.Error()
			}
		case "logon":
			var pid uint32
			if err := windows.GetNamedPipeClientProcessId(windows.Handle(f.Fd()), &pid); err != nil {
				resp.Error = err.Error()
			} else {
				resp = logonFor(pid)
			}
		case "release":
			releaseLogon(req.Ticket)
		default:
			resp.Error = "unknown op " + req.Op
		}
	}
	b, _ := json.Marshal(resp)
	_, _ = f.Write(append(b, '\n'))
	_ = windows.FlushFileBuffers(windows.Handle(f.Fd()))
	_ = windows.DisconnectNamedPipe(windows.Handle(f.Fd()))
}

var sessionSeq atomic.Int64

func openSession(name string, req request) (string, string, error) {
	id := fmt.Sprintf("%d-%d", os.Getpid(), sessionSeq.Add(1))
	inName, outName := pipeName(name)+"-"+id+"-in", pipeName(name)+"-"+id+"-out"
	hin, err := newPipe(inName, windows.PIPE_ACCESS_INBOUND)
	if err != nil {
		return "", "", err
	}
	hout, err := newPipe(outName, windows.PIPE_ACCESS_OUTBOUND)
	if err != nil {
		windows.CloseHandle(hin)
		return "", "", err
	}
	go func() {
		// The client connects right after the response; it is a spike, so a
		// client that never connects leaves this goroutine blocked.
		if err := connectPipe(hout); err != nil {
			log.Printf("session %s: connect out: %v", id, err)
			return
		}
		if err := connectPipe(hin); err != nil {
			log.Printf("session %s: connect in: %v", id, err)
			return
		}
		s := &session{id: id, req: req, in: os.NewFile(uintptr(hin), "in"), out: os.NewFile(uintptr(hout), "out")}
		s.run()
	}()
	return inName, outName, nil
}

type session struct {
	id    string
	req   request
	in    *os.File
	out   *os.File
	outMu sync.Mutex

	token   windows.Token
	ticket  string
	profile windows.Handle
	logon   *windows.SID
	winsta  windows.Handle
	desktop windows.Handle
	hpc     windows.Handle
	hpcOnce sync.Once
	job     windows.Handle
	proc    windows.Handle
	ptyIn   windows.Handle
	ptyOut  windows.Handle
}

func (s *session) send(kind byte, payload []byte) {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	var hdr [5]byte
	hdr[0] = kind
	binary.LittleEndian.PutUint32(hdr[1:], uint32(len(payload)))
	_, _ = s.out.Write(append(hdr[:], payload...))
}

func (s *session) logf(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	log.Printf("session %s: %s", s.id, msg)
	s.send(frLog, []byte(msg))
}

func (s *session) sendJSON(kind byte, v any) {
	b, _ := json.Marshal(v)
	s.send(kind, b)
}

func (s *session) run() {
	defer s.teardown()
	if err := s.start(); err != nil {
		s.logf("start failed: %v", err)
		s.sendJSON(frExit, exitInfo{Code: 0xFFFFFFFF, Error: err.Error()})
		// Wait for the client to hang up so it reads everything.
		_, _ = io.Copy(io.Discard, s.in)
		return
	}
	go s.pumpOutput()
	go s.wait()
	s.readInput()
}

// start logs on as mestre-agente, loads the profile and starts the process
// in a pseudoconsole and a job object.
func (s *session) start() error {
	pw, err := readAgentPassword()
	if err != nil {
		return err
	}
	if err := enablePrivileges("SeAssignPrimaryTokenPrivilege", "SeIncreaseQuotaPrivilege"); err != nil {
		s.logf("enable privileges: %v", err)
	} else {
		s.logf("enabled SeAssignPrimaryTokenPrivilege and SeIncreaseQuotaPrivilege in the service token")
	}
	var launcherEnv []string
	if s.req.TokenFromLauncher {
		resp, err := call(launchSvc, request{Op: "logon"})
		if err != nil {
			return fmt.Errorf("logon by %s: %w", launchSvc, err)
		}
		s.token, s.ticket, launcherEnv = windows.Token(resp.Token), resp.Ticket, resp.Env
		s.logf("token received from %s (LogonUser batch + LoadUserProfile done there), logon session %s", launchSvc, logonSessionID(s.token))
	} else if s.token, err = logonUser(agentUser, ".", pw, logon32LogonBatch); err != nil {
		return err
	} else {
		s.logf("LogonUser(batch) ok, logon session %s", logonSessionID(s.token))
	}
	if s.req.NoProfile || s.req.TokenFromLauncher {
		s.logf("LoadUserProfile skipped (experiment)")
	} else {
		// Only held when installed with -extra (experiment); harmless otherwise.
		if err := enablePrivileges("SeBackupPrivilege", "SeRestorePrivilege"); err == nil {
			s.logf("enabled SeBackupPrivilege and SeRestorePrivilege (experiment)")
		}
		s.profile, err = loadUserProfile(s.token, agentUser)
		if err != nil {
			return err
		}
		s.logf("LoadUserProfile ok")
	}
	if s.req.NoWinsta {
		s.logf("window station/desktop ACEs skipped (experiment)")
	} else if s.logon, err = logonSID(s.token); err != nil {
		s.logf("logon SID: %v", err)
	} else {
		s.winsta, s.desktop = processWindowStation(), threadDesktop()
		e1 := setObjectACE(s.winsta, s.logon, winstaAllAccess, true)
		e2 := setObjectACE(s.desktop, s.logon, desktopAllAccess, true)
		s.logf("grant logon SID %s on %s: winsta err=%v, desktop %s err=%v",
			s.logon, userObjectName(s.winsta), e1, userObjectName(s.desktop), e2)
	}
	env, err := tokenEnvironment(s.token)
	if launcherEnv != nil {
		env, err = launcherEnv, nil
		s.logf("environment from %s (CreateEnvironmentBlock there)", launchSvc)
	} else if err != nil && s.req.NoProfile {
		// Experiment: hive loaded by someone else (LocalSystem), which the
		// virtual account cannot read; build the profile variables from
		// ProfileList instead.
		s.logf("CreateEnvironmentBlock: %v; building the environment from ProfileList", err)
		env, err = profileListEnv(s.token)
	}
	if err != nil {
		return fmt.Errorf("CreateEnvironmentBlock: %w", err)
	}
	env = mergeEnv(env, append([]string{"TERM=xterm-256color"}, s.req.Env...))
	profileDir := lookupEnv(env, "USERPROFILE")
	if s.req.HomeIsProfile {
		env = mergeEnv(env, []string{"HOME=" + profileDir})
	}

	var inR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &s.ptyIn, nil, 0); err != nil {
		return fmt.Errorf("CreatePipe: %w", err)
	}
	if err := windows.CreatePipe(&s.ptyOut, &outW, nil, 0); err != nil {
		return fmt.Errorf("CreatePipe: %w", err)
	}
	cols, rows := s.req.Cols, s.req.Rows
	if cols == 0 {
		cols, rows = 200, 50
	}
	err = windows.CreatePseudoConsole(windows.Coord{X: int16(cols), Y: int16(rows)}, inR, outW, 0, &s.hpc)
	windows.CloseHandle(inR)
	windows.CloseHandle(outW)
	if err != nil {
		return fmt.Errorf("CreatePseudoConsole: %w", err)
	}
	s.logf("CreatePseudoConsole %dx%d ok", cols, rows)

	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return err
	}
	defer attrs.Delete()
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&s.hpc)), unsafe.Sizeof(s.hpc)); err != nil {
		return fmt.Errorf("UpdateProcThreadAttribute: %w", err)
	}
	si := windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = windows.STARTF_USESTDHANDLES // null std handles: the child uses only the pseudoconsole

	cmdline := windows.ComposeCommandLine(append([]string{s.req.Path}, s.req.Args...))
	var dir *uint16
	if s.req.Dir != "" {
		dir = wstr(s.req.Dir)
	}
	var pi windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_SUSPENDED)
	if err := windows.CreateProcessAsUser(s.token, nil, wstr(cmdline), nil, nil, false, flags, envBlock(env), dir, &si.StartupInfo, &pi); err != nil {
		return fmt.Errorf("CreateProcessAsUser %q: %w", cmdline, err)
	}
	s.proc = pi.Process
	defer windows.CloseHandle(pi.Thread)
	if s.job, err = windows.CreateJobObject(nil, nil); err != nil {
		windows.TerminateProcess(pi.Process, 1)
		return fmt.Errorf("CreateJobObject: %w", err)
	}
	var li windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	li.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(s.job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&li)), uint32(unsafe.Sizeof(li))); err != nil {
		windows.TerminateProcess(pi.Process, 1)
		return fmt.Errorf("SetInformationJobObject: %w", err)
	}
	if err := windows.AssignProcessToJobObject(s.job, pi.Process); err != nil {
		windows.TerminateProcess(pi.Process, 1)
		return fmt.Errorf("AssignProcessToJobObject: %w", err)
	}
	if _, err := windows.ResumeThread(pi.Thread); err != nil {
		return fmt.Errorf("ResumeThread: %w", err)
	}
	var sess uint32
	_ = windows.ProcessIdToSessionId(pi.ProcessId, &sess)
	s.logf("CreateProcessAsUser ok: pid %d, session %d, in job (KILL_ON_JOB_CLOSE)", pi.ProcessId, sess)
	s.sendJSON(frStarted, startInfo{PID: pi.ProcessId, Session: sess, LogonSession: logonSessionID(s.token), ProfileDir: profileDir, Env: env})
	return nil
}

func (s *session) pumpOutput() {
	buf := make([]byte, 32<<10)
	for {
		var n uint32
		err := windows.ReadFile(s.ptyOut, buf, &n, nil)
		if n > 0 {
			s.send(frOutput, append([]byte(nil), buf[:n]...))
		}
		if err != nil {
			s.logf("pseudoconsole output ended: %v", err)
			return
		}
	}
}

func (s *session) wait() {
	_, _ = windows.WaitForSingleObject(s.proc, windows.INFINITE)
	var code uint32
	_ = windows.GetExitCodeProcess(s.proc, &code)
	s.logf("main process exited with code %d", code)
	s.sendJSON(frExit, exitInfo{Code: code})
}

func (s *session) closePC() {
	s.hpcOnce.Do(func() {
		windows.ClosePseudoConsole(s.hpc)
		s.logf("ClosePseudoConsole done")
	})
}

func (s *session) readInput() {
	r := bufio.NewReader(s.in)
	for {
		var hdr [5]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			s.logf("client hung up: %v", err)
			return
		}
		p := make([]byte, binary.LittleEndian.Uint32(hdr[1:]))
		if _, err := io.ReadFull(r, p); err != nil {
			return
		}
		switch hdr[0] {
		case frInput:
			var n uint32
			if err := windows.WriteFile(s.ptyIn, p, &n, nil); err != nil {
				s.logf("write input: %v", err)
			}
		case frResize:
			c, rw := binary.LittleEndian.Uint16(p), binary.LittleEndian.Uint16(p[2:])
			err := windows.ResizePseudoConsole(s.hpc, windows.Coord{X: int16(c), Y: int16(rw)})
			s.logf("ResizePseudoConsole %dx%d err=%v", c, rw, err)
		case frClose:
			s.closePC()
		case frKill:
			err := windows.TerminateJobObject(s.job, 1)
			s.logf("TerminateJobObject err=%v", err)
		case frQuery:
			s.sendJSON(frPIDs, pidsInfo{PIDs: jobPIDs(s.job)})
		}
	}
}

func (s *session) teardown() {
	if s.hpc != 0 {
		s.closePC()
	}
	if s.job != 0 {
		windows.TerminateJobObject(s.job, 1)
		windows.CloseHandle(s.job)
	}
	for _, h := range []windows.Handle{s.proc, s.ptyIn, s.ptyOut} {
		if h != 0 {
			windows.CloseHandle(h)
		}
	}
	if s.logon != nil {
		_ = setObjectACE(s.winsta, s.logon, winstaAllAccess, false)
		_ = setObjectACE(s.desktop, s.logon, desktopAllAccess, false)
	}
	if s.ticket != "" {
		_, _ = call(launchSvc, request{Op: "release", Ticket: s.ticket})
	}
	if s.profile != 0 {
		if err := unloadUserProfile(s.token, s.profile); err != nil {
			log.Printf("session %s: %v", s.id, err)
		}
	}
	if s.token != 0 {
		s.token.Close()
	}
	s.in.Close()
	s.out.Close()
	log.Printf("session %s: torn down", s.id)
}

func jobPIDs(job windows.Handle) []uint32 {
	const max = 512
	buf := make([]uintptr, 2+max) // two ULONGs pack into the first uintptr on amd64
	err := windows.QueryInformationJobObject(job, windows.JobObjectBasicProcessIdList,
		uintptr(unsafe.Pointer(&buf[0])), uint32(len(buf))*uint32(unsafe.Sizeof(buf[0])), nil)
	if err != nil {
		return nil
	}
	hdr := (*[2]uint32)(unsafe.Pointer(&buf[0]))
	var out []uint32
	for _, p := range buf[1 : 1+int(hdr[1])] {
		out = append(out, uint32(p))
	}
	return out
}

func enablePrivileges(names ...string) error {
	var t windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &t); err != nil {
		return err
	}
	defer t.Close()
	for _, n := range names {
		var luid windows.LUID
		if err := windows.LookupPrivilegeValue(nil, wstr(n), &luid); err != nil {
			return fmt.Errorf("%s: %w", n, err)
		}
		tp := windows.Tokenprivileges{PrivilegeCount: 1}
		tp.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}
		if err := windows.AdjustTokenPrivileges(t, false, &tp, 0, nil, nil); err != nil {
			return fmt.Errorf("%s: %w", n, err)
		}
	}
	// AdjustTokenPrivileges succeeds (ERROR_NOT_ALL_ASSIGNED) when the token
	// does not hold a privilege, so read the token back.
	have, err := tokenPrivileges(t)
	if err != nil {
		return err
	}
	var missing []string
	for _, n := range names {
		if !slices.Contains(have, n+" (enabled)") {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("not held by the token: %v", missing)
	}
	return nil
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

func lookupEnv(env []string, key string) string {
	for _, e := range env {
		if k, v, _ := strings.Cut(e, "="); strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

// tokenEnvironment is CreateEnvironmentBlock for t, without the caller's
// variables.
func tokenEnvironment(t windows.Token) ([]string, error) {
	var block *uint16
	if err := windows.CreateEnvironmentBlock(&block, t, false); err != nil {
		return nil, err
	}
	defer windows.DestroyEnvironmentBlock(block)
	var env []string
	for p := unsafe.Pointer(block); ; {
		s := windows.UTF16PtrToString((*uint16)(p))
		if s == "" {
			return env, nil
		}
		env = append(env, s)
		p = unsafe.Add(p, (len(utf16.Encode([]rune(s)))+1)*2)
	}
}

// profileListEnv is the system environment plus the profile variables of
// t's user, read from HKLM\...\ProfileList (readable by the virtual account).
func profileListEnv(t windows.Token) ([]string, error) {
	u, err := t.GetTokenUser()
	if err != nil {
		return nil, err
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\`+u.User.Sid.String(), registry.QUERY_VALUE)
	if err != nil {
		return nil, fmt.Errorf("ProfileList: %w", err)
	}
	defer k.Close()
	p, _, err := k.GetStringValue("ProfileImagePath")
	if err != nil {
		return nil, err
	}
	p, _ = registry.ExpandString(p)
	env, err := tokenEnvironment(0) // system variables only
	if err != nil {
		return nil, err
	}
	return mergeEnv(env, []string{
		"USERPROFILE=" + p, "APPDATA=" + p + `\AppData\Roaming`, "LOCALAPPDATA=" + p + `\AppData\Local`,
		"HOMEDRIVE=" + p[:2], "HOMEPATH=" + p[2:], "TEMP=" + p + `\AppData\Local\Temp`, "TMP=" + p + `\AppData\Local\Temp`,
		"USERNAME=" + agentUser,
	}), nil
}

func envBlock(env []string) *uint16 {
	var b []uint16
	for _, e := range env {
		b = append(b, utf16.Encode([]rune(e))...)
		b = append(b, 0)
	}
	b = append(b, 0)
	return &b[0]
}

// Launcher side of the token handoff experiment.

type heldLogon struct {
	token   windows.Token
	profile windows.Handle
}

var (
	heldMu  sync.Mutex
	held    = map[string]heldLogon{}
	heldSeq atomic.Int64
)

// logonFor logs on as mestre-agente, loads the profile and duplicates the
// token into process pid (the pipe client). The profile stays loaded until
// release.
func logonFor(pid uint32) response {
	pw, err := readAgentPassword()
	if err != nil {
		return response{Error: err.Error()}
	}
	t, err := logonUser(agentUser, ".", pw, logon32LogonBatch)
	if err != nil {
		return response{Error: err.Error()}
	}
	prof, err := loadUserProfile(t, agentUser)
	if err != nil {
		t.Close()
		return response{Error: err.Error()}
	}
	env, err := tokenEnvironment(t)
	if err != nil {
		return response{Error: "CreateEnvironmentBlock: " + err.Error()}
	}
	p, err := windows.OpenProcess(windows.PROCESS_DUP_HANDLE, false, pid)
	if err != nil {
		return response{Error: "OpenProcess: " + err.Error()}
	}
	defer windows.CloseHandle(p)
	var dup windows.Handle
	if err := windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(t), p, &dup, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return response{Error: "DuplicateHandle: " + err.Error()}
	}
	ticket := fmt.Sprint(heldSeq.Add(1))
	heldMu.Lock()
	held[ticket] = heldLogon{t, prof}
	heldMu.Unlock()
	log.Printf("logon for pid %d: token 0x%x, logon session %s, ticket %s", pid, dup, logonSessionID(t), ticket)
	return response{Token: uint64(dup), Env: env, Ticket: ticket}
}

func releaseLogon(ticket string) {
	heldMu.Lock()
	h, ok := held[ticket]
	delete(held, ticket)
	heldMu.Unlock()
	if ok {
		err := unloadUserProfile(h.token, h.profile)
		h.token.Close()
		log.Printf("release %s: UnloadUserProfile err=%v", ticket, err)
	}
}
