//go:build windows

package main

import (
	"errors"
	"fmt"
	"regexp"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Win32 calls that golang.org/x/sys/windows v0.47.0 does not wrap.
var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	user32   = windows.NewLazySystemDLL("user32.dll")

	procIsProcessInJob          = kernel32.NewProc("IsProcessInJob")
	procWaitNamedPipeW          = kernel32.NewProc("WaitNamedPipeW")
	procGetProcessWindowStation = user32.NewProc("GetProcessWindowStation")
	procGetThreadDesktop        = user32.NewProc("GetThreadDesktop")
)

const (
	seGroupLogonID   = 0xC0000000
	winstaAllAccess  = 0x37F | 0xF0000
	desktopAllAccess = 0x1FF | 0xF0000
)

func wstr(s string) *uint16 {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		panic(err)
	}
	return p
}

func isProcessInJob(proc, job windows.Handle) (bool, error) {
	var in int32
	r, _, err := procIsProcessInJob.Call(uintptr(proc), uintptr(job), uintptr(unsafe.Pointer(&in)))
	if r == 0 {
		return false, fmt.Errorf("IsProcessInJob: %w", err)
	}
	return in != 0, nil
}

func waitNamedPipe(name string, d time.Duration) {
	_, _, _ = procWaitNamedPipeW.Call(uintptr(unsafe.Pointer(wstr(name))), uintptr(d.Milliseconds()))
}

// processCreated is the creation time of an open process (FILETIME, 100 ns).
func processCreated(h windows.Handle) (int64, error) {
	var c, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(h, &c, &e, &k, &u); err != nil {
		return 0, err
	}
	return int64(c.HighDateTime)<<32 | int64(c.LowDateTime), nil
}

func processImage(h windows.Handle) string {
	buf := make([]uint16, 1024)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return "?"
	}
	return windows.UTF16ToString(buf[:n])
}

// parentPID of the current process.
func parentPID() uint32 {
	var pbi windows.PROCESS_BASIC_INFORMATION
	if err := windows.NtQueryInformationProcess(windows.CurrentProcess(), windows.ProcessBasicInformation,
		unsafe.Pointer(&pbi), uint32(unsafe.Sizeof(pbi)), nil); err != nil {
		return 0
	}
	return uint32(pbi.InheritedFromUniqueProcessId)
}

func logonSID(t windows.Token) (*windows.SID, error) {
	g, err := t.GetTokenGroups()
	if err != nil {
		return nil, err
	}
	for _, s := range g.AllGroups() {
		if s.Attributes&seGroupLogonID == seGroupLogonID {
			return s.Sid.Copy()
		}
	}
	return nil, errors.New("token has no logon SID")
}

func processWindowStation() windows.Handle {
	r, _, _ := procGetProcessWindowStation.Call()
	return windows.Handle(r)
}

func threadDesktop() windows.Handle {
	r, _, _ := procGetThreadDesktop.Call(uintptr(windows.GetCurrentThreadId()))
	return windows.Handle(r)
}

func explicitAccess(sid *windows.SID, mask uint32, mode windows.ACCESS_MODE) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.ACCESS_MASK(mask),
		AccessMode:        mode,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}

// setObjectACE grants (or revokes) mask for sid on a window station or
// desktop handle.
func setObjectACE(h windows.Handle, sid *windows.SID, mask uint32, grant bool) error {
	sd, err := windows.GetSecurityInfo(h, windows.SE_WINDOW_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("GetSecurityInfo: %w", err)
	}
	old, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("DACL: %w", err)
	}
	mode := windows.ACCESS_MODE(windows.GRANT_ACCESS)
	if !grant {
		mode = windows.REVOKE_ACCESS
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{explicitAccess(sid, mask, mode)}, old)
	if err != nil {
		return fmt.Errorf("ACLFromEntries: %w", err)
	}
	return windows.SetSecurityInfo(h, windows.SE_WINDOW_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

// tokenDefaultDACL is TOKEN_DEFAULT_DACL.
type tokenDefaultDACL struct{ DefaultDacl *windows.ACL }

func defaultDACL(t windows.Token) (*windows.ACL, error) {
	var n uint32
	_ = windows.GetTokenInformation(t, windows.TokenDefaultDacl, nil, 0, &n)
	if n == 0 {
		return nil, errors.New("GetTokenInformation(TokenDefaultDacl): empty size")
	}
	buf := make([]byte, n)
	if err := windows.GetTokenInformation(t, windows.TokenDefaultDacl, &buf[0], n, &n); err != nil {
		return nil, fmt.Errorf("GetTokenInformation(TokenDefaultDacl): %w", err)
	}
	return (*tokenDefaultDACL)(unsafe.Pointer(&buf[0])).DefaultDacl, nil
}

func aclSDDL(acl *windows.ACL) string {
	sd, err := windows.NewSecurityDescriptor()
	if err != nil {
		return "?" + err.Error()
	}
	if err := sd.SetDACL(acl, true, false); err != nil {
		return "?" + err.Error()
	}
	return sd.String()
}

// grantInDefaultDACL adds an ACE for sid to the token's default DACL, the
// DACL that processes created with the token (and their children) get.
func grantInDefaultDACL(t windows.Token, sid *windows.SID, mask uint32) error {
	old, err := defaultDACL(t)
	if err != nil {
		return err
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{explicitAccess(sid, mask, windows.GRANT_ACCESS)}, old)
	if err != nil {
		return fmt.Errorf("ACLFromEntries: %w", err)
	}
	info := tokenDefaultDACL{acl}
	if err := windows.SetTokenInformation(t, windows.TokenDefaultDacl, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return fmt.Errorf("SetTokenInformation(TokenDefaultDacl): %w", err)
	}
	return nil
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
	return nil
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

var ansi = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[@-Z\\-_]|\x1b[()][0-9A-Za-z]`)

func strip(b []byte) string { return ansi.ReplaceAllString(string(b), "") }
