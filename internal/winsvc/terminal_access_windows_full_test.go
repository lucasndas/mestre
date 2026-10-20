//go:build windows && full

package winsvc

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/sys/windows"
)

// CA-CODEX-WIN-03
//
// The ACE of the logon SID of a terminal goes on the file of the socket and
// on its folder, with the access the measurements asked (read and write,
// this object only), stays out of everything else, goes away on Revoke, and
// the sweep of Only takes away the SIDs of dead terminals and nothing else.
// The base ACL (SYSTEM, the engine, the agents) and its protection from
// inheritance stay as they were.
func TestTheACEOfTheTerminalIsPutOnTheSocketAndItsFolderAndTakenAway(t *testing.T) {
	_, agents := fakeSIDs(t)
	owner, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	sock := filepath.Join(dir, "cli.sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := setFileSecurity(sock, cliSocketSDDL(owner, agents), owner); err != nil {
		t.Fatal(err)
	}
	if err := setFileSecurity(dir, cliSocketSDDL(owner, agents), nil); err != nil {
		t.Fatal(err)
	}
	baseSock, _ := applied(t, sock)
	base := entries(t, baseSock)
	a := TerminalAccess{Socket: sock}
	const t1, t2 = "S-1-5-5-0-11111", "S-1-5-5-0-22222"
	mask := uint32(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE)

	for _, sid := range []string{t1, t2, t1} { // twice t1: idempotent
		if err := a.Grant(sid); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{sock, dir} {
		acl, protected := applied(t, p)
		if !protected {
			t.Errorf("%s: the DACL lost its protection from inheritance", p)
		}
		got := entries(t, acl)
		for _, sid := range []string{t1, t2} {
			n := 0
			for _, e := range got {
				if e.sid == sid {
					n++
					if e.typ != allowed || e.flags != 0 || e.mask != mask {
						t.Errorf("%s: ACE of %s = %+v, want allow, no inheritance, read and write", p, sid, e)
					}
				}
			}
			if n != 1 {
				t.Errorf("%s: %d ACEs for %s, want 1", p, n, sid)
			}
		}
	}

	if err := a.Revoke(t1); err != nil {
		t.Fatal(err)
	}
	if err := a.Revoke(t1); err != nil { // already gone: not an error
		t.Fatal(err)
	}
	acl, _ := applied(t, sock)
	if sids := sidsOf(entries(t, acl)); slices.Contains(sids, t1) || !slices.Contains(sids, t2) {
		t.Errorf("after Revoke(t1): %v", sids)
	}

	if err := a.Grant(t1); err != nil {
		t.Fatal(err)
	}
	if err := a.Only([]string{t1}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{sock, dir} {
		acl, _ := applied(t, p)
		sids := sidsOf(entries(t, acl))
		if !slices.Contains(sids, t1) || slices.Contains(sids, t2) {
			t.Errorf("%s after Only(t1): %v", p, sids)
		}
	}
	if err := a.Only(nil); err != nil {
		t.Fatal(err)
	}
	acl, _ = applied(t, sock)
	if got := entries(t, acl); !slices.Equal(got, base) {
		t.Errorf("after Only(nil) the base ACL changed:\n%+v\nwant\n%+v", got, base)
	}
}

// CA-CODEX-WIN-03
//
// Anything that is not a logon SID is refused before any ACL is touched, and
// so is a socket path that is missing or relative.
func TestTheACEOfTheTerminalRefusesWhatIsNotALogonSID(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "cli.sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := applied(t, sock)
	want := entries(t, before)
	a := TerminalAccess{Socket: sock}
	for _, bad := range []string{"", "S-1-1-0", "S-1-5-18", "S-1-5-32-545", "S-1-5-21-1-2-3-1001", "S-1-5-5-1", "Everyone"} {
		if err := a.Grant(bad); err == nil {
			t.Errorf("Grant(%q) accepted", bad)
		}
		if err := a.Revoke(bad); err == nil {
			t.Errorf("Revoke(%q) accepted", bad)
		}
	}
	if err := a.Only([]string{"S-1-1-0"}); err == nil {
		t.Error("Only with a bad SID accepted")
	}
	after, _ := applied(t, sock)
	if got := entries(t, after); !slices.Equal(got, want) {
		t.Errorf("the ACL changed: %+v", got)
	}
	if err := (TerminalAccess{Socket: filepath.Join(dir, "missing", "cli.sock")}).Grant("S-1-5-5-0-1"); err == nil {
		t.Error("a missing socket was granted")
	}
	if err := (TerminalAccess{Socket: "cli.sock"}).Grant("S-1-5-5-0-1"); err == nil {
		t.Error("a relative socket path was granted")
	}
}

func sidsOf(es []entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.sid)
	}
	return out
}
