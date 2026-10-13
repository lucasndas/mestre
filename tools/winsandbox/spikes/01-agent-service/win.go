//go:build windows

package main

import (
	"errors"
	"fmt"
	"strings"
	u16 "unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Win32 calls that golang.org/x/sys/windows v0.47.0 does not wrap.
var (
	advapi32 = windows.NewLazySystemDLL("advapi32.dll")
	userenv  = windows.NewLazySystemDLL("userenv.dll")
	user32   = windows.NewLazySystemDLL("user32.dll")
	netapi32 = windows.NewLazySystemDLL("netapi32.dll")

	procLogonUserW                = advapi32.NewProc("LogonUserW")
	procLookupPrivilegeNameW      = advapi32.NewProc("LookupPrivilegeNameW")
	procLsaOpenPolicy             = advapi32.NewProc("LsaOpenPolicy")
	procLsaAddAccountRights       = advapi32.NewProc("LsaAddAccountRights")
	procLsaRemoveAccountRights    = advapi32.NewProc("LsaRemoveAccountRights")
	procLsaEnumerateAccountRights = advapi32.NewProc("LsaEnumerateAccountRights")
	procLsaFreeMemory             = advapi32.NewProc("LsaFreeMemory")
	procLsaClose                  = advapi32.NewProc("LsaClose")
	procLsaNtStatusToWinError     = advapi32.NewProc("LsaNtStatusToWinError")
	procLoadUserProfileW          = userenv.NewProc("LoadUserProfileW")
	procUnloadUserProfile         = userenv.NewProc("UnloadUserProfile")
	procGetProcessWindowStation   = user32.NewProc("GetProcessWindowStation")
	procGetThreadDesktop          = user32.NewProc("GetThreadDesktop")
	procGetUserObjectInformationW = user32.NewProc("GetUserObjectInformationW")
	procNetUserAdd                = netapi32.NewProc("NetUserAdd")
	procNetUserDel                = netapi32.NewProc("NetUserDel")
	procNetUserSetInfo            = netapi32.NewProc("NetUserSetInfo")
)

const (
	logon32LogonBatch      = 4
	logon32ProviderDefault = 0
	piNoUI                 = 1
	tokenStatisticsClass   = 10
	seGroupLogonID         = 0xC0000000
	uoiName                = 2
	winstaAllAccess        = 0x37F | 0xF0000
	desktopAllAccess       = 0x1FF | 0xF0000
	nerrUserExists         = 2224
	statusObjectNotFound   = 0xC0000034
)

func wstr(s string) *uint16 {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		panic(err)
	}
	return p
}

