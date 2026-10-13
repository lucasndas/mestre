//go:build windows

package main

import (
	"errors"
	"fmt"
	"net"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Win32 calls that golang.org/x/sys/windows v0.47.0 does not wrap.
var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	advapi32 = windows.NewLazySystemDLL("advapi32.dll")
	ntdll    = windows.NewLazySystemDLL("ntdll.dll")

	procIsProcessInJob        = kernel32.NewProc("IsProcessInJob")
	procWaitNamedPipeW        = kernel32.NewProc("WaitNamedPipeW")
	procSetConsoleCtrlHandler = kernel32.NewProc("SetConsoleCtrlHandler")
	procCreateRestrictedToken = advapi32.NewProc("CreateRestrictedToken")
	procNtResumeProcess       = ntdll.NewProc("NtResumeProcess")
)

const (
	// SIO_AF_UNIX_GETPEERPID = _WSAIOR(IOC_VENDOR, 256).
	sioAfUnixGetPeerPID = 0x58000100
	tokenStatistics     = 10
	threadAllAccess     = 0x1FFFFF
)

func wstr(s string) *uint16 {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		panic(err)
	}
	return p
}

// peerPID is the PID of the other side of an AF_UNIX connection.
func peerPID(c net.Conn) (uint32, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0, errors.New("not a unix connection")
	}
	rc, err := uc.SyscallConn()
	if err != nil {
		return 0, err
	}
	var pid uint32
	var ierr error
	if err := rc.Control(func(fd uintptr) {
		var n uint32
		ierr = windows.WSAIoctl(windows.Handle(fd), sioAfUnixGetPeerPID, nil, 0, (*byte)(unsafe.Pointer(&pid)), 4, &n, nil, 0)
	}); err != nil {
		return 0, err
	}
	if ierr != nil {
		return 0, fmt.Errorf("WSAIoctl(SIO_AF_UNIX_GETPEERPID): %w", ierr)
	}
	return pid, nil
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

func ntResumeProcess(h windows.Handle) error {
	if r, _, _ := procNtResumeProcess.Call(uintptr(h)); r != 0 {
		return fmt.Errorf("NtResumeProcess: NTSTATUS 0x%x", r)
	}
	return nil
}

// processCreated is the creation time of an open process (FILETIME, 100 ns).
func processCreated(h windows.Handle) (int64, error) {
	var c, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(h, &c, &e, &k, &u); err != nil {
		return 0, err
	}
	return int64(c.HighDateTime)<<32 | int64(c.LowDateTime), nil
}

func nowFiletime() int64 {
	var ft windows.Filetime
	windows.GetSystemTimeAsFileTime(&ft)
	return int64(ft.HighDateTime)<<32 | int64(ft.LowDateTime)
}

// openCreated opens pid and returns its creation time; the caller closes h.
func openCreated(pid, access uint32) (h windows.Handle, created int64, err error) {
	h, err = windows.OpenProcess(access|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0, 0, err
	}
	if created, err = processCreated(h); err != nil {
		windows.CloseHandle(h)
		return 0, 0, err
	}
	return h, created, nil
}

// alive says whether pid still runs with that creation time.
func alive(pid uint32, created int64) bool {
	h, c, err := openCreated(pid, windows.SYNCHRONIZE)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	if c != created {
		return false
	}
	ev, _ := windows.WaitForSingleObject(h, 0)
	return ev == uint32(windows.WAIT_TIMEOUT)
}

func processImage(h windows.Handle) string {
	buf := make([]uint16, 1024)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return "?"
	}
	return windows.UTF16ToString(buf[:n])
}

type tokenStats struct {
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

// logonSession is the AuthenticationId (logon session LUID) of t.
func logonSession(t windows.Token) (string, error) {
	var st tokenStats
	var n uint32
	if err := windows.GetTokenInformation(t, tokenStatistics, (*byte)(unsafe.Pointer(&st)), uint32(unsafe.Sizeof(st)), &n); err != nil {
		return "", fmt.Errorf("GetTokenInformation(TokenStatistics): %w", err)
	}
	return fmt.Sprintf("%08x:%08x", st.AuthenticationID.HighPart, st.AuthenticationID.LowPart), nil
}

// processLogonSession opens pid's token and returns its logon session.
func processLogonSession(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", fmt.Errorf("OpenProcess: %w", err)
	}
	defer windows.CloseHandle(h)
	var t windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &t); err != nil {
		return "", fmt.Errorf("OpenProcessToken: %w", err)
	}
	defer t.Close()
	return logonSession(t)
}

type procInfo struct {
	PID     uint32 `json:"pid"`
	PPID    uint32 `json:"ppid,omitempty"`
	Created int64  `json:"created,omitempty"`
	Image   string `json:"image"`
}

