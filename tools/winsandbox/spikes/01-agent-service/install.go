//go:build windows

package main

import (
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	agentUser  = "mestre-agente"
	baseDir    = `C:\ProgramData\MestreSpike`
	mainSvc    = "MestreSpike"         // virtual account NT SERVICE\MestreSpike
	launchSvc  = "MestreSpikeLauncher" // LocalSystem (plan B)
	enginePriv = "SeAssignPrimaryTokenPrivilege,SeIncreaseQuotaPrivilege"
)

var (
	binDir     = filepath.Join(baseDir, "bin")
	clisDir    = filepath.Join(baseDir, "clis")
	secretDir  = filepath.Join(baseDir, "no")
	secretFile = filepath.Join(secretDir, "mestre-agente.senha")
	logDir     = filepath.Join(baseDir, "logs")
	workDir    = filepath.Join(baseDir, "work")
)

func serviceName(short string) string {
	if short == "launcher" {
		return launchSvc
	}
	return mainSvc
}

func cmdInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	which := fs.String("service", "mestre", "mestre (virtual account) or launcher (LocalSystem)")
	grant := fs.Bool("grant", true, "mestre: grant "+enginePriv+" to the virtual account through the LSA (false removes them)")
	declare := fs.Bool("declare", true, "mestre: declare the privileges as the service's required privileges")
	extra := fs.String("extra", "", "mestre: extra privileges to grant and declare, comma separated (experiments)")
	_ = fs.Parse(args)
	name := serviceName(*which)

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	for _, d := range []string{clisDir, secretDir, logDir, workDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	exe, err := installSelf()
	if err != nil {
		return err
	}

	// Service first: the virtual account SID only resolves once it exists.
	acct := `NT SERVICE\` + name
	if name == launchSvc {
		acct = "LocalSystem"
	}
	if err := upsertService(name, exe, acct); err != nil {
		return err
	}
	fmt.Printf("service %s: binary %s, account %s\n", name, exe, acct)

	if err := setupAgent(); err != nil {
		return err
	}
	mainSID, _, _, err := windows.LookupSID("", `NT SERVICE\`+mainSvc)
	if err != nil {
		fmt.Printf("note: %s not installed yet, ACLs give it nothing (%v)\n", mainSvc, err)
	}
	if err := applyACLs(mainSID); err != nil {
		return err
	}

	if name == mainSvc {
		privs := strings.Split(enginePriv, ",")
		if *extra != "" {
			privs = append(privs, strings.Split(*extra, ",")...)
		}
		if *grant {
			err = lsaAddRights(mainSID, privs...)
		} else {
			err = lsaRemoveRights(mainSID, append(strings.Split(enginePriv, ","), "SeBackupPrivilege", "SeRestorePrivilege")...)
		}
		if err != nil {
			return err
		}
		required := []string{}
		if *declare {
			required = append([]string{"SeChangeNotifyPrivilege"}, privs...)
		}
		if err := setRequiredPrivileges(name, required); err != nil {
			return err
		}
		rights, err := lsaEnumRights(mainSID)
		fmt.Printf("LSA rights of %s (%s): %v err=%v\n", acct, mainSID, rights, err)
		fmt.Printf("required privileges declared: %v\n", required)
	}
	return nil
}

// installSelf copies the running binary to bin\spikesvc.exe.
func installSelf() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	dst := filepath.Join(binDir, "spikesvc.exe")
	if strings.EqualFold(self, dst) {
		return dst, nil
	}
	for _, n := range []string{mainSvc, launchSvc} {
		_ = stopService(n, 15*time.Second)
	}
	in, err := os.Open(self)
	if err != nil {
		return "", err
	}
	defer in.Close()
	// A stopped service's process may still hold the file for a moment.
	var out *os.File
	for end := time.Now().Add(10 * time.Second); ; {
		if out, err = os.Create(dst); err == nil || time.Now().After(end) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return "", err
	}
	return dst, out.Close()
}

func upsertService(name, exe, acct string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	cfg := mgr.Config{
		ServiceType: windows.SERVICE_WIN32_OWN_PROCESS, StartType: mgr.StartManual,
		ErrorControl: mgr.ErrorNormal, DisplayName: "Mestre M1 spike (" + name + ")",
		ServiceStartName: acct,
	}
	s, err := m.OpenService(name)
	if err == nil {
		defer s.Close()
		cfg.BinaryPathName = fmt.Sprintf(`"%s" service %s`, exe, name)
		return s.UpdateConfig(cfg)
	}
	s, err = m.CreateService(name, exe, cfg, "service", name)
	if err != nil {
		return fmt.Errorf("CreateService: %w", err)
	}
	return s.Close()
}

func setRequiredPrivileges(name string, privs []string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return err
	}
	defer s.Close()
	info := struct{ Privileges *uint16 }{multiSZ(privs)}
	if err := windows.ChangeServiceConfig2(s.Handle, windows.SERVICE_CONFIG_REQUIRED_PRIVILEGES_INFO, (*byte)(unsafe.Pointer(&info))); err != nil {
		return fmt.Errorf("ChangeServiceConfig2(required privileges): %w", err)
	}
	return nil
}

func stopService(name string, deadline time.Duration) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return nil
	}
	defer s.Close()
	st, err := s.Control(svc.Stop)
	if err != nil {
		return nil // not running
	}
	end := time.Now().Add(deadline)
	for st.State != svc.Stopped && time.Now().Before(end) {
		// Polling the SCM is the documented way to wait for a stop.
		time.Sleep(200 * time.Millisecond)
		if st, err = s.Query(); err != nil {
			return err
		}
	}
	if st.State != svc.Stopped {
		return fmt.Errorf("%s did not stop in %v", name, deadline)
	}
	return nil
}

// setupAgent creates (or resets) mestre-agente with a fresh random password
// kept with machine-scope DPAPI, and gives it the logon rights of
// docs/arquitetura/windows.md.
func setupAgent() error {
	pw, err := randomPassword(32)
	if err != nil {
		return err
	}
	created, err := createOrResetUser(agentUser, pw)
	if err != nil {
		return err
	}
	if err := writeAgentPassword(pw); err != nil {
		return err
	}
	sid, _, _, err := windows.LookupSID("", agentUser)
	if err != nil {
		return err
	}
	if err := lsaAddRights(sid, "SeBatchLogonRight", "SeDenyInteractiveLogonRight", "SeDenyRemoteInteractiveLogonRight"); err != nil {
		return err
	}
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon\SpecialAccounts\UserList`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.SetDWordValue(agentUser, 0); err != nil {
		return err
	}
	rights, _ := lsaEnumRights(sid)
	fmt.Printf("account %s (%s): created=%v, password reset and stored in %s, rights %v\n", agentUser, sid, created, secretFile, rights)
	return nil
}

