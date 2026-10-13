//go:build windows

package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/lucasndas/mestre/internal/realaccount"
	"github.com/lucasndas/mestre/internal/winsvc"
	"github.com/lucasndas/mestre/internal/winsvc/dpapi"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The Windows machine: the local account of the agents, with the rights the
// installation gives it; the launcher as a LocalSystem service and the engine
// as a service under its virtual account (NT SERVICE\Mestre$ci), both the
// test binary, started by the service manager; the password of the agents'
// account in the file of the installation (machine-scope DPAPI, SYSTEM alone).
// The tests run as the engine service, and their output goes to its log.

var (
	launcherService = "MestreLauncher$" + realaccount.Instance
	engineService   = winsvc.ServiceName(realaccount.Instance)
)

// icacls grants and removes with the tool of the system.
func icacls(ctx context.Context, out io.Writer, path string, args ...string) error {
	return runCmd(ctx, out, nil, "icacls", append([]string{path}, args...)...)
}

const (
	sidSystem = "*S-1-5-18"
	sidAdmins = "*S-1-5-32-544"
	sidUsers  = "*S-1-5-32-545"
	sidAll    = "*S-1-1-0"
)

// password is 32 characters of the four classes, as the installation makes it.
func password() (string, error) {
	const (
		lower   = "abcdefghijkmnopqrstuvwxyz"
		upper   = "ABCDEFGHJKLMNPQRSTUVWXYZ"
		digits  = "23456789"
		symbols = "!#$-_=+"
	)
	all := lower + upper + digits + symbols
	pick := func(set string) (byte, error) {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(set))))
		if err != nil {
			return 0, err
		}
		return set[n.Int64()], nil
	}
	var pw []byte
	for _, set := range []string{lower, upper, digits, symbols} {
		c, err := pick(set)
		if err != nil {
			return "", err
		}
		pw = append(pw, c)
	}
	for len(pw) < 32 {
		c, err := pick(all)
		if err != nil {
			return "", err
		}
		pw = append(pw, c)
	}
	return string(pw), nil
}

