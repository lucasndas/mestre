//go:build windows

package main

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

// The engine stand-in: the MestreSpike service with the virtual account.

// termRec is one row of the spike's "terminals" table (no\terminals.json,
// readable only by the service and SYSTEM).
type termRec struct {
	ID           string `json:"id"`
	Secret       []byte `json:"secret"`
	Sock         string `json:"sock"`
	Harden       bool   `json:"harden,omitempty"`
	HostPID      uint32 `json:"host_pid,omitempty"`
	HostCreated  int64  `json:"host_created,omitempty"`
	LogonSession string `json:"logon_session,omitempty"`
	AgentPID     uint32 `json:"agent_pid,omitempty"`
}

type term struct {
	rec  termRec
	mu   sync.Mutex // one request at a time on the connection
	conn *lineConn
	raw  net.Conn
}

// control is one line of JSON on the control pipe (Administrators only).
type control struct {
	Op       string   `json:"op"`
	ID       string   `json:"id,omitempty"`
	Path     string   `json:"path,omitempty"`
	Args     []string `json:"args,omitempty"`
	Env      []string `json:"env,omitempty"`
	Dir      string   `json:"dir,omitempty"`
	Harden   bool     `json:"harden,omitempty"`
	Data     []byte   `json:"data,omitempty"`
	From     int64    `json:"from,omitempty"`
	PID      uint32   `json:"pid,omitempty"`
	Created  int64    `json:"created,omitempty"`
	Mode     string   `json:"mode,omitempty"`
	Sweep    bool     `json:"sweep,omitempty"`
	Regex    string   `json:"regex,omitempty"`
	Labels   []string `json:"labels,omitempty"`
	TimeoutS float64  `json:"timeout_s,omitempty"`
}

type controlResp struct {
	Error   string                  `json:"error,omitempty"`
	Log     []string                `json:"log,omitempty"`
	Rec     *termRec                `json:"rec,omitempty"`
	Host    *hostResp               `json:"host,omitempty"`
	Terms   []termView              `json:"terms,omitempty"`
	Boot    []string                `json:"boot,omitempty"`
	Matched bool                    `json:"matched,omitempty"`
	Waited  string                  `json:"waited,omitempty"`
	Probes  map[string]*identResult `json:"probes,omitempty"`
}

type termView struct {
	ID          string `json:"id"`
	HostPID     uint32 `json:"host_pid"`
	HostCreated int64  `json:"host_created"`
	AgentPID    uint32 `json:"agent_pid"`
	Connected   bool   `json:"connected"`
	HostAlive   bool   `json:"host_alive"`
}

var (
	termsMu sync.Mutex
	terms   = map[string]*term{}
	bootLog []string

	probesMu      sync.Mutex
	probes        = map[string]*identResult{}
	probesChanged = make(chan struct{})

	engineSID, agentSID *windows.SID
)

type handler struct{}

