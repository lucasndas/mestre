//go:build windows

package main

import (
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"
)

// start-pty: the launcher side of spike 5 (tools/winsandbox/spikes/03-mestre-pty,
// docs/arquitetura/windows.md#iniciador-mestre-launcher). Only the MestreSpike
// service may ask. Per terminal: LogonUser (batch, one logon session each),
// LoadUserProfile, CreateEnvironmentBlock, an ACE of NT SERVICE\MestreSpike in
// the token's default DACL, the logon SID on the service window station and
// desktop, and bin\ptyhost.exe created with CreateProcessAsUser outside any
// job, with a restricted process descriptor and the secret on an inherited
// pipe (its stdin). The profile stays loaded until that ptyhost exits.

var ptyIDPattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

const (
	// NT SERVICE\MestreSpike: terminate and query; OWNER RIGHTS: query only
	// (removes the owner's implicit READ_CONTROL and WRITE_DAC).
	ptyProcessSDDL = "D:P(A;;GA;;;SY)(A;;0x101001;;;%s)(A;;0x101000;;;OW)"
	ptyThreadSDDL  = "D:P(A;;GA;;;SY)(A;;0x100800;;;%s)(A;;0x100800;;;OW)"
	engineDACLMask = windows.PROCESS_QUERY_LIMITED_INFORMATION | windows.SYNCHRONIZE
)

func startPTY(pipe windows.Handle, req request) response {
	if err := checkEngineClient(pipe); err != nil {
		log.Printf("start-pty refused: %v", err)
		return response{Error: "refused: " + err.Error()}
	}
	if !ptyIDPattern.MatchString(req.ID) || len(req.Secret) != 32 {
		return response{Error: "start-pty: bad id or secret size"}
	}
	resp, err := createPTYHost(req)
	if err != nil {
		log.Printf("start-pty %s: %v", req.ID, err)
		return response{Error: err.Error()}
	}
	return resp
}