func platformRun(ctx context.Context, out io.Writer, o options) (err error) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return errors.New("run it as an administrator")
	}
	stage := realaccount.WindowsStage
	if _, err := os.Stat(stage); err == nil {
		return fmt.Errorf("%s exists: this machine already ran the tests, or it is not disposable", stage)
	}
	b, err := build(ctx, out)
	defer os.RemoveAll(b.dir)
	if err != nil {
		return err
	}
	if !o.keep {
		defer cleanup(out, stage)
	}
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("service manager: %w", err)
	}
	defer func() { _ = m.Disconnect() }()

	// Folders and what the tests run, before anything can start.
	engineDir := filepath.Join(stage, "ci")
	launcherDir := filepath.Join(stage, "launcher")
	for _, d := range []string{engineDir, launcherDir, filepath.Join(launcherDir, "state"), filepath.Join(launcherDir, "logs"),
		filepath.Join(stage, "work"), filepath.Join(stage, "bin", realaccount.PTYVersion)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	testExe := filepath.Join(stage, "realaccount.test.exe")

	// The services first: the virtual account of the engine exists once its
	// service does, and the ACLs below name it.
	if err := createService(m, engineService, `NT SERVICE\`+engineService, testExe, "service-engine", stage,
		"-test.v", "-test.count=1", "-test.timeout="+o.timeout.String()); err != nil {
		return err
	}
	cfgPath := filepath.Join(stage, "launcher.json")
	if err := createService(m, launcherService, "LocalSystem", testExe, "service-launcher", cfgPath); err != nil {
		return err
	}
	// Like the installation: the engine keeps only the privilege it uses.
	if err := runCmd(ctx, out, nil, "sc.exe", "privs", engineService, "SeChangeNotifyPrivilege"); err != nil {
		return err
	}

	engine := `NT SERVICE\` + engineService
	for _, step := range []struct {
		path string
		args []string
	}{
		{stage, []string{"/inheritance:r", "/grant:r", sidSystem + ":(OI)(CI)F", "/grant:r", sidAdmins + ":(OI)(CI)F", "/grant:r", sidUsers + ":(OI)(CI)RX", "/grant:r", engine + ":(OI)(CI)RX"}},
		{engineDir, []string{"/inheritance:r", "/grant:r", sidSystem + ":(OI)(CI)F", "/grant:r", sidAdmins + ":(OI)(CI)F", "/grant:r", engine + ":(OI)(CI)F"}},
		{launcherDir, []string{"/inheritance:r", "/grant:r", sidSystem + ":(OI)(CI)F", "/grant:r", sidAdmins + ":(OI)(CI)F"}},
		{filepath.Join(stage, "work"), []string{"/grant:r", sidAll + ":(OI)(CI)M"}},
	} {
		if err := icacls(ctx, out, step.path, step.args...); err != nil {
			return err
		}
	}
	for src, dst := range map[string]string{b.test: testExe, b.pty: filepath.Join(stage, "bin", realaccount.PTYVersion, "mestre-pty.exe")} {
		if err := copyFile(src, dst); err != nil {
			return err
		}
	}

	pw, err := createAgentAccount(ctx, out)
	if err != nil {
		return err
	}
	// The password file of the installation: machine-scope DPAPI, owned by and
	// open to SYSTEM alone.
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	blob, err := dpapi.Protect([]byte(pw), realaccount.PasswordLabel)
	if err != nil {
		return err
	}
	if err := enableRestore(); err != nil {
		return err
	}
	passwordFile := filepath.Join(launcherDir, "mestre-agente.senha")
	if err := dpapi.WriteFile(passwordFile, blob, system); err != nil {
		return fmt.Errorf("password file: %w", err)
	}
	cfg, err := json.Marshal(realaccount.WindowsLauncherConfig{
		Instance: realaccount.Instance, AgentUser: realaccount.WindowsAgentUser, PasswordFile: passwordFile,
		BinDir: filepath.Join(stage, "bin"), SocketDir: filepath.Join(engineDir, "pty"),
		StateDir: filepath.Join(launcherDir, "state"), LogDir: filepath.Join(launcherDir, "logs"),
	})
	if err != nil {
		return err
	}
	if err := os.WriteFile(cfgPath, cfg, 0o644); err != nil {
		return err
	}

	// The launcher, then the tests.
	ls, err := m.OpenService(launcherService)
	if err != nil {
		return err
	}
	defer ls.Close()
	if err := ls.Start(); err != nil {
		return fmt.Errorf("start the launcher service: %w", err)
	}
	if err := waitState(ctx, ls, svc.Running); err != nil {
		dumpLogs(out, stage)
		return fmt.Errorf("the launcher service: %w", err)
	}
	es, err := m.OpenService(engineService)
	if err != nil {
		return err
	}
	defer es.Close()
	if err := es.Start(); err != nil {
		return fmt.Errorf("start the engine service: %w", err)
	}
	err = followUntilStopped(ctx, out, es, filepath.Join(engineDir, "engine.log"))
	if err == nil {
		code, rerr := os.ReadFile(filepath.Join(engineDir, "engine.exit"))
		if rerr != nil || strings.TrimSpace(string(code)) != "0" {
			err = fmt.Errorf("the tests failed (exit %q, %v)", strings.TrimSpace(string(code)), rerr)
		}
	}
	if err != nil {
		dumpLogs(out, stage)
	}
	return err
}

func createService(m *mgr.Mgr, name, account, exe string, args ...string) error {
	s, err := m.CreateService(name, exe, mgr.Config{
		ServiceType: windows.SERVICE_WIN32_OWN_PROCESS, StartType: mgr.StartManual, ErrorControl: mgr.ErrorNormal,
		DisplayName: "Mestre real-account tests (" + name + ")", ServiceStartName: account,
	}, args...)
	if err != nil {
		return fmt.Errorf("create service %s: %w", name, err)
	}
	return s.Close()
}

// createAgentAccount makes the local account of the agents like the
// installation: a common user with a random password that does not expire and
// that it cannot change, the right to log on as a batch job and the rights
// that deny the interactive and remote logons, hidden from the login screen.
// It checks the result with the logons themselves.
func createAgentAccount(ctx context.Context, out io.Writer) (string, error) {
	pw, err := password()
	if err != nil {
		return "", err
	}
	name := realaccount.WindowsAgentUser
	script := `$pw = ConvertTo-SecureString $env:RA_PW -AsPlainText -Force
New-LocalUser -Name '` + name + `' -Password $pw -PasswordNeverExpires -UserMayNotChangePassword -AccountNeverExpires -Description 'Mestre real-account tests' | Out-Null
Add-LocalGroupMember -Group 'Users' -Member '` + name + `'`
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), "RA_PW="+pw)
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("create the account %s: %w\n%s", name, err, b)
	}
	sid, _, _, err := windows.LookupSID("", name)
	if err != nil {
		return "", err
	}
	if err := grantRights(sid, "SeBatchLogonRight", "SeDenyInteractiveLogonRight", "SeDenyRemoteInteractiveLogonRight"); err != nil {
		return "", err
	}
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon\SpecialAccounts\UserList`, registry.SET_VALUE)
	if err != nil {
		return "", err
	}
	err = k.SetDWordValue(name, 0)
	_ = k.Close()
	if err != nil {
		return "", err
	}
	if err := logon(name, pw, logonBatch); err != nil {
		return "", fmt.Errorf("batch logon of %s: %w", name, err)
	}
	if err := logon(name, pw, logonInteractive); !errors.Is(err, windows.Errno(1385)) { // ERROR_LOGON_TYPE_NOT_GRANTED
		return "", fmt.Errorf("interactive logon of %s = %v, want it denied (ERROR_LOGON_TYPE_NOT_GRANTED)", name, err)
	}
	fmt.Fprintf(out, "account %s: batch logon allowed, interactive logon denied\n", name)
	return pw, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, in); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// waitState waits for the service to reach the state, or fails when it