// snapshot lists the running processes (one NtQuerySystemInformation call).
func snapshot() map[uint32]procInfo {
	size := uint32(512 << 10)
	for {
		buf := make([]byte, size)
		var n uint32
		err := windows.NtQuerySystemInformation(windows.SystemProcessInformation, unsafe.Pointer(&buf[0]), size, &n)
		if errors.Is(err, windows.STATUS_INFO_LENGTH_MISMATCH) {
			size = n + 64<<10
			continue
		}
		if err != nil {
			return nil
		}
		m := map[uint32]procInfo{}
		for off := uint32(0); ; {
			p := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buf[off]))
			name := "System Idle Process"
			if p.ImageName.Buffer != nil {
				name = p.ImageName.String()
			}
			pid := uint32(p.UniqueProcessID)
			m[pid] = procInfo{PID: pid, PPID: uint32(p.InheritedFromUniqueProcessID), Created: p.CreateTime, Image: name}
			if p.NextEntryOffset == 0 {
				return m
			}
			off += p.NextEntryOffset
		}
	}
}

// sessionProcesses lists the processes whose token is in logon session ls.
// Processes whose token cannot be opened are skipped (counted in denied).
func sessionProcesses(ls string) (out []procInfo, denied int) {
	for pid, p := range snapshot() {
		if pid == 0 || pid == 4 {
			continue
		}
		s, err := processLogonSession(pid)
		if err != nil {
			denied++
			continue
		}
		if s == ls {
			out = append(out, p)
		}
	}
	return out, denied
}

func jobPIDs(job windows.Handle) []uint32 {
	const max = 512
	buf := make([]uintptr, 2+max) // two ULONGs pack into the first uintptr on amd64
	err := windows.QueryInformationJobObject(job, windows.JobObjectBasicProcessIdList,
		uintptr(unsafe.Pointer(&buf[0])), uint32(len(buf))*uint32(unsafe.Sizeof(buf[0])), nil)
	if err != nil {
		return nil
	}
	hdr := (*[2]uint32)(unsafe.Pointer(&buf[0]))
	var out []uint32
	for _, p := range buf[1 : 1+int(hdr[1])] {
		out = append(out, uint32(p))
	}
	return out
}

// jobActive is ActiveProcesses of JOBOBJECT_BASIC_ACCOUNTING_INFORMATION.
func jobActive(job windows.Handle) (uint32, error) {
	var info struct {
		TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
		TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
	}
	err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil)
	return info.ActiveProcesses, err
}

func enablePrivilege(name string) error {
	var t windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &t); err != nil {
		return err
	}
	defer t.Close()
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, wstr(name), &luid); err != nil {
		return err
	}
	tp := windows.Tokenprivileges{PrivilegeCount: 1}
	tp.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}
	return windows.AdjustTokenPrivileges(t, false, &tp, 0, nil, nil)
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

// threadIDs lists the threads of pid (Toolhelp).
func threadIDs(pid uint32) ([]uint32, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var out []uint32
	var e windows.ThreadEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Thread32First(snap, &e); err == nil; err = windows.Thread32Next(snap, &e) {
		if e.OwnerProcessID == pid {
			out = append(out, e.ThreadID)
		}
	}
	return out, nil
}

// pathSD returns owner and DACL of a file or folder as SDDL.
func pathSD(path string) string {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return "?" + err.Error()
	}
	return sd.String()
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

// tokenDefaultDACL is TOKEN_DEFAULT_DACL.
type tokenDefaultDACL struct{ DefaultDacl *windows.ACL }

func defaultDACL(t windows.Token) (*windows.ACL, []byte, error) {
	var n uint32
	_ = windows.GetTokenInformation(t, windows.TokenDefaultDacl, nil, 0, &n)
	if n == 0 {
		return nil, nil, errors.New("GetTokenInformation(TokenDefaultDacl): empty size")
	}
	buf := make([]byte, n)
	if err := windows.GetTokenInformation(t, windows.TokenDefaultDacl, &buf[0], n, &n); err != nil {
		return nil, nil, fmt.Errorf("GetTokenInformation(TokenDefaultDacl): %w", err)
	}
	return (*tokenDefaultDACL)(unsafe.Pointer(&buf[0])).DefaultDacl, buf, nil
}

func setDefaultDACL(t windows.Token, acl *windows.ACL) error {
	info := tokenDefaultDACL{acl}
	if err := windows.SetTokenInformation(t, windows.TokenDefaultDacl, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return fmt.Errorf("SetTokenInformation(TokenDefaultDacl): %w", err)
	}
	return nil
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

func daclFromSDDL(sddl string) (*windows.ACL, error) {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, err
	}
	acl, _, err := sd.DACL()
	return acl, err
}

// createRestrictedToken with no restrictions: a child token of t, which the
// creator may assign to a new process without SeAssignPrimaryTokenPrivilege.
func createRestrictedToken(t windows.Token) (windows.Token, error) {
	var out windows.Token
	r, _, err := procCreateRestrictedToken.Call(uintptr(t), 0, 0, 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return 0, fmt.Errorf("CreateRestrictedToken: %w", err)
	}
	return out, nil
}

func setConsoleCtrlIgnore() error {
	if r, _, err := procSetConsoleCtrlHandler.Call(0, 1); r == 0 {
		return err
	}
	return nil
}

func accountName(sid *windows.SID) string {
	a, d, _, err := sid.LookupAccount("")
	if err != nil {
		return sid.String()
	}
	return d + `\` + a
}