func runService() error {
	if f, err := os.OpenFile(filepath.Join(logDir, "mestrepty-engine.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		log.SetOutput(f)
	}
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	return svc.Run(serviceName, handler{})
}

func (handler) Execute(_ []string, req <-chan svc.ChangeRequest, st chan<- svc.Status) (bool, uint32) {
	st <- svc.Status{State: svc.StartPending}
	if u, err := windows.GetCurrentProcessToken().GetTokenUser(); err == nil {
		engineSID, _ = u.User.Sid.Copy()
	}
	agentSID, _, _, _ = windows.LookupSID("", agentUser)
	log.Printf("start: pid %d, account %s", windows.GetCurrentProcessId(), accountName(engineSID))
	reconnectAll()
	go serveCLI()
	go serveControl()
	st <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for c := range req {
		switch c.Cmd {
		case svc.Interrogate:
			st <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			// The terminals stay: the engine only closes its connections.
			log.Printf("stop (terminals left running)")
			st <- svc.Status{State: svc.StopPending}
			return false, 0
		}
	}
	return false, 0
}

func bootf(format string, a ...any) {
	s := fmt.Sprintf(format, a...)
	log.Print(s)
	bootLog = append(bootLog, s)
}

func loadState() []termRec {
	b, err := os.ReadFile(stateFile)
	if err != nil {
		return nil
	}
	var recs []termRec
	_ = json.Unmarshal(b, &recs)
	return recs
}

func saveStateLocked() {
	var recs []termRec
	for _, t := range terms {
		recs = append(recs, t.rec)
	}
	slices.SortFunc(recs, func(a, b termRec) int { return strings.Compare(a.ID, b.ID) })
	b, _ := json.MarshalIndent(recs, "", "  ")
	if err := os.WriteFile(stateFile, b, 0o600); err != nil {
		log.Printf("save state: %v", err)
	}
}

// reconnectAll is the engine's return (docs/arquitetura/terminal.md#volta-do-motor).
func reconnectAll() {
	recs := loadState()
	bootf("return: %d terminals in %s", len(recs), stateFile)
	for _, r := range recs {
		t := &term{rec: r}
		start := time.Now()
		var logs []string
		err := t.connect(&logs, 5*time.Second)
		for _, l := range logs {
			bootf("  %s: %s", r.ID, l)
		}
		if err != nil {
			bootf("  %s: not reconnected (%v), in %v: would become interrupted", r.ID, err, time.Since(start).Round(time.Millisecond))
		} else {
			bootf("  %s: reconnected in %v", r.ID, time.Since(start).Round(time.Millisecond))
		}
		termsMu.Lock()
		terms[r.ID] = t
		termsMu.Unlock()
	}
}

// connect dials the ptyhost's socket, checks the peer PID against the
// record and does the handshake.
func (t *term) connect(logs *[]string, d time.Duration) error {
	logf := func(format string, a ...any) { *logs = append(*logs, fmt.Sprintf(format, a...)) }
	if t.rec.HostPID != 0 && !alive(t.rec.HostPID, t.rec.HostCreated) {
		return fmt.Errorf("ptyhost pid %d with creation time %d is not running", t.rec.HostPID, t.rec.HostCreated)
	}
	start := time.Now()
	var c net.Conn
	var err error
	for {
		if c, err = net.Dial("unix", t.rec.Sock); err == nil || time.Since(start) > d {
			break
		}
		// The ptyhost may not be listening yet: bounded retry.
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		return fmt.Errorf("dial %s: %w (tried for %v)", t.rec.Sock, err, time.Since(start).Round(time.Millisecond))
	}
	logf("connected to %s after %v", t.rec.Sock, time.Since(start).Round(time.Millisecond))
	peer, err := peerPID(c)
	if err != nil {
		c.Close()
		return err
	}
	ph, ct, err := openCreated(peer, 0)
	if err != nil {
		c.Close()
		return fmt.Errorf("open peer %d: %w", peer, err)
	}
	windows.CloseHandle(ph)
	if peer != t.rec.HostPID || ct != t.rec.HostCreated {
		c.Close()
		return fmt.Errorf("peer pid %d created %d is not the registered ptyhost (pid %d created %d)", peer, ct, t.rec.HostPID, t.rec.HostCreated)
	}
	logf("SIO_AF_UNIX_GETPEERPID: %d, creation time matches the launcher's answer", peer)
	l := newLineConn(c)
	hpid, err := engineHandshake(l, t.rec.Secret, false)
	if err != nil {
		c.Close()
		return err
	}
	logf("handshake ok (host says pid %d)", hpid)
	t.mu.Lock()
	if t.raw != nil {
		t.raw.Close()
	}
	t.conn, t.raw = l, c
	t.mu.Unlock()
	return nil
}

func (t *term) call(req hostReq, d time.Duration) (*hostResp, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.conn == nil {
		return nil, errors.New("not connected")
	}
	if err := t.conn.send(req); err != nil {
		t.conn = nil
		return nil, err
	}
	var r hostResp
	if err := t.conn.recv(&r, d); err != nil {
		t.conn = nil
		return nil, err
	}
	return &r, nil
}

func findTerm(id string) (*term, error) {
	termsMu.Lock()
	defer termsMu.Unlock()
	t, ok := terms[id]
	if !ok {
		return nil, fmt.Errorf("no terminal %q", id)
	}
	return t, nil
}

var idPattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// openTerminal follows the creation order of docs/arquitetura/terminal.md:
// row, ptyhost, PID, handshake, start.
func openTerminal(c control) (controlResp, error) {
	var resp controlResp
	logf := func(format string, a ...any) { resp.Log = append(resp.Log, fmt.Sprintf(format, a...)) }
	if !idPattern.MatchString(c.ID) {
		return resp, errors.New("bad id")
	}
	if _, err := findTerm(c.ID); err == nil {
		return resp, fmt.Errorf("terminal %q exists", c.ID)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return resp, err
	}
	t := &term{rec: termRec{ID: c.ID, Secret: secret, Sock: sockPath(c.ID), Harden: c.Harden}}
	termsMu.Lock()
	terms[c.ID] = t
	saveStateLocked()
	termsMu.Unlock()
	logf("1. row written (id, socket, secret)")

	start := time.Now()
	var lr launcherResp
	if err := callJSON(launcherPipe, launcherReq{Op: "start-pty", ID: c.ID, Secret: secret, Harden: c.Harden}, &lr); err != nil {
		return resp, err
	}
	if lr.Error != "" {
		return resp, errors.New("launcher: " + lr.Error)
	}
	logf("2. launcher start-pty in %v: ptyhost pid %d, created %d, logon session %s", time.Since(start).Round(time.Millisecond), lr.PID, lr.Created, lr.LogonSession)
	termsMu.Lock()
	t.rec.HostPID, t.rec.HostCreated, t.rec.LogonSession = lr.PID, lr.Created, lr.LogonSession
	saveStateLocked()
	termsMu.Unlock()
	logf("3. pid and creation time written")

	var clog []string
	err := t.connect(&clog, 10*time.Second)
	for _, l := range clog {
		logf("4. %s", l)
	}
	if err != nil {
		return resp, err
	}
	r, err := t.call(hostReq{Op: "start", Path: c.Path, Args: c.Args, Env: c.Env, Dir: c.Dir}, 30*time.Second)
	if err != nil {
		return resp, err
	}
	if r.Error != "" {
		return resp, errors.New("host: " + r.Error)
	}
	termsMu.Lock()
	t.rec.AgentPID = r.PID
	saveStateLocked()
	rec := t.rec
	termsMu.Unlock()
	logf("5. start: agent pid %d", r.PID)
	rec.Secret = nil
	resp.Rec = &rec
	return resp, nil
}

func forget(id string) {
	termsMu.Lock()
	if t, ok := terms[id]; ok {
		t.mu.Lock()
		if t.raw != nil {
			t.raw.Close()
		}
		t.mu.Unlock()
		delete(terms, id)
		saveStateLocked()
	}
	termsMu.Unlock()
	_ = os.Remove(sockPath(id))
}

// killHost is the engine's last resort: TerminateProcess on the ptyhost,
// whose job then closes (KILL_ON_JOB_CLOSE).
func killHost(t *term) (string, error) {
	h, ct, err := openCreated(t.rec.HostPID, windows.PROCESS_TERMINATE|windows.SYNCHRONIZE)
	if err != nil {
		return "", fmt.Errorf("OpenProcess(PROCESS_TERMINATE): %w", err)
	}
	defer windows.CloseHandle(h)
	if ct != t.rec.HostCreated {
		return "", errors.New("PID reused")
	}
	if err := windows.TerminateProcess(h, 1); err != nil {
		return "", err
	}
	start := time.Now()
	ev, _ := windows.WaitForSingleObject(h, 5000)
	return fmt.Sprintf("TerminateProcess ok, exited=%v after %v (deadline 5s)", ev == windows.WAIT_OBJECT_0, time.Since(start).Round(time.Millisecond)), nil
}

// badSecret plays an engine with the wrong secret on a new connection.
func badSecret(t *term) []string {
	var out []string
	c, err := net.Dial("unix", t.rec.Sock)
	if err != nil {
		return []string{"dial: " + err.Error()}
	}
	defer c.Close()
	wrong := make([]byte, 32)
	_, _ = rand.Read(wrong)
	l := newLineConn(c)
	_, err = engineHandshake(l, wrong, true)
	out = append(out, fmt.Sprintf("handshake with a wrong secret: %v", err))
	// Whatever happened, try a request on the same connection.
	_ = l.send(hostReq{Op: "screen"})
	var r hostResp
	rerr := l.recv(&r, 3*time.Second)
	out = append(out, fmt.Sprintf("screen request after it: err=%v, got %d chars of screen", rerr, len(r.Text)))
	return out
}

func waitScreen(t *term, re *regexp.Regexp, d time.Duration) (string, bool, time.Duration) {
	start := time.Now()
	for {
		r, err := t.call(hostReq{Op: "screen"}, 10*time.Second)
		text := ""
		if err == nil {
			text = r.Text
		}
		if re.MatchString(text) {
			return text, true, time.Since(start)
		}
		if err != nil || time.Since(start) > d {
			return text, false, time.Since(start)
		}
		// Bounded poll of the screen (the spike has no "stable" event).
		time.Sleep(200 * time.Millisecond)
	}
}

func serveControl() {
	for {
		h, err := newPipe(controlPipe, "D:P(A;;GA;;;SY)(A;;GA;;;BA)")
		if err != nil {
			log.Printf("control pipe: %v", err)
			return
		}
		if err := windows.ConnectNamedPipe(h, nil); err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
			windows.CloseHandle(h)
			continue
		}
		go func() {
			f := os.NewFile(uintptr(h), "control")
			defer f.Close()
			handleControl(f)
			_ = windows.FlushFileBuffers(h)
			_ = windows.DisconnectNamedPipe(h)
		}()
	}
}