// checkEngineClient accepts only the process of the MestreSpike service, with
// the token of its virtual account.
func checkEngineClient(pipe windows.Handle) error {
	var pid uint32
	if err := windows.GetNamedPipeClientProcessId(pipe, &pid); err != nil {
		return fmt.Errorf("GetNamedPipeClientProcessId: %w", err)
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(mainSvc)
	if err != nil {
		return err
	}
	defer s.Close()
	st, err := s.Query()
	if err != nil {
		return err
	}
	if st.ProcessId != pid {
		return fmt.Errorf("client pid %d is not the %s service (pid %d)", pid, mainSvc, st.ProcessId)
	}
	want, _, _, err := windows.LookupSID("", `NT SERVICE\`+mainSvc)
	if err != nil {
		return err
	}
	p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(p)
	var t windows.Token
	if err := windows.OpenProcessToken(p, windows.TOKEN_QUERY, &t); err != nil {
		return err
	}
	defer t.Close()
	u, err := t.GetTokenUser()
	if err != nil {
		return err
	}
	if !u.User.Sid.Equals(want) {
		return fmt.Errorf("client token is %s, not %s", accountName(u.User.Sid), accountName(want))
	}
	return nil
}

func createPTYHost(req request) (resp response, err error) {
	engine, _, _, err := windows.LookupSID("", `NT SERVICE\`+mainSvc)
	if err != nil {
		return resp, err
	}
	pw, err := readAgentPassword()
	if err != nil {
		return resp, err
	}
	t, err := logonUser(agentUser, ".", pw, logon32LogonBatch)
	if err != nil {
		return resp, err
	}
	var prof windows.Handle
	var logon *windows.SID
	winsta, desktop := processWindowStation(), threadDesktop()
	cleanup := func() {
		if logon != nil {
			_ = setObjectACE(winsta, logon, winstaAllAccess, false)
			_ = setObjectACE(desktop, logon, desktopAllAccess, false)
		}
		if prof != 0 {
			if err := unloadUserProfile(t, prof); err != nil {
				log.Printf("start-pty %s: %v", req.ID, err)
			}
		}
		t.Close()
	}
	defer func() {
		if err != nil {
			cleanup()
		}
	}()
	if prof, err = loadUserProfile(t, agentUser); err != nil {
		return resp, err
	}
	env, err := tokenEnvironment(t)
	if err != nil {
		return resp, fmt.Errorf("CreateEnvironmentBlock: %w", err)
	}
	if err = addToDefaultDACL(t, engine, engineDACLMask); err != nil {
		return resp, err
	}
	if logon, err = logonSID(t); err != nil {
		return resp, err
	}
	if err = setObjectACE(winsta, logon, winstaAllAccess, true); err != nil {
		return resp, err
	}
	if err = setObjectACE(desktop, logon, desktopAllAccess, true); err != nil {
		return resp, err
	}

	// The secret goes through an anonymous pipe: only the read end is
	// inherited (handle list), as the child's stdin.
	var rd, wr windows.Handle
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	if err = windows.CreatePipe(&rd, &wr, &sa, 0); err != nil {
		return resp, fmt.Errorf("CreatePipe: %w", err)
	}
	defer windows.CloseHandle(rd)
	if err = windows.SetHandleInformation(wr, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		windows.CloseHandle(wr)
		return resp, err
	}
	var n uint32
	err = windows.WriteFile(wr, req.Secret, &n, nil)
	windows.CloseHandle(wr)
	if err != nil {
		return resp, fmt.Errorf("write secret: %w", err)
	}

	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return resp, err
	}
	defer attrs.Delete()
	inherit := []windows.Handle{rd}
	if err = attrs.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&inherit[0]), uintptr(len(inherit))*unsafe.Sizeof(inherit[0])); err != nil {
		return resp, fmt.Errorf("UpdateProcThreadAttribute(HANDLE_LIST): %w", err)
	}
	si := windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = windows.STARTF_USESTDHANDLES
	si.StdInput = rd

	psa, err := securityAttributes(fmt.Sprintf(ptyProcessSDDL, engine))
	if err != nil {
		return resp, err
	}
	tsa, err := securityAttributes(fmt.Sprintf(ptyThreadSDDL, engine))
	if err != nil {
		return resp, err
	}
	exe := filepath.Join(binDir, "ptyhost.exe")
	argv := []string{exe, "ptyhost", "-id", req.ID}
	if req.Harden {
		argv = append(argv, "-harden")
	}
	flags := uint32(windows.CREATE_BREAKAWAY_FROM_JOB | windows.CREATE_UNICODE_ENVIRONMENT |
		windows.EXTENDED_STARTUPINFO_PRESENT | windows.DETACHED_PROCESS)
	var pi windows.ProcessInformation
	if err = windows.CreateProcessAsUser(t, wstr(exe), wstr(windows.ComposeCommandLine(argv)), psa, tsa, true,
		flags, envBlock(env), wstr(workDir), &si.StartupInfo, &pi); err != nil {
		return resp, fmt.Errorf("CreateProcessAsUser %s: %w", exe, err)
	}
	windows.CloseHandle(pi.Thread)
	created, err := processCreated(pi.Process)
	if err != nil {
		windows.TerminateProcess(pi.Process, 1)
		windows.CloseHandle(pi.Process)
		return resp, err
	}
	ls := logonSessionID(t)
	log.Printf("start-pty %s: ptyhost pid %d created %d, logon session %s, harden %v", req.ID, pi.ProcessId, created, ls, req.Harden)
	go func() {
		_, _ = windows.WaitForSingleObject(pi.Process, windows.INFINITE)
		var code uint32
		_ = windows.GetExitCodeProcess(pi.Process, &code)
		windows.CloseHandle(pi.Process)
		cleanup()
		log.Printf("start-pty %s: ptyhost pid %d exited with %d; profile released, logon session %s closed", req.ID, pi.ProcessId, code, ls)
	}()
	return response{PID: pi.ProcessId, Created: created, LogonSession: ls}, nil
}

func securityAttributes(sddl string) (*windows.SecurityAttributes, error) {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, fmt.Errorf("SDDL %s: %w", sddl, err)
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}, nil
}

// processCreated is the creation time of an open process (FILETIME, 100 ns).
func processCreated(h windows.Handle) (int64, error) {
	var c, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(h, &c, &e, &k, &u); err != nil {
		return 0, err
	}
	return int64(c.HighDateTime)<<32 | int64(c.LowDateTime), nil
}

// tokenDefaultDACL is TOKEN_DEFAULT_DACL.
type tokenDefaultDACL struct{ DefaultDacl *windows.ACL }

// addToDefaultDACL grants mask to sid in the token's default DACL, the DACL
// that processes created with the token (and their children) get.
func addToDefaultDACL(t windows.Token, sid *windows.SID, mask uint32) error {
	var n uint32
	_ = windows.GetTokenInformation(t, windows.TokenDefaultDacl, nil, 0, &n)
	if n == 0 {
		return errors.New("GetTokenInformation(TokenDefaultDacl): empty size")
	}
	buf := make([]byte, n)
	if err := windows.GetTokenInformation(t, windows.TokenDefaultDacl, &buf[0], n, &n); err != nil {
		return fmt.Errorf("GetTokenInformation(TokenDefaultDacl): %w", err)
	}
	old := (*tokenDefaultDACL)(unsafe.Pointer(&buf[0])).DefaultDacl
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.ACCESS_MASK(mask),
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}, old)
	if err != nil {
		return fmt.Errorf("ACLFromEntries: %w", err)
	}
	info := tokenDefaultDACL{acl}
	if err := windows.SetTokenInformation(t, windows.TokenDefaultDacl, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return fmt.Errorf("SetTokenInformation(TokenDefaultDacl): %w", err)
	}
	return nil
}
