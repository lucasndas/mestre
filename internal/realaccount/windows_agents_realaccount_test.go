//go:build realaccount && windows

package realaccount_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"

	"github.com/lucasndas/mestre/internal/launcher"
	"github.com/lucasndas/mestre/internal/realaccount"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/internal/winsvc"
	"github.com/lucasndas/mestre/internal/winsvc/dpapi"
	"github.com/lucasndas/mestre/internal/winsvc/logonsweep"
)

// The Windows side, part one: the services and the agents. The test binary is
// started by the service manager twice, with the arguments tools/realaccount
// gave the service: as the mestre-launcher (LocalSystem, the account of the
// installation) and as the engine (the virtual account NT SERVICE\Mestre$ci,
// which runs the tests). What the agents do runs in a terminal, as the agent
// account, with its own logon.

// envStage is where the tool put what the tests run.
const envStage = "MESTRE_RA_STAGE"

func stage() string {
	if s := os.Getenv(envStage); s != "" {
		return s
	}
	return realaccount.WindowsStage
}

func leaveSessionAttr() *syscall.SysProcAttr { return nil }

// engineJob is the Job Object the engine joins at the start, allowing
// breakaway: a mestre-pty that is not in it proves the launcher took it out.
var engineJob windows.Handle

func osMain(m *testing.M, args []string) (bool, int) {
	switch args[0] {
	case "service-engine": // <stage> <test flags...>
		return true, runService(func(_ context.Context) int { return runEngine(m, args[1:]) })
	case "service-launcher": // <config file>
		return true, runService(func(ctx context.Context) int { return runLauncher(ctx, args[1]) })
	}
	return false, 0
}

// runService runs f as a Windows service until it ends (its code is the exit
// code of the service) or the service manager stops it.
func runService(f func(context.Context) int) int {
	if err := svc.Run("", handler{run: f}); err != nil {
		fmt.Fprintln(os.Stderr, "service:", err)
		return 3
	}
	return 0
}

type handler struct{ run func(context.Context) int }

func (h handler) Execute(_ []string, req <-chan svc.ChangeRequest, st chan<- svc.Status) (bool, uint32) {
	st <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- h.run(ctx) }()
	st <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				st <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				st <- svc.Status{State: svc.StopPending}
				cancel()
				return false, 0
			}
		case code := <-done:
			st <- svc.Status{State: svc.StopPending}
			return code != 0, uint32(code)
		}
	}
}

// runEngine runs the tests as the engine service: the gate and the role come
// from the service's command line (a service has no environment of its own),
// the output goes to a log the tool reads, and the process joins a Job of its
// own.
func runEngine(m *testing.M, args []string) int {
	dir := args[0]
	_ = os.Setenv(realaccount.EnvDisposable, "1")
	_ = os.Setenv(envRole, "engine")
	_ = os.Setenv(envStage, dir)
	log, err := os.Create(filepath.Join(dir, "ci", "engine.log"))
	if err != nil {
		return 3
	}
	defer log.Close()
	os.Stdout, os.Stderr = log, log
	os.Args = append([]string{os.Args[0]}, args[1:]...)
	if job, err := windows.CreateJobObject(nil, nil); err == nil {
		var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
		if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err == nil &&
			windows.AssignProcessToJobObject(job, windows.CurrentProcess()) == nil {
			engineJob = job
		}
	}
	code := m.Run()
	_ = os.WriteFile(filepath.Join(dir, "ci", "engine.exit"), []byte(strconv.Itoa(code)), 0o600)
	return code
}

