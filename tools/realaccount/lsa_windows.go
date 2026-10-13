//go:build windows

package main

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The user rights of an account (docs/arquitetura/windows.md#conta-dos-agentes-mestre-agente):
// the installation grants these through the LSA policy, which is what this
// file does for the account of the tests.

var (
	advapi32                = windows.NewLazySystemDLL("advapi32.dll")
	procLsaOpenPolicy       = advapi32.NewProc("LsaOpenPolicy")
	procLsaAddAccountRights = advapi32.NewProc("LsaAddAccountRights")
	procLsaClose            = advapi32.NewProc("LsaClose")
	procLsaNtStatusToWinErr = advapi32.NewProc("LsaNtStatusToWinError")
	procLogonUserW          = advapi32.NewProc("LogonUserW")
)

// policyCreateAccount | policyLookupNames
const policyAccess = 0x00000010 | 0x00000800

type lsaObjectAttributes struct {
	Length                   uint32
	RootDirectory            windows.Handle
	ObjectName               uintptr
	Attributes               uint32
	SecurityDescriptor       uintptr
	SecurityQualityOfService uintptr
}

type lsaUnicodeString struct {
	Length        uint16
	MaximumLength uint16
	Buffer        *uint16
}

func ntStatus(status uintptr) error {
	if status == 0 {
		return nil
	}
	code, _, _ := procLsaNtStatusToWinErr.Call(status)
	return fmt.Errorf("NTSTATUS %#x: %w", status, windows.Errno(code))
}

// grantRights gives the account with sid the user rights (SeBatchLogonRight,
// SeDenyInteractiveLogonRight...).
func grantRights(sid *windows.SID, rights ...string) error {
	var oa lsaObjectAttributes
	oa.Length = uint32(unsafe.Sizeof(oa))
	var policy windows.Handle
	if r, _, _ := procLsaOpenPolicy.Call(0, uintptr(unsafe.Pointer(&oa)), policyAccess, uintptr(unsafe.Pointer(&policy))); r != 0 {
		return fmt.Errorf("LsaOpenPolicy: %w", ntStatus(r))
	}
	defer func() { _, _, _ = procLsaClose.Call(uintptr(policy)) }()
	for _, right := range rights {
		u, err := windows.UTF16FromString(right)
		if err != nil {
			return err
		}
		n := uint16(len(u)-1) * 2
		s := lsaUnicodeString{Length: n, MaximumLength: n + 2, Buffer: &u[0]}
		if r, _, _ := procLsaAddAccountRights.Call(uintptr(policy), uintptr(unsafe.Pointer(sid)), uintptr(unsafe.Pointer(&s)), 1); r != 0 {
			return fmt.Errorf("LsaAddAccountRights(%s): %w", right, ntStatus(r))
		}
	}
	return nil
}

// Logon types of LogonUser.
const (
	logonInteractive = 2
	logonBatch       = 4
)

// logon makes a logon of the local account and closes it at once; the error
// is why Windows refused it.
func logon(user, password string, kind uint32) error {
	u, err := windows.UTF16PtrFromString(user)
	if err != nil {
		return err
	}
	d, _ := windows.UTF16PtrFromString(".")
	p, err := windows.UTF16PtrFromString(password)
	if err != nil {
		return err
	}
	var tok windows.Token
	r, _, e := procLogonUserW.Call(uintptr(unsafe.Pointer(u)), uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(p)), uintptr(kind), 0, uintptr(unsafe.Pointer(&tok)))
	if r == 0 {
		return e
	}
	return tok.Close()
}

// enableRestore turns SeRestorePrivilege on in the token of this process, which
// writing a file owned by SYSTEM (the password file) needs.
func enableRestore() error {
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &tok); err != nil {
		return err
	}
	defer tok.Close()
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr("SeRestorePrivilege"), &luid); err != nil {
		return err
	}
	tp := windows.Tokenprivileges{PrivilegeCount: 1}
	tp.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}
	return windows.AdjustTokenPrivileges(tok, false, &tp, 0, nil, nil)
}

var (
	secur32                    = windows.NewLazySystemDLL("secur32.dll")
	procLsaEnumerateSessions   = secur32.NewProc("LsaEnumerateLogonSessions")
	procLsaGetLogonSessionData = secur32.NewProc("LsaGetLogonSessionData")
	procLsaFreeReturnBuffer    = secur32.NewProc("LsaFreeReturnBuffer")
)

// logonSessionData is the head of SECURITY_LOGON_SESSION_DATA, up to the
// field that is read.
type logonSessionData struct {
	Size                  uint32
	LogonID               windows.LUID
	UserName              lsaUnicodeString
	LogonDomain           lsaUnicodeString
	AuthenticationPackage lsaUnicodeString
	LogonType             uint32
}

// sessionsOf lists the logon sessions of the local account user, as LSA
// describes them: only an administrator can read the sessions of others.
func sessionsOf(user string) ([]uint64, error) {
	var count uint32
	var luids *windows.LUID
	if r, _, _ := procLsaEnumerateSessions.Call(uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&luids))); r != 0 {
		return nil, fmt.Errorf("LsaEnumerateLogonSessions: %w", ntStatus(r))
	}
	defer func() { _, _, _ = procLsaFreeReturnBuffer.Call(uintptr(unsafe.Pointer(luids))) }()
	var found []uint64
	for _, l := range unsafe.Slice(luids, count) {
		var data *logonSessionData
		if r, _, _ := procLsaGetLogonSessionData.Call(uintptr(unsafe.Pointer(&l)), uintptr(unsafe.Pointer(&data))); r != 0 {
			continue // ended meanwhile
		}
		name := ""
		if n := int(data.UserName.Length) / 2; n > 0 && data.UserName.Buffer != nil {
			name = windows.UTF16ToString(unsafe.Slice(data.UserName.Buffer, n))
		}
		_, _, _ = procLsaFreeReturnBuffer.Call(uintptr(unsafe.Pointer(data)))
		if strings.EqualFold(name, user) {
			found = append(found, uint64(l.HighPart)<<32|uint64(l.LowPart))
		}
	}
	return found, nil
}