func handleControl(f *os.File) {
	line, err := bufio.NewReader(f).ReadBytes('\n')
	if err != nil {
		return
	}
	var c control
	var resp controlResp
	if err := json.Unmarshal(line, &c); err != nil {
		resp.Error = err.Error()
	} else {
		log.Printf("control: op %s id %s", c.Op, c.ID)
		if err := doControl(c, &resp); err != nil {
			resp.Error = err.Error()
		}
	}
	b, _ := json.Marshal(resp)
	_, _ = f.Write(append(b, '\n'))
}

func doControl(c control, resp *controlResp) error {
	d := time.Duration(c.TimeoutS * float64(time.Second))
	switch c.Op {
	case "open":
		r, err := openTerminal(c)
		*resp = r
		return err
	case "list":
		termsMu.Lock()
		for _, t := range terms {
			t.mu.Lock()
			resp.Terms = append(resp.Terms, termView{ID: t.rec.ID, HostPID: t.rec.HostPID, HostCreated: t.rec.HostCreated,
				AgentPID: t.rec.AgentPID, Connected: t.conn != nil, HostAlive: alive(t.rec.HostPID, t.rec.HostCreated)})
			t.mu.Unlock()
		}
		termsMu.Unlock()
		resp.Boot = bootLog
		return nil
	case "probes":
		resp.Probes, resp.Waited = waitProbes(c.Labels, d)
		return nil
	case "clearprobes":
		probesMu.Lock()
		probes = map[string]*identResult{}
		probesMu.Unlock()
		return nil
	}
	t, err := findTerm(c.ID)
	if err != nil {
		return err
	}
	switch c.Op {
	case "screen", "input", "replay", "status", "injob", "stop":
		r, err := t.call(hostReq{Op: c.Op, Data: c.Data, From: c.From, PID: c.PID, Created: c.Created, Mode: c.Mode, Sweep: c.Sweep}, 60*time.Second)
		if err != nil {
			return err
		}
		resp.Host = r
		if c.Op == "stop" && c.Mode == "kill" {
			forget(c.ID)
		}
	case "waitscreen":
		re, err := regexp.Compile(c.Regex)
		if err != nil {
			return err
		}
		text, ok, waited := waitScreen(t, re, d)
		resp.Host, resp.Matched, resp.Waited = &hostResp{Text: text}, ok, waited.Round(time.Millisecond).String()
	case "killhost":
		s, err := killHost(t)
		resp.Log = []string{s}
		if err == nil {
			forget(c.ID)
		}
		return err
	case "badsecret":
		resp.Log = badSecret(t)
	case "forget":
		forget(c.ID)
	default:
		return fmt.Errorf("unknown op %q", c.Op)
	}
	return nil
}