// runLauncher is the mestre-launcher service: winsvc.NewLauncher with the real
// agent account, whose password the tool kept the way the installation does
// (machine-scope DPAPI, a file for SYSTEM alone), and the engine of the
// instance as the only client.
func runLauncher(ctx context.Context, path string) int {
	fail := func(err error) int {
		_ = os.WriteFile(path+".err", []byte(err.Error()), 0o600)
		return 1
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return fail(err)
	}
	var cfg realaccount.WindowsLauncherConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return fail(err)
	}
	// CA-TERM-15: the refusal of the account of the launcher itself is checked
	// where it matters, in a service as LocalSystem, which the tests of the
	// package are not: the agents would run as SYSTEM.
	if _, err := (winsvc.SelfAccount{}).Logon("probe"); !errors.Is(err, winsvc.ErrSelfAccountInService) {
		return fail(fmt.Errorf("the account of the launcher itself logged on in a service: %v", err))
	}
	if _, err := winsvc.NewLauncher(winsvc.LauncherConfig{Account: winsvc.SelfAccount{}}); !errors.Is(err, winsvc.ErrSelfAccountInService) {
		return fail(fmt.Errorf("a launcher was built over the account of the launcher itself in a service: %v", err))
	}
	if f, err := os.OpenFile(filepath.Join(cfg.LogDir, "launcher.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		defer f.Close()
		os.Stderr = f
	}
	policy, err := winsvc.EnginePolicy(cfg.Instance)
	if err != nil {
		return fail(err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fail(err)
	}
	l, err := winsvc.NewLauncher(winsvc.LauncherConfig{
		Pipe: winsvc.PipeName(cfg.Instance), BinDir: cfg.BinDir, SocketDir: cfg.SocketDir, StateDir: cfg.StateDir, LogDir: cfg.LogDir,
		Engine: policy,
		Account: &winsvc.AgentAccount{
			User: cfg.AgentUser, Engine: policy.SID,
			Password: func() (string, error) {
				blob, err := dpapi.ReadFile(cfg.PasswordFile, system, 4096)
				if err != nil {
					return "", err
				}
				pw, err := dpapi.Unprotect(blob, realaccount.PasswordLabel)
				return string(pw), err
			},
		},
		Audit:  func(e launcher.Event) { fmt.Fprintf(os.Stderr, "audit: %+v\n", e) },
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	})
	if err != nil {
		return fail(err)
	}
	if err := l.Serve(ctx); err != nil {
		return fail(err)
	}
	return 0
}

// openResult opens the process for mask and names the outcome: "ok", "denied"
// or the error.
func openResult(pid int, mask uint32) string {
	h, err := windows.OpenProcess(mask, false, uint32(pid))
	if err == nil {
		_ = windows.CloseHandle(h)
		return "ok"
	}
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return "denied"
	}
	return err.Error()
}

// processAccess are the rights the isolation between terminals is about.
var processAccess = []struct {
	name string
	mask uint32
}{
	{"query", windows.PROCESS_QUERY_LIMITED_INFORMATION},
	{"wait", windows.SYNCHRONIZE},
	{"read memory", windows.PROCESS_VM_READ},
	{"write memory", windows.PROCESS_VM_WRITE | windows.PROCESS_VM_OPERATION},
	{"duplicate handles", windows.PROCESS_DUP_HANDLE},
	{"create thread", windows.PROCESS_CREATE_THREAD},
	{"terminate", windows.PROCESS_TERMINATE},
	{"change the DACL", windows.WRITE_DAC},
}

// osAgent has the agent modes of Windows.
func osAgent(args []string) int {
	switch args[0] {
	case "access": // <report> <name=pid>...: what this terminal may do to those processes, and to its own
		return runAccessProbe(args[1], args[2:])
	case "acls": // <report> <socket of another terminal> <folder of the sockets> <cli.sock> <state folder>
		return runACLProbe(args[1:])
	case "whoami": // <report>
		return runWhoami(args[1])
	case "wmi": // <addr>: starts a process through WMI, which escapes the Job Object
		return runWMI(args[1])
	case "hold": // <addr>: told to the engine, then waits for it to go away
		return runHold(args[1])
	case "readfile": // <report> <path>
		return runReadFile(args[1], args[2])
	}
	return 2
}

func writeReport(path string, rep map[string]string) int {
	b, err := json.Marshal(rep)
	if err != nil || writeAtomic(path, b) != nil {
		return 1
	}
	fmt.Print("DONE\r\n")
	block()
	return 0
}

// runAccessProbe is the agent of CA-TERM-12: it opens, with each right, the
// processes of another terminal, its own mestre-pty (its parent) and a child
// of its own, which is the control: processes of the same terminal share the
// logon SID, which the default DACL of the token gives everything.
func runAccessProbe(report string, targets []string) int {
	self, err := os.Executable()
	if err != nil {
		return 1
	}
	child := exec.Command(self, helperAgent, "sleeper", "quiet")
	if err := child.Start(); err != nil {
		return 1
	}
	defer func() { _ = child.Process.Kill() }()
	pids := map[string]int{"own mestre-pty": os.Getppid(), "own child": child.Process.Pid}
	for _, t := range targets {
		name, v, ok := strings.Cut(t, "=")
		pid, err := strconv.Atoi(v)
		if !ok || err != nil {
			return 2
		}
		pids[name] = pid
	}
	rep := map[string]string{}
	for name, pid := range pids {
		for _, a := range processAccess {
			rep[name+"/"+a.name] = openResult(pid, a.mask)
		}
	}
	return writeReport(report, rep)
}

