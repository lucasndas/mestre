package winsvc

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// CA-CODEX-WIN-04
//
// The CLI adds to the DACL of its own process one ACE for the service of the
// engine with PROCESS_QUERY_LIMITED_INFORMATION and nothing else; calling it
// again adds nothing; the ACEs that were there stay.
func TestTheCLIAllowsTheEngineToQueryItselfAndNothingMore(t *testing.T) {
	sid, err := windows.StringToSid("S-1-5-80-9-8-7-6-5")
	if err != nil {
		t.Fatal(err)
	}
	read := func() (n int, mine []uint32) {
		h, err := windows.OpenProcess(windows.READ_CONTROL, false, windows.GetCurrentProcessId())
		if err != nil {
			t.Fatal(err)
		}
		defer windows.CloseHandle(h)
		sd, err := windows.GetSecurityInfo(h, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		acl, _, err := sd.DACL()
		if err != nil {
			t.Fatal(err)
		}
		for i := range uint32(acl.AceCount) {
			var a *windows.ACCESS_ALLOWED_ACE
			if err := windows.GetAce(acl, i, &a); err != nil {
				t.Fatal(err)
			}
			if windows.EqualSid((*windows.SID)(unsafe.Pointer(&a.SidStart)), sid) {
				if a.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
					t.Errorf("ACE of the engine is not an allow: type %d", a.Header.AceType)
				}
				mine = append(mine, uint32(a.Mask))
			}
		}
		return int(acl.AceCount), mine
	}
	before, none := read()
	if len(none) != 0 {
		t.Fatalf("the test SID already has an ACE: %v", none)
	}
	for range 2 {
		if err := allowQueryOfSelf(sid); err != nil {
			t.Fatal(err)
		}
	}
	after, mine := read()
	if after != before+1 || len(mine) != 1 || mine[0] != windows.PROCESS_QUERY_LIMITED_INFORMATION {
		t.Errorf("ACEs %d -> %d, rights of the engine %#x; want one ACE of 0x1000", before, after, mine)
	}
}
