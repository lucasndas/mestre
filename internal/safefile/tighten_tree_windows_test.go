//go:build windows

package safefile_test

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/mestre-ia/mestre/internal/safefile"
)

// denies counts the access-denied entries of the DACL of path for sid.
func denies(t *testing.T, path string, sid *windows.SID) int {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		t.Fatalf("no DACL: %v", err)
	}
	n := 0
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE && windows.EqualSid((*windows.SID)(unsafe.Pointer(&ace.SidStart)), sid) {
			n++
		}
	}
	return n
}

// The ACL of regras\ denies the write to the agent account, once, and what is born under it
// inherits the entry.
func TestDenyWriteOnRulesFolder(t *testing.T) {
	t.Parallel()
	guests, err := windows.CreateWellKnownSid(windows.WinBuiltinGuestsSid)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "regras")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := safefile.DenyWrite(dir, guests); err != nil {
			t.Fatal(err)
		}
	}
	if n := denies(t, dir, guests); n != 1 {
		t.Errorf("deny entries on regras = %d, want 1 (idempotent)", n)
	}
	child := filepath.Join(dir, "politica.yaml")
	if err := os.WriteFile(child, []byte("versao: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := denies(t, child, guests); n != 1 {
		t.Errorf("deny entries on a child = %d, want 1 (inherited)", n)
	}
}

// A missing regras\ and an unset account are no error.
func TestTightenRulesWithoutAccountOrFolder(t *testing.T) {
	t.Parallel()
	if err := safefile.TightenRules(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := safefile.DenyWrite(filepath.Join(t.TempDir(), "nope"), nil); err != nil {
		t.Fatal(err)
	}
}
