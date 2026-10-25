package winsvc

import (
	"runtime"
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
	t.Parallel()
	sid, err := windows.StringToSid("S-1-5-80-9-8-7-6-5")
	if err != nil {
		t.Fatal(err)
	}
	// the DACL belongs to the process of the whole test binary: give it back as it was
	// found, or a second run in the same process (-count) finds the ACE of the first
	orig, err := windows.GetSecurityInfo(windows.CurrentProcess(), windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	origACL, _, err := orig.DACL()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		err := windows.SetSecurityInfo(windows.CurrentProcess(), windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, origACL, nil)
		runtime.KeepAlive(orig)
		if err != nil {
			t.Errorf("restore the DACL of the process: %v", err)
		}
	})
	read := func() (n int, mine []uint32) {
		sd, err := windows.GetSecurityInfo(windows.CurrentProcess(), windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION)
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