// Launcher protocol (tools/winsandbox/spikes/01-agent-service).

type launcherReq struct {
	Op     string `json:"op"`
	ID     string `json:"id,omitempty"`
	Secret []byte `json:"secret,omitempty"`
	Harden bool   `json:"harden,omitempty"`
}

type launcherResp struct {
	Error        string `json:"error,omitempty"`
	PID          uint32 `json:"pid,omitempty"`
	Created      int64  `json:"created,omitempty"`
	LogonSession string `json:"logon_session,omitempty"`
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
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("connect to %s: %w", name, err)
		}
		switch {
		case errors.Is(err, windows.ERROR_PIPE_BUSY):
			waitNamedPipe(name, time.Second)
		case errors.Is(err, windows.ERROR_FILE_NOT_FOUND):
			// Between two clients the server may not have the next instance
			// yet: bounded retry.
			time.Sleep(20 * time.Millisecond)
		default:
			return nil, fmt.Errorf("connect to %s: %w", name, err)
		}
	}
}

func newPipe(name, sddl string) (windows.Handle, error) {
	// The service's own SID: new instances of a pipe need
	// FILE_CREATE_PIPE_INSTANCE on the first instance's DACL.
	if engineSID != nil {
		sddl += "(A;;GA;;;" + engineSID.String() + ")"
	}
	sa, err := securityAttributes(sddl)
	if err != nil {
		return 0, err
	}
	return windows.CreateNamedPipe(wstr(name), windows.PIPE_ACCESS_DUPLEX,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		windows.PIPE_UNLIMITED_INSTANCES, 64<<10, 64<<10, 0, sa)
}
