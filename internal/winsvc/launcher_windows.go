//go:build windows

package winsvc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/lucasndas/mestre/internal/launcher"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The mestre-launcher on Windows (docs/arquitetura/windows.md#iniciador-mestre-launcher):
// a service that starts the mestre-pty of each terminal with the token of
// the agent account, outside every Job Object of the engine, and releases
// the logon session and the profile when the mestre-pty ends. The protocol,
// the checks of the requests and the record of them are internal/launcher's;
// this file is what is Windows.

const (
	// processSDDL is the descriptor of the mestre-pty: SYSTEM everything, the
	// engine terminates, queries and waits, the owner (the agent account)
	// only queries (OWNER RIGHTS removes the implicit READ_CONTROL and
	// WRITE_DAC), so another agent can neither open it to read its memory
	// nor change who may.
	processSDDL = "D:P(A;;GA;;;SY)(A;;0x101001;;;%s)(A;;0x101000;;;OW)"
	threadSDDL  = "D:P(A;;GA;;;SY)(A;;0x100800;;;%s)(A;;0x100800;;;OW)"
)

// ServiceName is the Windows service of the engine of an instance
// (docs/arquitetura/componentes.md#instâncias).
func ServiceName(instance string) string {
	if instance == "" || instance == "principal" {
		return "Mestre"
	}
	return "Mestre$" + instance
}

// ClientPolicy accepts only the engine of the instance: the user of the
// process on the other end of the pipe must be its account, and the process
// the one the service manager has for the service. Both are read at each
// connection.
type ClientPolicy struct {
	// SID is the account of the engine (NT SERVICE\Mestre).
	SID *windows.SID
	// ServicePID returns the PID of the engine according to the service
	// manager (see SCMServicePID); nil skips the check (the tests give the
	// PID of the test process).
	ServicePID func() (uint32, error)
}

