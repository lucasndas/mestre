//go:build windows

package winsvc

import (
	"errors"
	"fmt"
	"strconv"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Win32 calls that golang.org/x/sys/windows does not wrap.
var (
	advapi32 = windows.NewLazySystemDLL("advapi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	userenv  = windows.NewLazySystemDLL("userenv.dll")
	user32   = windows.NewLazySystemDLL("user32.dll")

	procLogonUserW              = advapi32.NewProc("LogonUserW")
	procLoadUserProfileW        = userenv.NewProc("LoadUserProfileW")
	procUnloadUserProfile       = userenv.NewProc("UnloadUserProfile")
	procGetProcessWindowStation = user32.NewProc("GetProcessWindowStation")
	procGetThreadDesktop        = user32.NewProc("GetThreadDesktop")
	procIsProcessInJob          = kernel32.NewProc("IsProcessInJob")
	procWaitNamedPipeW          = kernel32.NewProc("WaitNamedPipeW")
)

const (
	logon32LogonBatch      = 4
	logon32ProviderDefault = 0
	piNoUI                 = 1
	seGroupLogonID         = 0xC0000000
	winstaAllAccess        = 0x37F | 0xF0000
	desktopAllAccess       = 0x1FF | 0xF0000
	stillActive            = 259
)

func wstr(s string) *uint16 {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return nil
	}
	return p
}

// IsProcessInJob reports whether process is in job; a zero job means "any
// job".
func IsProcessInJob(process, job windows.Handle) (bool, error) {
	var in int32
	r, _, err := procIsProcessInJob.Call(uintptr(process), uintptr(job), uintptr(unsafe.Pointer(&in)))
	if r == 0 {
		return false, fmt.Errorf("IsProcessInJob: %w", err)
	}
	return in != 0, nil
}

// creationTime returns the creation time of the process behind h as the
// decimal FILETIME terminal.System.Identify gives, and whether it still runs.
func creationTime(h windows.Handle) (start string, running bool, err error) {
	var c, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(h, &c, &e, &k, &u); err != nil {
		return "", false, err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return "", false, err
	}
	t := uint64(c.HighDateTime)<<32 | uint64(c.LowDateTime)
	return strconv.FormatUint(t, 10), code == stillActive, nil
}

// envBlock makes the UTF-16 environment block CreateProcess takes.
func envBlock(env []string) *uint16 {
	var b []uint16
	for _, e := range env {
		b = append(b, utf16.Encode([]rune(e))...)
		b = append(b, 0)
	}
	if len(b) == 0 {
		b = append(b, 0)
	}
	b = append(b, 0)
	return &b[0]
}

// securityAttributes makes the attributes of a security descriptor given as
// SDDL. The descriptor lives as long as the returned pointer is referenced.
func securityAttributes(sddl string) (*windows.SecurityAttributes, error) {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, fmt.Errorf("security descriptor %s: %w", sddl, err)
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}, nil
}

// tokenUser returns the SID of the user of the process pid.
func tokenUser(pid uint32) (*windows.SID, error) {
	p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return nil, fmt.Errorf("open process %d: %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(p) }()
	var t windows.Token
	if err := windows.OpenProcessToken(p, windows.TOKEN_QUERY, &t); err != nil {
		return nil, fmt.Errorf("token of process %d: %w", pid, err)
	}
	defer t.Close()
	u, err := t.GetTokenUser()
	if err != nil {
		return nil, err
	}
	return u.User.Sid.Copy()
}

func accountName(sid *windows.SID) string {
	a, d, _, err := sid.LookupAccount("")
	if err != nil {
		return sid.String()
	}
	return d + `\` + a
}

// currentUserSID is the SID of the user of this process.
func currentUserSID() (*windows.SID, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return u.User.Sid.Copy()
}

// CurrentUserSID is the SID of the user of this process, for tests and for
// the installer's wiring.
func CurrentUserSID() (*windows.SID, error) { return currentUserSID() }

// LogonUser logon types and the call itself, for the account of the agents.
func logonUser(user, domain, password string, logonType uint32) (windows.Token, error) {
	var t windows.Token
	r, _, err := procLogonUserW.Call(uintptr(unsafe.Pointer(wstr(user))), uintptr(unsafe.Pointer(wstr(domain))),
		uintptr(unsafe.Pointer(wstr(password))), uintptr(logonType), logon32ProviderDefault, uintptr(unsafe.Pointer(&t)))
	if r == 0 {
		return 0, fmt.Errorf("LogonUser %s: %w", user, err)
	}
	return t, nil
}

// profileInfo is PROFILEINFOW.
type profileInfo struct {
	Size        uint32
	Flags       uint32
	UserName    *uint16
	ProfilePath *uint16
	DefaultPath *uint16
	ServerName  *uint16
	PolicyPath  *uint16
	Profile     windows.Handle
}

func loadUserProfile(t windows.Token, user string) (windows.Handle, error) {
	pi := profileInfo{Flags: piNoUI, UserName: wstr(user)}
	pi.Size = uint32(unsafe.Sizeof(pi))
	r, _, err := procLoadUserProfileW.Call(uintptr(t), uintptr(unsafe.Pointer(&pi)))
	if r == 0 {
		return 0, fmt.Errorf("LoadUserProfile: %w", err)
	}
	return pi.Profile, nil
}

func unloadUserProfile(t windows.Token, h windows.Handle) error {
	r, _, err := procUnloadUserProfile.Call(uintptr(t), uintptr(h))
	if r == 0 {
		return fmt.Errorf("UnloadUserProfile: %w", err)
	}
	return nil
}

// tokenEnvironment is the environment of the profile of the user of t.
func tokenEnvironment(t windows.Token) ([]string, error) {
	var block *uint16
	if err := windows.CreateEnvironmentBlock(&block, t, false); err != nil {
		return nil, err
	}
	defer func() { _ = windows.DestroyEnvironmentBlock(block) }()
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

// logonSID returns the logon SID of the token: the SID that names its logon
// session on window stations and desktops.
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
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{aceFor(sid, mask, mode)}, old)
	if err != nil {
		return fmt.Errorf("ACLFromEntries: %w", err)
	}
	if err := windows.SetSecurityInfo(h, windows.SE_WINDOW_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		return fmt.Errorf("SetSecurityInfo: %w", err)
	}
	return nil
}

func aceFor(sid *windows.SID, mask uint32, mode windows.ACCESS_MODE) windows.EXPLICIT_ACCESS {
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

// tokenDefaultDACL is TOKEN_DEFAULT_DACL.
type tokenDefaultDACL struct{ DefaultDacl *windows.ACL }

// addToDefaultDACL grants mask to sid in the default DACL of the token, the
// DACL that processes created with the token (and their children) get.
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
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{aceFor(sid, mask, windows.GRANT_ACCESS)}, old)
	if err != nil {
		return fmt.Errorf("ACLFromEntries: %w", err)
	}
	info := tokenDefaultDACL{acl}
	if err := windows.SetTokenInformation(t, windows.TokenDefaultDacl, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return fmt.Errorf("SetTokenInformation(TokenDefaultDacl): %w", err)
	}
	return nil
}
