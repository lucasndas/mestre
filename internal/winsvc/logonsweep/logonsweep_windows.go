//go:build windows

package logonsweep

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	secur32                    = windows.NewLazySystemDLL("secur32.dll")
	procLsaGetLogonSessionData = secur32.NewProc("LsaGetLogonSessionData")
	procLsaFreeReturnBuffer    = secur32.NewProc("LsaFreeReturnBuffer")
)

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

// lsaUnicodeString is LSA_UNICODE_STRING.
type lsaUnicodeString struct {
	Length        uint16
	MaximumLength uint16
	Buffer        *uint16
}

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

func toLUID(l windows.LUID) LUID { return LUID(uint64(l.HighPart)<<32 | uint64(l.LowPart)) }

// tokenLUID is the logon session of the token.
func tokenLUID(t windows.Token) (LUID, error) {
	var st tokenStatistics
	var n uint32
	if err := windows.GetTokenInformation(t, windows.TokenStatistics, (*byte)(unsafe.Pointer(&st)), uint32(unsafe.Sizeof(st)), &n); err != nil {
		return 0, fmt.Errorf("GetTokenInformation(TokenStatistics): %w", err)
	}
	return toLUID(st.AuthenticationID), nil
}

// SessionOfToken describes the logon session of the token: its LUID and the
// type of logon LSA recorded for it.
func SessionOfToken(t windows.Token) (Session, error) {
	id, err := tokenLUID(t)
	if err != nil {
		return Session{}, err
	}
	luid := windows.LUID{LowPart: uint32(id), HighPart: int32(id >> 32)}
	var data *logonSessionData
	if r, _, _ := procLsaGetLogonSessionData.Call(uintptr(unsafe.Pointer(&luid)), uintptr(unsafe.Pointer(&data))); r != 0 {
		return Session{}, fmt.Errorf("LsaGetLogonSessionData: NTSTATUS %#x", r)
	}
	defer func() { _, _, _ = procLsaFreeReturnBuffer.Call(uintptr(unsafe.Pointer(data))) }()
	return Session{ID: id, LogonType: data.LogonType}, nil
}

// SessionOfSelf describes the logon session of this process.
func SessionOfSelf() (Session, error) {
	return SessionOfToken(windows.GetCurrentProcessToken())
}

// Run ends every process of the logon session s, except Options.Keep and this
// process, if the session may be swept at all (Allowed). It returns how many
// processes it ended. The rounds repeat until one finds nothing, because
// ending a process can leave a sibling free to create another.
func Run(s Session, opts Options) (int, error) {
	if err := Allowed(s); err != nil {
		return 0, fmt.Errorf("logon session not swept: %w", err)
	}
	return sweep(s.ID, opts)
}

func sweep(target LUID, opts Options) (int, error) {
	keep := append([]uint32{uint32(os.Getpid())}, opts.Keep...)
	total := 0
	for range MaxRounds {
		n, err := sweepOnce(target, keep, opts.Only, opts.terminate)
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, nil
		}
	}
	return total, fmt.Errorf("logon session %#x still has processes after %d rounds", uint64(target), MaxRounds)
}

// sweepOnce takes one snapshot, ends the processes of the target session and
// waits for them to be gone. The token is read on the very handle that is then
// terminated, so a PID reused between the snapshot and the kill is never hit.
func sweepOnce(target LUID, keep []uint32, only func(uint32) bool, terminate func(uintptr) error) (int, error) {
	if terminate == nil {
		terminate = func(h uintptr) error { return windows.TerminateProcess(windows.Handle(h), 1) }
	}
	pids, err := snapshot()
	if err != nil {
		return 0, err
	}
	handles := map[uint32]windows.Handle{}
	var procs []Proc
	defer func() {
		for _, h := range handles {
			_ = windows.CloseHandle(h)
		}
	}()
	for _, pid := range pids {
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid)
		if err != nil {
			continue // exited meanwhile, or not ours to open
		}
		// A process that already ended stays in the snapshot, and its handle opens,
		// for as long as anyone holds a handle to it (its parent, until it waits for
		// it). It is not a process to end: counting it again in the next round would
		// count one process twice, and a process nobody reaps would never let the
		// rounds find nothing. A process that is only ending is not signalled yet, and
		// is still waited for below.
		if ev, err := windows.WaitForSingleObject(h, 0); err == nil && ev == windows.WAIT_OBJECT_0 {
			_ = windows.CloseHandle(h)
			continue
		}
		var t windows.Token
		if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &t); err != nil {
			_ = windows.CloseHandle(h)
			continue
		}
		id, err := tokenLUID(t)
		_ = t.Close()
		if err != nil {
			_ = windows.CloseHandle(h)
			continue
		}
		handles[pid] = h
		procs = append(procs, Proc{PID: pid, LUID: id, Known: true})
	}
	var errs []error
	var ended []windows.Handle
	// denied has the processes TerminateProcess answered "access denied" for:
	// that is what Windows says of a process that is already ending (the Job
	// Object of the terminal was terminated a moment ago, or the process ended
	// and someone still holds a handle). They are waited for like the others,
	// and only an error if they do not end.
	denied := map[windows.Handle]error{}
	for _, pid := range Select(procs, target, keep...) {
		if only != nil && !only(pid) {
			continue
		}
		h := handles[pid]
		if err := terminate(uintptr(h)); err != nil {
			err = fmt.Errorf("terminate process %d (%s): %w", pid, imageName(h), err)
			if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
				errs = append(errs, err)
				continue
			}
			denied[h] = err
		}
		ended = append(ended, h)
	}
	for _, h := range ended {
		if ev, err := windows.WaitForSingleObject(h, 10_000); err != nil || ev != windows.WAIT_OBJECT_0 {
			if d := denied[h]; d != nil {
				errs = append(errs, d)
			} else {
				errs = append(errs, errors.New("a process of the logon session did not end"))
			}
		}
	}
	return len(ended), errors.Join(errs...)
}

// imageName is the executable of the process, for the messages.
func imageName(h windows.Handle) string {
	buf := make([]uint16, windows.MAX_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return "?"
	}
	return windows.UTF16ToString(buf[:n])
}

// snapshot lists the PIDs of the running processes.
func snapshot() ([]uint32, error) {
	s, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("CreateToolhelp32Snapshot: %w", err)
	}
	defer func() { _ = windows.CloseHandle(s) }()
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	var pids []uint32
	for err := windows.Process32First(s, &e); err == nil; err = windows.Process32Next(s, &e) {
		pids = append(pids, e.ProcessID)
	}
	return pids, nil
}

// Enumerate lists the processes this one can open, with the logon session of
// each, as one round of the sweep sees them.
func Enumerate() ([]Proc, error) {
	pids, err := snapshot()
	if err != nil {
		return nil, err
	}
	var procs []Proc
	for _, pid := range pids {
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
		if err != nil {
			procs = append(procs, Proc{PID: pid})
			continue
		}
		var t windows.Token
		if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &t); err != nil {
			_ = windows.CloseHandle(h)
			procs = append(procs, Proc{PID: pid})
			continue
		}
		id, err := tokenLUID(t)
		_ = t.Close()
		_ = windows.CloseHandle(h)
		procs = append(procs, Proc{PID: pid, LUID: id, Known: err == nil})
	}
	return procs, nil
}
