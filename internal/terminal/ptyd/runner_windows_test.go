package ptyd

import (
	"testing"
	"unsafe"

	"github.com/aymanbagabas/go-pty"
	"golang.org/x/sys/windows"
)

// The Job Object has a DACL of its own: SYSTEM and the logon session of the
// mestre-pty, and not the user of the process, which every terminal shares
// (docs/arquitetura/windows.md#conpty). Creating the Job starts no process.
func TestTheJobObjectIsForTheLogonSessionOnly(t *testing.T) {
	t.Parallel()
	sys, err := prepare(&pty.Cmd{}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sys.abort)

	sd, err := windows.GetSecurityInfo(sys.job, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		t.Fatalf("DACL = %v, %v", acl, err)
	}
	var got []string
	for i := range uint32(acl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			t.Fatal(err)
		}
		got = append(got, (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String())
	}

	logon, lerr := logonSID(windows.GetCurrentProcessToken())
	if lerr != nil {
		t.Skipf("this process has no logon SID: %v", lerr)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"S-1-5-18": true, logon.String(): true}
	if len(got) != len(want) {
		t.Fatalf("ACEs = %v, want SYSTEM and the logon SID only", got)
	}
	for _, s := range got {
		if !want[s] {
			t.Errorf("unexpected ACE for %s", s)
		}
		if s == user.User.Sid.String() {
			t.Errorf("the user of the process (%s) is in the DACL of the Job", s)
		}
	}
}
