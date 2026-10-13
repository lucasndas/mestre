//go:build windows

package main

import (
	"fmt"
	"io"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A logon session lives as long as any token of it is open anywhere in the
// system. When one outlives its terminal, this finds who holds it: it lists
// every handle of the system, duplicates the ones that are tokens and reads
// their logon session.

var procNtQuerySystemInformation = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtQuerySystemInformation")

const (
	systemExtendedHandleInformation = 0x40
	statusInfoLengthMismatch        = 0xC0000004
)

// handleEntry is SYSTEM_HANDLE_TABLE_ENTRY_INFO_EX.
type handleEntry struct {
	Object           uintptr
	ProcessID        uintptr
	HandleValue      uintptr
	GrantedAccess    uint32
	CreatorBackTrace uint16
	ObjectTypeIndex  uint16
	Attributes       uint32
	Reserved         uint32
}

func systemHandles() ([]handleEntry, error) {
	size := 1 << 22
	for {
		buf := make([]byte, size)
		var need uint32
		r, _, _ := procNtQuerySystemInformation.Call(systemExtendedHandleInformation, uintptr(unsafe.Pointer(&buf[0])), uintptr(size), uintptr(unsafe.Pointer(&need)))
		if r == statusInfoLengthMismatch {
			size = int(need) + 1<<20
			continue
		}
		if r != 0 {
			return nil, fmt.Errorf("NtQuerySystemInformation: NTSTATUS %#x", r)
		}
		n := *(*uintptr)(unsafe.Pointer(&buf[0]))
		entries := unsafe.Slice((*handleEntry)(unsafe.Pointer(&buf[2*unsafe.Sizeof(uintptr(0))])), n)
		return append([]handleEntry(nil), entries...), nil
	}
}

// describeTokenHolders prints which processes hold a token of one of the
// logon sessions.
func describeTokenHolders(out io.Writer, sessions []uint64) {
	enableDebug()
	entries, err := systemHandles()
	if err != nil {
		fmt.Fprintf(out, "token holders: %v\n", err)
		return
	}
	// The type index of tokens: the one of the handle of our own token.
	var own windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &own); err != nil {
		fmt.Fprintf(out, "token holders: %v\n", err)
		return
	}
	defer own.Close()
	var tokenType uint16
	for _, e := range entries {
		if e.ProcessID == uintptr(windows.GetCurrentProcessId()) && e.HandleValue == uintptr(own) {
			tokenType = e.ObjectTypeIndex
		}
	}
	if tokenType == 0 {
		fmt.Fprintln(out, "token holders: the type of the token handles is unknown")
		return
	}
	want := map[uint64]bool{}
	for _, s := range sessions {
		want[s] = true
	}
	procs := map[uintptr]windows.Handle{}
	defer func() {
		for _, h := range procs {
			_ = windows.CloseHandle(h)
		}
	}()
	found := 0
	for _, e := range entries {
		if e.ObjectTypeIndex != tokenType || e.ProcessID == uintptr(windows.GetCurrentProcessId()) {
			continue
		}
		ph, ok := procs[e.ProcessID]
		if !ok {
			ph, _ = windows.OpenProcess(windows.PROCESS_DUP_HANDLE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(e.ProcessID))
			procs[e.ProcessID] = ph
		}
		if ph == 0 {
			continue
		}
		var dup windows.Handle
		if err := windows.DuplicateHandle(ph, windows.Handle(e.HandleValue), windows.CurrentProcess(), &dup, windows.TOKEN_QUERY, false, 0); err != nil {
			continue
		}
		var st tokenStats
		var n uint32
		err := windows.GetTokenInformation(windows.Token(dup), windows.TokenStatistics, (*byte)(unsafe.Pointer(&st)), uint32(unsafe.Sizeof(st)), &n)
		_ = windows.CloseHandle(dup)
		if err != nil {
			continue
		}
		if id := uint64(st.AuthenticationID.HighPart)<<32 | uint64(st.AuthenticationID.LowPart); want[id] {
			found++
			name := "?"
			buf := make([]uint16, windows.MAX_PATH)
			sz := uint32(len(buf))
			if windows.QueryFullProcessImageName(ph, 0, &buf[0], &sz) == nil {
				name = windows.UTF16ToString(buf[:sz])
			}
			fmt.Fprintf(out, "token of session %#x held by PID %d (%s), handle %#x\n", id, e.ProcessID, name, e.HandleValue)
		}
	}
	if found == 0 {
		fmt.Fprintln(out, "token holders: no handle of a token of those sessions that this process can duplicate")
	}
}

// tokenStats is TOKEN_STATISTICS.
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

// enableDebug turns SeDebugPrivilege on, which opens the processes of the system.
func enableDebug() {
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &tok); err != nil {
		return
	}
	defer tok.Close()
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr("SeDebugPrivilege"), &luid); err != nil {
		return
	}
	tp := windows.Tokenprivileges{PrivilegeCount: 1}
	tp.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}
	_ = windows.AdjustTokenPrivileges(tok, false, &tp, 0, nil, nil)
}
