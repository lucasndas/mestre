// Windows only: on Windows the key is protected by machine-scope DPAPI and
// the file ACL, not by file modes. The Linux pair is keyfile_unix_test.go
// (mode 0600, owner, symbolic link). The test runs as the developer or CI
// user, the account the store expects when it is not the engine's virtual
// account; the real NT SERVICE\Mestre and mestre-agente are CA-COFRE-08, in
// the heavy suite.

package vault

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// setDACL replaces the DACL of path with the SDDL dacl (e.g. "D:P(A;;FA;;;BU)").
func setDACL(t *testing.T, path, dacl string, protected bool) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(dacl)
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	si := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION)
	if protected {
		si = windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, si, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}

// CA-COFRE-02, Windows counterpart: the key file readable by another account.
func TestKeyFileReadableByOthersIsRefused(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ sddl, sid string }{
		{"BU", "S-1-5-32-545"}, // Users, the group of mestre-agente
		{"WD", "S-1-1-0"},      // Everyone
		{"AU", "S-1-5-11"},     // Authenticated Users
	} {
		t.Run(c.sddl, func(t *testing.T) {
			t.Parallel()
			keyPath := filepath.Join(t.TempDir(), "cofre.key")
			s := NewFileKeyStore(keyPath)
			f := newFixture(t, s)
			v := f.create(t)
			put(t, v, "a", TypeProviderAPIKey, "sk-value-aaaaaaaaaaaaaaaa", nil)
			if err := f.open(t).Err(); err != nil {
				t.Fatalf("open with the owner-only key file: %v", err)
			}

			me := s.account.String()
			setDACL(t, keyPath, "D:P(A;;FA;;;"+me+")(A;;FR;;;"+c.sddl+")", true)
			v2 := f.open(t)
			assertClosed(t, f, v2, ErrClosed)
			var unsafe *UnsafeKeyFileError
			if err := v2.Err(); !errors.As(err, &unsafe) || !strings.Contains(err.Error(), "grants access to") || !strings.Contains(err.Error(), c.sid) {
				t.Fatalf("Err = %v, want the unsafe key file error naming %s", err, c.sid)
			}
			if a := f.alertList()[0]; a.Severity != SeverityGrave || a.Check != "WIN-14" {
				t.Errorf("alert = %+v, want grave WIN-14", a)
			}
		})
	}
}

func TestKeyFileWithInheritedACLIsRefused(t *testing.T) {
	t.Parallel()
	s := NewFileKeyStore(filepath.Join(t.TempDir(), "cofre.key"))
	if err := s.Save(t.Context(), []byte("x")); err != nil {
		t.Fatal(err)
	}
	setDACL(t, s.Path(), "D:(A;;FA;;;"+s.account.String()+")", false)
	var unsafe *UnsafeKeyFileError
	if _, err := s.Load(t.Context()); !errors.As(err, &unsafe) || !strings.Contains(err.Error(), "inherits") {
		t.Errorf("Load = %v, want refusal for an inheriting ACL", err)
	}
}

func TestKeyFileOfAnotherOwnerIsRefused(t *testing.T) {
	t.Parallel()
	s := NewFileKeyStore(filepath.Join(t.TempDir(), "cofre.key"))
	if err := s.Save(t.Context(), []byte("x")); err != nil {
		t.Fatal(err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	// The engine runs as another account than the file owner.
	var unsafe *UnsafeKeyFileError
	if _, err := NewFileKeyStoreForAccount(s.Path(), system).Load(t.Context()); !errors.As(err, &unsafe) || !strings.Contains(err.Error(), "owned by") {
		t.Errorf("Load = %v, want refusal for another owner", err)
	}
}

func TestKeyFileSymlinkIsRefused(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := NewFileKeyStore(filepath.Join(dir, "real.key"))
	if err := target.Save(t.Context(), []byte("x")); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "cofre.key")
	if err := os.Symlink(target.Path(), link); err != nil {
		t.Skipf("this account cannot create symbolic links (needs SeCreateSymbolicLinkPrivilege or developer mode): %v", err)
	}
	var unsafe *UnsafeKeyFileError
	if _, err := NewFileKeyStore(link).Load(t.Context()); !errors.As(err, &unsafe) || !strings.Contains(err.Error(), "reparse point") {
		t.Errorf("Load through a symlink = %v, want refusal", err)
	}
	if _, err := NewFileKeyStore(filepath.Join(dir, "none.key")).Load(t.Context()); !errors.Is(err, ErrNoKey) {
		t.Errorf("Load of a missing file = %v, want ErrNoKey", err)
	}
}

// The file holds the DPAPI blob, never the key itself, and Save replaces it.
func TestKeyFileHoldsOnlyTheDPAPIBlob(t *testing.T) {
	t.Parallel()
	s := NewFileKeyStore(filepath.Join(t.TempDir(), "cofre.key"))
	for _, key := range [][]byte{bytes.Repeat([]byte{0xA5}, 32), bytes.Repeat([]byte{0x5A}, 32)} {
		if err := s.Save(t.Context(), key); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(s.Path())
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, key) {
			t.Fatal("the key file contains the key in clear")
		}
		got, err := s.Load(t.Context())
		if err != nil || !bytes.Equal(got, key) {
			t.Fatalf("Load = %x, %v; want the saved key", got, err)
		}
	}
}