func logonUser(user, domain, password string, logonType uint32) (windows.Token, error) {
	var t windows.Token
	r, _, err := procLogonUserW.Call(uintptr(unsafe.Pointer(wstr(user))), uintptr(unsafe.Pointer(wstr(domain))),
		uintptr(unsafe.Pointer(wstr(password))), uintptr(logonType), logon32ProviderDefault, uintptr(unsafe.Pointer(&t)))
	if r == 0 {
		return 0, fmt.Errorf("LogonUser: %w", err)
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

func privilegeName(luid windows.LUID) string {
	buf := make([]uint16, 128)
	n := uint32(len(buf))
	r, _, _ := procLookupPrivilegeNameW.Call(0, uintptr(unsafe.Pointer(&luid)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		return fmt.Sprintf("luid(%d,%d)", luid.HighPart, luid.LowPart)
	}
	return windows.UTF16ToString(buf[:n])
}

// tokenPrivileges lists "Name (enabled|disabled)" for every privilege of t.
func tokenPrivileges(t windows.Token) ([]string, error) {
	var n uint32
	_ = windows.GetTokenInformation(t, windows.TokenPrivileges, nil, 0, &n)
	if n == 0 {
		return nil, errors.New("GetTokenInformation(TokenPrivileges): empty size")
	}
	buf := make([]byte, n)
	if err := windows.GetTokenInformation(t, windows.TokenPrivileges, &buf[0], n, &n); err != nil {
		return nil, fmt.Errorf("GetTokenInformation(TokenPrivileges): %w", err)
	}
	tp := (*windows.Tokenprivileges)(unsafe.Pointer(&buf[0]))
	var out []string
	for _, p := range tp.AllPrivileges() {
		state := "disabled"
		if p.Attributes&windows.SE_PRIVILEGE_ENABLED != 0 {
			state = "enabled"
		}
		out = append(out, fmt.Sprintf("%s (%s)", privilegeName(p.Luid), state))
	}
	return out, nil
}

// tokenStatistics is TOKEN_STATISTICS.
type tokenStatistics struct {
	TokenID            windows.LUID
	AuthenticationID   windows.LUID
	ExpirationTime     int64
	TokenType          uint32
	ImpersonationLevel uint32
	DynamicCharged     uint32
	DynamicAvailable   uint32
	GroupCount         uint32
	PrivilegeCount     uint32
	ModifiedID         windows.LUID
}

// logonSessionID returns the authentication LUID (the logon session) of t.
func logonSessionID(t windows.Token) string {
	var st tokenStatistics
	var n uint32
	if err := windows.GetTokenInformation(t, tokenStatisticsClass, (*byte)(unsafe.Pointer(&st)), uint32(unsafe.Sizeof(st)), &n); err != nil {
		return "?" + err.Error()
	}
	return fmt.Sprintf("%08x:%08x", st.AuthenticationID.HighPart, st.AuthenticationID.LowPart)
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

func accountName(sid *windows.SID) string {
	a, d, _, err := sid.LookupAccount("")
	if err != nil {
		return sid.String()
	}
	return d + `\` + a
}

func processWindowStation() windows.Handle {
	r, _, _ := procGetProcessWindowStation.Call()
	return windows.Handle(r)
}

func threadDesktop() windows.Handle {
	r, _, _ := procGetThreadDesktop.Call(uintptr(windows.GetCurrentThreadId()))
	return windows.Handle(r)
}

func userObjectName(h windows.Handle) string {
	buf := make([]uint16, 256)
	var n uint32
	r, _, err := procGetUserObjectInformationW.Call(uintptr(h), uoiName, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*2), uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		return "?" + err.Error()
	}
	return windows.UTF16ToString(buf)
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
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.ACCESS_MASK(mask),
		AccessMode:        mode,
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
	if err := windows.SetSecurityInfo(h, windows.SE_WINDOW_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		return fmt.Errorf("SetSecurityInfo: %w", err)
	}
	return nil
}

// LSA account rights.

type lsaUnicodeString struct {
	Length        uint16
	MaximumLength uint16
	Buffer        *uint16
}

type lsaObjectAttributes struct {
	Length                   uint32
	RootDirectory            windows.Handle
	ObjectName               *lsaUnicodeString
	Attributes               uint32
	SecurityDescriptor       uintptr
	SecurityQualityOfService uintptr
}

func lsaErr(call string, status uintptr) error {
	if status == 0 {
		return nil
	}
	r, _, _ := procLsaNtStatusToWinError.Call(status)
	return fmt.Errorf("%s: %w (NTSTATUS 0x%08x)", call, windows.Errno(r), status)
}

func lsaOpen() (uintptr, error) {
	var oa lsaObjectAttributes
	oa.Length = uint32(unsafe.Sizeof(oa))
	var h uintptr
	const policyAllAccess = 0x000F0FFF
	st, _, _ := procLsaOpenPolicy.Call(0, uintptr(unsafe.Pointer(&oa)), policyAllAccess, uintptr(unsafe.Pointer(&h)))
	return h, lsaErr("LsaOpenPolicy", st)
}

func lsaStrings(rights []string) []lsaUnicodeString {
	out := make([]lsaUnicodeString, len(rights))
	for i, r := range rights {
		out[i] = lsaUnicodeString{Length: uint16(len(r) * 2), MaximumLength: uint16(len(r)*2 + 2), Buffer: wstr(r)}
	}
	return out
}

func lsaAddRights(sid *windows.SID, rights ...string) error {
	h, err := lsaOpen()
	if err != nil {
		return err
	}
	defer procLsaClose.Call(h)
	s := lsaStrings(rights)
	st, _, _ := procLsaAddAccountRights.Call(h, uintptr(unsafe.Pointer(sid)), uintptr(unsafe.Pointer(&s[0])), uintptr(len(s)))
	return lsaErr("LsaAddAccountRights", st)
}

func lsaRemoveRights(sid *windows.SID, rights ...string) error {
	h, err := lsaOpen()
	if err != nil {
		return err
	}
	defer procLsaClose.Call(h)
	s := lsaStrings(rights)
	st, _, _ := procLsaRemoveAccountRights.Call(h, uintptr(unsafe.Pointer(sid)), 0, uintptr(unsafe.Pointer(&s[0])), uintptr(len(s)))
	if st == statusObjectNotFound {
		return nil
	}
	return lsaErr("LsaRemoveAccountRights", st)
}

func lsaEnumRights(sid *windows.SID) ([]string, error) {
	h, err := lsaOpen()
	if err != nil {
		return nil, err
	}
	defer procLsaClose.Call(h)
	var p *lsaUnicodeString
	var n uint32
	st, _, _ := procLsaEnumerateAccountRights.Call(h, uintptr(unsafe.Pointer(sid)), uintptr(unsafe.Pointer(&p)), uintptr(unsafe.Pointer(&n)))
	if st == statusObjectNotFound {
		return nil, nil
	}
	if err := lsaErr("LsaEnumerateAccountRights", st); err != nil {
		return nil, err
	}
	defer procLsaFreeMemory.Call(uintptr(unsafe.Pointer(p)))
	var out []string
	for _, u := range unsafe.Slice(p, n) {
		out = append(out, windows.UTF16ToString(unsafe.Slice(u.Buffer, u.Length/2)))
	}
	return out, nil
}

// Local accounts.

type userInfo1 struct {
	Name        *uint16
	Password    *uint16
	PasswordAge uint32
	Priv        uint32
	HomeDir     *uint16
	Comment     *uint16
	Flags       uint32
	ScriptPath  *uint16
}

// createOrResetUser creates a plain local user (Users group) with a
// password that never expires and that the user cannot change, or resets
// the password if the user exists. It reports whether it created it.
func createOrResetUser(name, password string) (bool, error) {
	const userPrivUser, ufScript, ufPasswdCantChange, ufDontExpirePasswd = 1, 0x1, 0x40, 0x10000
	ui := userInfo1{Name: wstr(name), Password: wstr(password), Priv: userPrivUser,
		Comment: wstr("Mestre agents (M1 spike)"), Flags: ufScript | ufPasswdCantChange | ufDontExpirePasswd}
	r, _, _ := procNetUserAdd.Call(0, 1, uintptr(unsafe.Pointer(&ui)), 0)
	if r == 0 {
		return true, nil
	}
	if r != nerrUserExists {
		return false, fmt.Errorf("NetUserAdd: status %d (%v)", r, windows.Errno(r))
	}
	pw := struct{ Password *uint16 }{wstr(password)}
	r, _, _ = procNetUserSetInfo.Call(0, uintptr(unsafe.Pointer(wstr(name))), 1003, uintptr(unsafe.Pointer(&pw)), 0)
	if r != 0 {
		return false, fmt.Errorf("NetUserSetInfo(1003): status %d (%v)", r, windows.Errno(r))
	}
	return false, nil
}

func deleteUser(name string) error {
	r, _, _ := procNetUserDel.Call(0, uintptr(unsafe.Pointer(wstr(name))))
	if r != 0 && r != 2221 { // NERR_UserNotFound
		return fmt.Errorf("NetUserDel: status %d", r)
	}
	return nil
}

// multiSZ encodes strings as a REG_MULTI_SZ-style UTF-16 block.
func multiSZ(ss []string) *uint16 {
	b := u16.Encode([]rune(strings.Join(ss, "\x00") + "\x00\x00"))
	return &b[0]
}
