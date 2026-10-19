package winsvc

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

type ace struct {
	sid  string
	mask uint32
}

// Part of CA-TERM-12 (the DACL that is built, in the fast suite on Windows;
// the Job Object clause is in internal/terminal/ptyd, and that a process of
// another terminal cannot open it is in the real-account tests, which can make
// two logon sessions of the agent account)
func TestTheDefaultDACLOfATerminalExcludesTheAccount(t *testing.T) {
	t.Parallel()
	logon, err := windows.StringToSid("S-1-5-5-0-123456")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := windows.StringToSid("S-1-5-80-1111111111-2222222222-3333333333-4444444444-5555555555")
	if err != nil {
		t.Fatal(err)
	}
	acl, sd, err := terminalDACL(logon, engine)
	if err != nil {
		t.Fatal(err)
	}
	var got []ace
	for i := range uint32(acl.AceCount) {
		var a *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &a); err != nil {
			t.Fatal(err)
		}
		got = append(got, ace{(*windows.SID)(unsafe.Pointer(&a.SidStart)).String(), uint32(a.Mask)})
	}
	_ = sd
	want := []ace{
		{"S-1-5-18", windows.GENERIC_ALL},
		{logon.String(), windows.GENERIC_ALL},
		{engine.String(), windows.PROCESS_QUERY_LIMITED_INFORMATION | windows.SYNCHRONIZE},
		{"S-1-3-4", 0}, // OWNER RIGHTS: nothing
	}
	if len(got) != len(want) {
		t.Fatalf("ACEs = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ACE %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if _, _, err := terminalDACL(nil, engine); err == nil {
		t.Error("a DACL without the logon SID was built")
	}
}