// stopped before.
func waitState(ctx context.Context, s *mgr.Service, want svc.State) error {
	for {
		st, err := s.Query()
		if err != nil {
			return err
		}
		switch st.State {
		case want:
			return nil
		case svc.Stopped:
			return fmt.Errorf("stopped before it was %v (exit code %d, service exit code %d)", want, st.Win32ExitCode, st.ServiceSpecificExitCode)
		}
		if err := clock.Sleep(ctx, clock.Real{}, 250*time.Millisecond); err != nil {
			return err
		}
	}
}

// followUntilStopped copies the log of the tests to out as it grows, until the
// service stops.
func followUntilStopped(ctx context.Context, out io.Writer, s *mgr.Service, log string) error {
	var off int64
	flush := func() {
		f, err := os.Open(log)
		if err != nil {
			return
		}
		defer f.Close()
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return
		}
		n, _ := io.Copy(out, f)
		off += n
	}
	for {
		st, err := s.Query()
		if err != nil {
			return err
		}
		flush()
		if st.State == svc.Stopped {
			return nil
		}
		if err := clock.Sleep(ctx, clock.Real{}, time.Second); err != nil {
			_, _ = s.Control(svc.Stop)
			flush()
			return err
		}
	}
}

func dumpLogs(out io.Writer, stage string) {
	logs, _ := filepath.Glob(filepath.Join(stage, "launcher", "logs", "*.log"))
	for _, l := range append(logs, filepath.Join(stage, "launcher.json.err")) {
		if b, err := os.ReadFile(l); err == nil {
			fmt.Fprintf(out, "--- %s\n%s\n", l, b)
		}
	}
}

// cleanup removes what the run made, best effort: the machine is disposable.
func cleanup(out io.Writer, stage string) {
	ctx := context.Background()
	var errs []error
	if m, err := mgr.Connect(); err == nil {
		for _, name := range []string{engineService, launcherService} {
			s, err := m.OpenService(name)
			if err != nil {
				continue
			}
			if st, err := s.Control(svc.Stop); err == nil {
				for i := 0; i < 40 && st.State != svc.Stopped; i++ {
					_ = clock.Sleep(ctx, clock.Real{}, 250*time.Millisecond)
					if st, err = s.Query(); err != nil {
						break
					}
				}
			}
			if err := s.Delete(); err != nil {
				errs = append(errs, err)
			}
			s.Close()
		}
		_ = m.Disconnect()
	}
	name := realaccount.WindowsAgentUser
	if err := runCmd(ctx, io.Discard, nil, "net", "user", name, "/delete"); err != nil {
		errs = append(errs, err)
	}
	script := `Get-CimInstance Win32_UserProfile | Where-Object { $_.LocalPath -like '*\` + name + `' } | Remove-CimInstance`
	_ = runCmd(ctx, io.Discard, nil, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon\SpecialAccounts\UserList`, registry.SET_VALUE); err == nil {
		_ = k.DeleteValue(name)
		_ = k.Close()
	}
	// The folders have protected ACLs without the administrators: take them back.
	_ = runCmd(ctx, io.Discard, nil, "takeown", "/f", stage, "/r", "/d", "y")
	_ = runCmd(ctx, io.Discard, nil, "icacls", stage, "/grant", "*S-1-5-32-544:(OI)(CI)F", "/t", "/c", "/q")
	if err := os.RemoveAll(stage); err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		fmt.Fprintf(out, "realaccount: cleanup left something behind (ignored, the machine is disposable): %v\n", err)
	}
}