func randomPassword(n int) (string, error) {
	const lower, upper, digits, symbols = "abcdefghijkmnopqrstuvwxyz", "ABCDEFGHJKLMNPQRSTUVWXYZ", "23456789", "!#%+-=@^_~"
	all := lower + upper + digits + symbols
	for {
		b := make([]byte, n)
		for i := range b {
			j, err := rand.Int(rand.Reader, big.NewInt(int64(len(all))))
			if err != nil {
				return "", err
			}
			b[i] = all[j.Int64()]
		}
		s := string(b)
		if strings.ContainsAny(s, lower) && strings.ContainsAny(s, upper) && strings.ContainsAny(s, digits) && strings.ContainsAny(s, symbols) {
			return s, nil
		}
	}
}

func writeAgentPassword(pw string) error {
	in := windows.DataBlob{Size: uint32(len(pw)), Data: unsafe.StringData(pw)}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, wstr("mestre-agente"), nil, 0, nil, windows.CRYPTPROTECT_LOCAL_MACHINE|windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return fmt.Errorf("CryptProtectData: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	_ = os.Remove(secretFile)
	return os.WriteFile(secretFile, unsafe.Slice(out.Data, out.Size), 0o600)
}

func readAgentPassword() (string, error) {
	b, err := os.ReadFile(secretFile)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if len(b) == 0 {
		return "", errors.New("empty password file")
	}
	in := windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", fmt.Errorf("CryptUnprotectData: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return string(unsafe.Slice(out.Data, out.Size)), nil
}

// applyACLs sets protected DACLs on the spike folders, mirroring
// docs/arquitetura/persistencia.md: the password only for the mestre service account
// and SYSTEM, binaries read-only for Users, the work folder writable by
// mestre-agente.
func applyACLs(svcSID *windows.SID) error {
	agent, _, _, err := windows.LookupSID("", agentUser)
	if err != nil {
		return err
	}
	n := ""
	if svcSID != nil {
		n = svcSID.String()
	}
	a := agent.String()
	ace := func(mask, sid string) string {
		if sid == "" {
			return ""
		}
		return "(A;OICI;" + mask + ";;;" + sid + ")"
	}
	const full, rx, modify = "FA", "0x1200a9", "0x1301bf"
	admin := ace(full, "SY") + ace(full, "BA")
	sets := []struct{ path, sddl string }{
		{baseDir, "D:P" + admin + ace(rx, n)},
		{binDir, "D:P" + admin + ace(rx, n) + ace(rx, "BU")},
		{clisDir, "D:P" + admin + ace(rx, n) + ace(rx, "BU")},
		{logDir, "D:P" + admin + ace(modify, n)},
		{workDir, "D:P" + admin + ace(rx, n) + ace(modify, a)},
		{secretDir, "D:P" + admin + ace(full, n)},
		{secretFile, "D:P(A;;FA;;;SY)" + strings.ReplaceAll(ace(full, n), "OICI", "")},
	}
	for _, s := range sets {
		if err := setPathDACL(s.path, s.sddl); err != nil {
			return fmt.Errorf("ACL %s: %w", s.path, err)
		}
	}
	return nil
}

func setPathDACL(path, sddl string) error {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

func cmdUninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	if sid, _, _, err := windows.LookupSID("", `NT SERVICE\`+mainSvc); err == nil {
		_ = lsaRemoveRights(sid, append(strings.Split(enginePriv, ","), "SeBackupPrivilege", "SeRestorePrivilege")...)
	}
	for _, n := range []string{mainSvc, launchSvc} {
		_ = stopService(n, 15*time.Second)
		if s, err := m.OpenService(n); err == nil {
			_ = s.Delete()
			s.Close()
		}
	}
	return deleteUser(agentUser)
}

// cmdRights prints the LSA rights of an account (evidence for spike 2).
func cmdRights(args []string) error {
	for _, a := range args {
		sid, _, _, err := windows.LookupSID("", a)
		if err != nil {
			return err
		}
		r, err := lsaEnumRights(sid)
		fmt.Printf("%s (%s): %v err=%v\n", a, sid, r, err)
	}
	return nil
}
