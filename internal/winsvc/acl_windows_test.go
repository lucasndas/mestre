package winsvc

import (
	"net"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

type entry struct {
	typ, flags byte
	mask       uint32
	sid        string
}

// entries lists the ACEs of a DACL, in order.
func entries(t *testing.T, acl *windows.ACL) []entry {
	t.Helper()
	var out []entry
	for i := range uint32(acl.AceCount) {
		var a *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &a); err != nil {
			t.Fatal(err)
		}
		out = append(out, entry{a.Header.AceType, a.Header.AceFlags, uint32(a.Mask), (*windows.SID)(unsafe.Pointer(&a.SidStart)).String()})
	}
	return out
}

func fakeSIDs(t *testing.T) (engine, agents *windows.SID) {
	t.Helper()
	engine, err := windows.StringToSid("S-1-5-80-1111111111-2222222222-3333333333-4444444444-5555555555")
	if err != nil {
		t.Fatal(err)
	}
	agents, err = windows.StringToSid("S-1-5-21-1-2-3-1001")
	if err != nil {
		t.Fatal(err)
	}
	return engine, agents
}

func parse(t *testing.T, sddl string) (acl *windows.ACL, protected bool) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err = sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	ctl, _, _ := sd.Control()
	return acl, ctl&windows.SE_DACL_PROTECTED != 0
}

const (
	allowed      = windows.ACCESS_ALLOWED_ACE_TYPE
	inheritBoth  = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
	fileAll      = 0x1f01ff // FILE_ALL_ACCESS
	ownerRightsS = "S-1-3-4"
)

// The ACL of cli.sock: owner and SYSTEM everything, mestre-agente read and
// write, nothing from the folder.
func TestTheACLOfTheCLISocket(t *testing.T) {
	t.Parallel()
	engine, agents := fakeSIDs(t)
	acl, protected := parse(t, cliSocketSDDL(engine, agents))
	if !protected {
		t.Error("the DACL of cli.sock inherits from its folder")
	}
	want := []entry{
		{allowed, 0, fileAll, "S-1-5-18"},
		{allowed, 0, fileAll, engine.String()},
		{allowed, 0, windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE, agents.String()},
	}
	if got := entries(t, acl); !slices.Equal(got, want) {
		t.Fatalf("ACEs = %+v\nwant   %+v", got, want)
	}
}

// The ACL of the pty folder, as docs/arquitetura/windows.md describes it:
// SYSTEM and the engine everything (inherited), OWNER RIGHTS only
// READ_CONTROL (inherited), and mestre-agente only adds a file, here only.
func TestTheACLOfThePTYFolder(t *testing.T) {
	t.Parallel()
	engine, agents := fakeSIDs(t)
	acl, protected := parse(t, ptyDirSDDL(engine, agents))
	if !protected {
		t.Error("the DACL of the pty folder inherits from its parent")
	}
	want := []entry{
		{allowed, inheritBoth, fileAll, "S-1-5-18"},
		{allowed, inheritBoth, fileAll, engine.String()},
		{allowed, inheritBoth, windows.READ_CONTROL, ownerRightsS},
		{allowed, 0, fileAddFile, agents.String()},
	}
	got := entries(t, acl)
	if !slices.Equal(got, want) {
		t.Fatalf("ACEs = %+v\nwant   %+v", got, want)
	}
	for _, e := range got {
		if e.sid == agents.String() && e.flags != 0 {
			t.Errorf("the right of mestre-agente is inherited: %+v", e)
		}
	}
}

// applied reads the DACL set on a file or folder.
func applied(t *testing.T, path string) (acl *windows.ACL, protected bool) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err = sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	ctl, _, _ := sd.Control()
	return acl, ctl&windows.SE_DACL_PROTECTED != 0
}

// The ACLs are applied for real, with the user of the test in every role,
// and the socket still works for it. Two accounts cannot be told apart
// without a second logon (the real-account tests).
func TestTheACLsAreApplied(t *testing.T) {
	t.Parallel()
	self, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("cli.sock", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "cli.sock")
		ln, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		if err := (CLIAccess{AgentSID: self}).Apply(path); err != nil {
			t.Fatal(err)
		}
		acl, protected := applied(t, path)
		if !protected || acl.AceCount != 3 {
			t.Fatalf("protected = %v, %d ACEs", protected, acl.AceCount)
		}
		dial(t, ln, path)
	})

	t.Run("pty folder", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "pty")
		if err := (PTYDirACL{AgentSID: self}).Ensure(dir); err != nil {
			t.Fatal(err)
		}
		if err := (PTYDirACL{AgentSID: self}).Ensure(dir); err != nil { // idempotent
			t.Fatal(err)
		}
		acl, protected := applied(t, dir)
		if !protected || acl.AceCount != 4 {
			t.Fatalf("protected = %v, %d ACEs", protected, acl.AceCount)
		}
		path := filepath.Join(dir, "t1.sock")
		ln, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		dial(t, ln, path)
	})

	t.Run("no agent account", func(t *testing.T) {
		t.Parallel()
		if err := (PTYDirACL{}).Ensure(filepath.Join(t.TempDir(), "pty")); err == nil {
			t.Error("a folder was made without an agent account to give the right to")
		}
		if err := (CLIAccess{AgentAccount: "mestre-agente-that-does-not-exist"}).Apply(filepath.Join(t.TempDir(), "x")); err == nil {
			t.Error("an unknown account was accepted")
		}
	})
}

// dial connects to the socket and accepts the connection.
func dial(t *testing.T, ln net.Listener, path string) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			_ = c.Close()
		}
		done <- err
	}()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("connect to %s: %v", path, err)
	}
	_ = c.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// The account of the agents is looked up as an account of this computer: a
// name that points elsewhere (a domain, a UPN) is refused, and the account of
// the current user, when it is a local one, resolves to its own SID.
// CA-TERM-17
func TestAccountSIDOnlyResolvesLocalAccounts(t *testing.T) {
	t.Parallel()
	for _, name := range []string{`DOMAIN\mestre-agente`, "mestre-agente@example.com", `.\mestre-agente`, "NT AUTHORITY/SYSTEM"} {
		if _, err := accountSID(name); err == nil {
			t.Errorf("accountSID(%q) accepted a name that is not of a local account", name)
		}
	}
	host, err := windows.ComputerName()
	if err != nil {
		t.Fatal(err)
	}
	tok := windows.GetCurrentProcessToken()
	tu, err := tok.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	name, domain, _, err := tu.User.Sid.LookupAccount("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(domain, host) {
		t.Skipf("the user of the test (%s\\%s) is not a local account", domain, name)
	}
	got, err := accountSID(name)
	if err != nil || !got.Equals(tu.User.Sid) {
		t.Fatalf("accountSID(%q) = %v, %v; want %v", name, got, err, tu.User.Sid)
	}
}