// runACLProbe is the agent of the ACLs of the sockets and folders of the
// engine: what the agent account can reach from a terminal that is not the
// owner of the socket.
func runACLProbe(args []string) int {
	report := args[0]
	rep := map[string]string{}
	dial := func(path string) string {
		c, err := net.Dial("unix", path)
		if err != nil {
			return denialOf(err)
		}
		_ = c.Close()
		return "ok"
	}
	rep["socket of another terminal"] = dial(args[1])
	_, err := os.ReadDir(args[2])
	rep["list the folder of the sockets"] = denialOf(err)
	rep["delete the socket of another terminal"] = denialOf(os.Remove(args[1]))
	rep["cli.sock"] = dial(args[3])
	_, err = os.ReadDir(args[4])
	rep["list the folder of the engine"] = denialOf(err)
	f, err := os.OpenFile(winsvc.PipeName(realaccount.Instance), os.O_RDWR, 0)
	if err == nil {
		_ = f.Close()
	}
	rep["pipe of the launcher"] = denialOf(err)
	return writeReport(report, rep)
}

func denialOf(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, windows.ERROR_ACCESS_DENIED), errors.Is(err, windows.WSAEACCES), errors.Is(err, os.ErrPermission):
		return "denied"
	}
	return err.Error()
}

// runWhoami reports what a process of the terminal sees of itself: the user
// and the logon session of its own token and of its mestre-pty's (its parent,
// which its owner may query), the profile folder of the environment block and
// the user hive the launcher loaded.
func runWhoami(report string) int {
	rep := map[string]string{"USERPROFILE": os.Getenv("USERPROFILE"), "APPDATA": os.Getenv("APPDATA")}
	describe := func(prefix string, t windows.Token) {
		if u, err := t.GetTokenUser(); err == nil {
			rep[prefix+"user"] = u.User.Sid.String()
		}
		if s, err := logonsweep.SessionOfToken(t); err == nil {
			rep[prefix+"logon session"] = fmt.Sprintf("%#x", uint64(s.ID))
			rep[prefix+"logon type"] = strconv.Itoa(int(s.LogonType))
		}
	}
	describe("", windows.GetCurrentProcessToken())
	if t, err := procToken(os.Getppid()); err == nil {
		describe("mestre-pty ", t)
		_ = t.Close()
	} else {
		rep["mestre-pty token"] = denialOf(err)
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software`, registry.QUERY_VALUE)
	if err == nil {
		_ = k.Close()
	}
	rep["HKCU"] = denialOf(err)
	_, err = os.Stat(os.Getenv("USERPROFILE"))
	rep["profile folder"] = denialOf(err)
	return writeReport(report, rep)
}

// runWMI starts a holder through WMI (Win32_Process.Create), which creates the
// process outside the Job Object of the terminal, and says what WMI answered.
func runWMI(addr string) int {
	self, err := os.Executable()
	if err != nil {
		return 1
	}
	cmdline := fmt.Sprintf(`"%s" %s hold %s`, self, helperAgent, addr)
	script := "$r = Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{CommandLine='" +
		strings.ReplaceAll(cmdline, "'", "''") + "'}; 'WMI ' + $r.ReturnValue"
	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	fmt.Printf("%s\r\n", strings.TrimSpace(string(out)))
	if err != nil {
		fmt.Printf("WMIFAIL %v\r\n", err)
		return 1
	}
	fmt.Print("READY\r\n")
	block()
	return 0
}

// runHold connects to the engine, says its PID and start identity, and waits
// until the connection is closed or the process is ended.
func runHold(addr string) int {
	c, err := net.Dial("tcp", addr)
	if err != nil {
		return 1
	}
	defer c.Close()
	start, err := terminal.NewSystem().Identify(os.Getpid())
	if err != nil {
		return 1
	}
	if _, err := fmt.Fprintf(c, "%d %s\n", os.Getpid(), start); err != nil {
		return 1
	}
	_, _ = bufio.NewReader(c).ReadByte()
	return 0
}

func runReadFile(report, path string) int {
	_, err := os.ReadFile(path)
	return writeReport(report, map[string]string{"read": denialOf(err)})
}

// procToken opens the token of a process of any terminal, with the right the
// engine has over them (the query right of the default DACL).
func procToken(pid int) (windows.Token, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0, err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var t windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &t); err != nil {
		return 0, err
	}
	return t, nil
}

// agentSID is the SID of the agent account.
func agentSID(t *testing.T) *windows.SID {
	t.Helper()
	sid, _, _, err := windows.LookupSID("", realaccount.WindowsAgentUser)
	if err != nil {
		t.Fatal(err)
	}
	return sid
}
