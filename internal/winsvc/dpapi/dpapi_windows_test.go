// Windows only: DPAPI and file ACLs exist only on Windows. The vault's use
// of this package, and its Linux pair, are tested in internal/vault
// (keyfile_windows_test.go and keyfile_unix_test.go).

package dpapi

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestProtectNeedsTheSameLabel(t *testing.T) {
	t.Parallel()
	secret := []byte("0123456789abcdef0123456789abcdef")
	blob, err := Protect(secret, "label a")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, secret) {
		t.Fatal("the blob contains the secret in clear")
	}
	got, err := Unprotect(blob, "label a")
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("Unprotect = %q, %v; want the secret", got, err)
	}
	if _, err := Unprotect(blob, "label b"); err == nil {
		t.Error("Unprotect with another label succeeded")
	}
	if _, err := Protect(secret, ""); err == nil {
		t.Error("Protect without a label succeeded")
	}
}

func TestWrittenFileIsForTheAccountOnly(t *testing.T) {
	t.Parallel()
	me, err := CurrentAccount()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "f.bin")
	for _, content := range []string{"first", "second"} {
		if err := WriteFile(path, []byte(content), me); err != nil {
			t.Fatal(err)
		}
		got, err := ReadFile(path, me, 1<<10)
		if err != nil || string(got) != content {
			t.Fatalf("ReadFile = %q, %v; want %q", got, err, content)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Errorf("folder has %d entries (%v), want only the file, no temporary left", len(entries), err)
	}

	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + me.String() + ")(A;;FR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	var unsafe *UnsafeFileError
	if err := Verify(path, me); !errors.As(err, &unsafe) || !strings.Contains(err.Error(), "S-1-1-0") {
		t.Fatalf("Verify with Everyone in the ACL = %v, want refusal naming S-1-1-0", err)
	}
	if _, err := ReadFile(path, me, 1<<10); !errors.As(err, &unsafe) {
		t.Fatalf("ReadFile with Everyone in the ACL = %v, want refusal", err)
	}
	if err := Restrict(path, me); err != nil {
		t.Fatal(err)
	}
	if err := Verify(path, me); err != nil {
		t.Errorf("Verify after Restrict = %v", err)
	}
}

func TestOnlyRegularFilesAreRead(t *testing.T) {
	t.Parallel()
	me, err := CurrentAccount()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := ReadFile(filepath.Join(dir, "none"), me, 1); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ReadFile of a missing file = %v, want os.ErrNotExist", err)
	}
	if _, err := ReadFile(dir, me, 1); err == nil {
		t.Error("ReadFile of a directory succeeded")
	}
}