// EnginePolicy is the policy of the real service: the virtual account of the
// service of the instance, and the PID the SCM reports.
func EnginePolicy(instance string) (ClientPolicy, error) {
	name := ServiceName(instance)
	sid, _, _, err := windows.LookupSID("", `NT SERVICE\`+name)
	if err != nil {
		return ClientPolicy{}, fmt.Errorf("account of service %s: %w", name, err)
	}
	return ClientPolicy{SID: sid, ServicePID: SCMServicePID(name)}, nil
}

// SCMServicePID returns the function that asks the service manager for the
// PID of the service name.
func SCMServicePID(name string) func() (uint32, error) {
	return func() (uint32, error) {
		m, err := mgr.Connect()
		if err != nil {
			return 0, fmt.Errorf("service manager: %w", err)
		}
		defer func() { _ = m.Disconnect() }()
		s, err := m.OpenService(name)
		if err != nil {
			return 0, fmt.Errorf("service %s: %w", name, err)
		}
		defer s.Close()
		st, err := s.Query()
		if err != nil {
			return 0, fmt.Errorf("service %s: %w", name, err)
		}
		return st.ProcessId, nil
	}
}

// Check is the launcher.PeerCheck of the pipe.
func (p ClientPolicy) Check(c net.Conn) (string, error) {
	pc, ok := c.(*pipeConn)
	if !ok {
		return "?", fmt.Errorf("%T is not a pipe of the launcher", c)
	}
	pid, err := pc.ClientPID()
	if err != nil {
		return "?", err
	}
	who := fmt.Sprintf("pid=%d", pid)
	sid, err := tokenUser(pid)
	if err != nil {
		return who, err
	}
	who += " user=" + accountName(sid)
	if p.SID == nil || !sid.Equals(p.SID) {
		return who, fmt.Errorf("the user of process %d is not the account of the engine", pid)
	}
	if p.ServicePID != nil {
		want, err := p.ServicePID()
		if err != nil {
			return who, err
		}
		if pid != want {
			return who, fmt.Errorf("process %d is not the engine service (pid %d)", pid, want)
		}
	}
	return who, nil
}

// LauncherConfig configures a Launcher.
type LauncherConfig struct {
	// Pipe is \\.\pipe\mestre-<inst>-launcher (see PipeName).
	Pipe string
	// BinDir is the folder of installed binaries, <version>\mestre-pty.exe.
	BinDir string
	// SocketDir is where the mestre-pty create their sockets.
	SocketDir string
	// StateDir keeps the list of live mestre-pty the launcher takes back
	// after a restart; ACL for SYSTEM only (the installer's).
	StateDir string
	// LogDir, when set, receives the stderr of each mestre-pty as <id>.log.
	LogDir string
	// WorkDir is the folder the mestre-pty starts in (default %SystemRoot%).
	WorkDir string
	Engine  ClientPolicy
	Account Account
	// Ended is called after the session of a mestre-pty was released because
	// it ended (the profile unloaded, the logon session closed).
	Ended  func(id string)
	Audit  func(launcher.Event)
	Logger *slog.Logger
	Clock  clock.Clock
}

// Launcher is the mestre-launcher of an instance.
type Launcher struct {
	srv *launcher.Server
	ln  net.Listener
	be  *backend
}

// NewLauncher opens the pipe, takes back the mestre-pty that survived the
// previous launcher and returns the launcher, ready to Serve.
func NewLauncher(cfg LauncherConfig) (*Launcher, error) {
	if cfg.Account == nil || cfg.Engine.SID == nil {
		return nil, errors.New("launcher: no agent account or no engine account")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	list, err := launcher.OpenPTYList(filepath.Join(cfg.StateDir, "launcher-ptys.json"))
	if err != nil {
		cfg.Logger.Warn("list of mestre-pty unreadable: starting with none", "error", err.Error())
	}
	stop, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return nil, fmt.Errorf("CreateEvent: %w", err)
	}
	be := &backend{cfg: cfg, list: list, stop: stop, engine: cfg.Engine.SID, log: cfg.Logger}
	be.workDir = cfg.WorkDir
	if be.workDir == "" {
		be.workDir = os.Getenv("SystemRoot")
	}
	srv, err := launcher.NewServer(launcher.Config{
		Backend: be, Check: cfg.Engine.Check, BinDir: cfg.BinDir, SocketDir: cfg.SocketDir, SecretInline: true,
		Audit: cfg.Audit, Logger: cfg.Logger, Clock: cfg.Clock,
	})
	if err != nil {
		_ = windows.CloseHandle(stop)
		return nil, err
	}
	sddl, err := pipeSDDL(cfg.Engine.SID)
	if err != nil {
		_ = windows.CloseHandle(stop)
		return nil, err
	}
	ln, err := ListenPipe(cfg.Pipe, sddl)
	if err != nil {
		_ = windows.CloseHandle(stop)
		return nil, err
	}
	be.adopt()
	return &Launcher{srv: srv, ln: ln, be: be}, nil
}

// pipeSDDL lets SYSTEM and the launcher's own account (which create the
// instances) do everything and the engine only read and write.
func pipeSDDL(engine *windows.SID) (string, error) {
	self, err := currentUserSID()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("D:P(A;;GA;;;SY)(A;;GA;;;%s)(A;;0x%x;;;%s)", self, pipeClientAccess, engine), nil
}

// Serve answers the engine until ctx ends. The mestre-pty it started go on;
// it only stops watching them (the next launcher takes them back).
func (l *Launcher) Serve(ctx context.Context) error {
	err := l.srv.Serve(ctx, l.ln)
	l.be.shutdown()
	return err
}

// Started is how many mestre-pty this launcher created.
func (l *Launcher) Started() int { return int(l.be.started.Load()) }

// Adopted lists the mestre-pty taken back from the previous launcher.
func (l *Launcher) Adopted() []launcher.PTYEntry { return l.be.adopted }

// backend is the launcher.Backend of Windows.
type backend struct {
	cfg     LauncherConfig
	list    *launcher.PTYList
	engine  *windows.SID
	workDir string
	log     *slog.Logger

	stop    windows.Handle // set when the launcher stops: watchers let go, release nothing
	wg      sync.WaitGroup
	started atomic.Int32
	adopted []launcher.PTYEntry
	once    sync.Once
}

var _ launcher.Backend = (*backend)(nil)

// Kill does nothing: the mestre-pty ends its own Job Object, and the engine
// ends the mestre-pty (it holds the right to) when it does not answer.
func (b *backend) Kill(context.Context, string) error { return nil }

// Start creates the mestre-pty with the token of the agent account.
func (b *backend) Start(_ context.Context, r launcher.StartRequest) (launcher.Started, error) {
	sess, err := b.cfg.Account.Logon(r.ID)
	if err != nil {
		return launcher.Started{}, err
	}
	proc, pid, err := b.create(sess, r)
	if err != nil {
		sess.Release()
		return launcher.Started{}, err
	}
	start, _, err := creationTime(proc)
	if err != nil {
		_ = windows.TerminateProcess(proc, 1)
		_ = windows.CloseHandle(proc)
		sess.Release()
		return launcher.Started{}, fmt.Errorf("creation time of %d: %w", pid, err)
	}
	b.started.Add(1)
	if err := b.list.Add(launcher.PTYEntry{ID: r.ID, PID: int(pid), Start: start}); err != nil {
		b.log.Warn("list of mestre-pty not written", "terminal", r.ID, "error", err.Error())
	}
	b.watch(r.ID, proc, sess)
	return launcher.Started{PID: int(pid), Start: start}, nil
}

// create is CreateProcessAsUser with everything the docs list: outside any
// Job (CREATE_BREAKAWAY_FROM_JOB), detached from any console, the security
// descriptors above, and the secret on the read end of a pipe that is the
// only handle inherited (stdin, and the value in --secret-fd).
func (b *backend) create(sess *Session, r launcher.StartRequest) (windows.Handle, uint32, error) {
	var rd, wr windows.Handle
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	if err := windows.CreatePipe(&rd, &wr, &sa, 0); err != nil {
		return 0, 0, fmt.Errorf("CreatePipe: %w", err)
	}
	defer func() { _ = windows.CloseHandle(rd) }()
	if err := windows.SetHandleInformation(wr, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		_ = windows.CloseHandle(wr)
		return 0, 0, err
	}
	var n uint32
	err := windows.WriteFile(wr, r.Secret, &n, nil)
	_ = windows.CloseHandle(wr)
	if err != nil {
		return 0, 0, fmt.Errorf("write the secret: %w", err)
	}

	inherit := []windows.Handle{rd}
	si := windows.StartupInfoEx{}
	si.Flags = windows.STARTF_USESTDHANDLES
	si.StdInput = rd
	if b.cfg.LogDir != "" {
		lf, err := os.OpenFile(filepath.Join(b.cfg.LogDir, r.ID+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return 0, 0, fmt.Errorf("log of the mestre-pty: %w", err)
		}
		defer lf.Close()
		lh := windows.Handle(lf.Fd())
		if err := windows.SetHandleInformation(lh, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			return 0, 0, err
		}
		si.StdErr = lh
		inherit = append(inherit, lh)
	}
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return 0, 0, err
	}
	defer attrs.Delete()
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&inherit[0]), uintptr(len(inherit))*unsafe.Sizeof(inherit[0])); err != nil {
		return 0, 0, fmt.Errorf("UpdateProcThreadAttribute(HANDLE_LIST): %w", err)
	}
	si.ProcThreadAttributeList = attrs.List()
	si.Cb = uint32(unsafe.Sizeof(si))

	psa, err := securityAttributes(fmt.Sprintf(processSDDL, b.engine))
	if err != nil {
		return 0, 0, err
	}
	tsa, err := securityAttributes(fmt.Sprintf(threadSDDL, b.engine))
	if err != nil {
		return 0, 0, err
	}
	argv := []string{r.Binary, "--id", r.ID, "--socket", r.Socket, "--secret-fd", strconv.FormatUint(uint64(rd), 10)}
	flags := uint32(windows.CREATE_BREAKAWAY_FROM_JOB | windows.CREATE_UNICODE_ENVIRONMENT |
		windows.EXTENDED_STARTUPINFO_PRESENT | windows.DETACHED_PROCESS)
	var pi windows.ProcessInformation
	app, cmdline := wstr(r.Binary), wstr(windows.ComposeCommandLine(argv))
	if sess.Token != 0 {
		err = windows.CreateProcessAsUser(sess.Token, app, cmdline, psa, tsa, true, flags, envBlock(sess.Env), wstr(b.workDir), &si.StartupInfo, &pi)
	} else {
		err = windows.CreateProcess(app, cmdline, psa, tsa, true, flags, envBlock(sess.Env), wstr(b.workDir), &si.StartupInfo, &pi)
	}
	if err != nil {
		return 0, 0, fmt.Errorf("create %s: %w", r.Binary, err)
	}
	_ = windows.CloseHandle(pi.Thread)
	return pi.Process, pi.ProcessId, nil
}

// watch releases the session when the mestre-pty ends. When the launcher
// stops first it lets go and releases nothing: the process is still there.
func (b *backend) watch(id string, proc windows.Handle, sess *Session) {
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		defer func() { _ = windows.CloseHandle(proc) }()
		which, err := windows.WaitForMultipleObjects([]windows.Handle{proc, b.stop}, false, windows.INFINITE)
		if err == nil && which != windows.WAIT_OBJECT_0 {
			return
		}
		sess.Release()
		if err := b.list.Remove(id); err != nil {
			b.log.Warn("list of mestre-pty not written", "terminal", id, "error", err.Error())
		}
		b.log.Info("mestre-pty ended; profile and logon session released", "terminal", id)
		if b.cfg.Ended != nil {
			b.cfg.Ended(id)
		}
	}()
}

// adopt takes back the mestre-pty of the list that are still the same
// process (PID and creation time), and drops the others: a PID that now
// belongs to something else is never touched.
func (b *backend) adopt() {
	for _, e := range b.list.Entries() {
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(e.PID))
		if err != nil {
			b.log.Info("mestre-pty of the list is gone", "terminal", e.ID, "pid", e.PID)
			_ = b.list.Remove(e.ID)
			continue
		}
		start, running, err := creationTime(h)
		if err != nil || !running || start != e.Start {
			_ = windows.CloseHandle(h)
			b.log.Info("PID of the list is not the mestre-pty any more", "terminal", e.ID, "pid", e.PID)
			_ = b.list.Remove(e.ID)
			continue
		}
		sess, err := b.cfg.Account.Adopt(e.ID, e.PID)
		if err != nil {
			b.log.Warn("cannot take back the session of a mestre-pty", "terminal", e.ID, "error", err.Error())
			sess = &Session{Release: func() {}}
		}
		b.adopted = append(b.adopted, e)
		b.watch(e.ID, h, sess)
	}
}

// shutdown stops the watchers without releasing anything.
func (b *backend) shutdown() {
	b.once.Do(func() {
		_ = windows.SetEvent(b.stop)
		b.wg.Wait()
		_ = windows.CloseHandle(b.stop)
	})
}
