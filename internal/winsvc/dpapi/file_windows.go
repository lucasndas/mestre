package dpapi

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// UnsafeFileError means the file is not protected as required: another
// account has access, the owner is wrong, the DACL inherits from the folder
// or the path is not a regular file. Reason is safe to show and log.
type UnsafeFileError struct {
	Path   string
	Reason string
}

func (e *UnsafeFileError) Error() string {
	return fmt.Sprintf("dpapi: refusing %s: %s", e.Path, e.Reason)
}

// CurrentAccount is the SID of the account running this process: the
// engine's virtual account (NT SERVICE\Mestre, or Mestre$<inst> for a named
// instance) in production, the developer or CI user in tests.
func CurrentAccount() (*windows.SID, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("dpapi: account of this process: %w", err)
	}
	return u.User.Sid.Copy()
}

// ownerOnly is the descriptor of a protected file: owned by account, with a
// protected DACL (no inherited entries) that grants full access to account
// and nobody else. SYSTEM and Administrators get no entry: they reach the
// file only through privileges (backup, take ownership), not through the ACL.
func ownerOnly(account *windows.SID) (*windows.SECURITY_DESCRIPTOR, error) {
	if account == nil || !account.IsValid() {
		return nil, errors.New("dpapi: no valid account")
	}
	s := account.String()
	sd, err := windows.SecurityDescriptorFromString("O:" + s + "D:P(A;;FA;;;" + s + ")")
	if err != nil {
		return nil, fmt.Errorf("dpapi: build security descriptor: %w", err)
	}
	return sd, nil
}

// WriteFile replaces path with data atomically. The temporary file is born
// with the owner-only descriptor (never a moment with a wider ACL), is
// flushed, and is moved over path with MoveFileEx, which keeps its
// descriptor.
func WriteFile(path string, data []byte, account *windows.SID) (err error) {
	sd, err := ownerOnly(account)
	if err != nil {
		return err
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp-"+hex.EncodeToString(suffix[:]))
	tmp16, err := windows.UTF16PtrFromString(tmp)
	if err != nil {
		return err
	}
	path16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	sa := &windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(*sa))
	h, err := windows.CreateFile(tmp16, windows.GENERIC_WRITE, 0, sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return &os.PathError{Op: "create", Path: tmp, Err: err}
	}
	f := os.NewFile(uintptr(h), tmp)
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = windows.MoveFileEx(tmp16, path16, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return &os.LinkError{Op: "rename", Old: tmp, New: path, Err: err}
	}
	return nil
}

// ReadFile reads at most limit bytes of path after checking, on the open
// handle, that it is a regular file (not a symbolic link, junction or other
// reparse point, which are not followed), owned by account, with a
// protected DACL that grants access to account only. A failed check is an
// *UnsafeFileError; a missing file matches os.ErrNotExist.
func ReadFile(path string, account *windows.SID, limit int64) ([]byte, error) {
	f, err := openChecked(path, account, windows.GENERIC_READ)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}

// Verify runs the checks of ReadFile without reading (checklist WIN-14).
func Verify(path string, account *windows.SID) error {
	f, err := openChecked(path, account, 0)
	if err != nil {
		return err
	}
	return f.Close()
}

// Restrict gives an existing regular file the owner-only descriptor of
// WriteFile (the fix of WIN-14). Making account the owner needs either to
// be account or SeRestorePrivilege.
func Restrict(path string, account *windows.SID) error {
	sd, err := ownerOnly(account)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	f, err := open(path, windows.WRITE_DAC|windows.WRITE_OWNER)
	if err != nil {
		return err
	}
	defer f.Close()
	if reason := fileKind(windows.Handle(f.Fd())); reason != "" {
		return &UnsafeFileError{Path: path, Reason: reason}
	}
	si := windows.SECURITY_INFORMATION(windows.OWNER_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if err := windows.SetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, si, account, nil, dacl, nil); err != nil {
		return fmt.Errorf("dpapi: restrict %s: %w", path, err)
	}
	return nil
}

// open opens path itself, never the target of a reparse point. It shares
// read and delete: a reader (the status of the vault, a promotion check, an
// antivirus) must not make the erasing of the key or its replacement fail with
// a sharing violation while the file is open for a moment.
func open(path string, access uint32) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, access|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

func openChecked(path string, account *windows.SID, access uint32) (*os.File, error) {
	if account == nil || !account.IsValid() {
		return nil, errors.New("dpapi: no valid account")
	}
	f, err := open(path, access)
	if err != nil {
		return nil, err
	}
	h := windows.Handle(f.Fd())
	reason := fileKind(h)
	if reason == "" {
		var sd *windows.SECURITY_DESCRIPTOR
		sd, err = windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
		if err == nil {
			reason = checkDescriptor(sd, account)
		}
	}
	if err != nil || reason != "" {
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("dpapi: security of %s: %w", path, err)
		}
		return nil, &UnsafeFileError{Path: path, Reason: reason}
	}
	return f, nil
}

// fileKind returns why h is not a regular file, or "".
func fileKind(h windows.Handle) string {
	var info struct {
		FileAttributes uint32
		ReparseTag     uint32
	}
	if err := windows.GetFileInformationByHandleEx(h, windows.FileAttributeTagInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return "cannot read its attributes: " + err.Error()
	}
	switch {
	case info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0:
		return "it is a reparse point (symbolic link or junction), must be a regular file"
	case info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0:
		return "it is a directory, must be a regular file"
	}
	if t, err := windows.GetFileType(h); err != nil || t != windows.FILE_TYPE_DISK {
		return "it is not a regular file"
	}
	return ""
}

// checkDescriptor returns why sd does not protect a file for account only,
// or "".
func checkDescriptor(sd *windows.SECURITY_DESCRIPTOR, account *windows.SID) string {
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return "it has no owner"
	}
	if !owner.Equals(account) {
		return fmt.Sprintf("owned by %s, must be owned by the engine account %s", name(owner), name(account))
	}
	control, _, err := sd.Control()
	if err != nil {
		return "cannot read its security descriptor: " + err.Error()
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return "its ACL inherits entries from the folder, must be protected (no inheritance)"
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return "it has no ACL (everyone has access)"
	}
	var others []string
	for i := range uint32(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return "cannot read its ACL: " + err.Error()
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			// Denying never widens access.
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if ace.Mask != 0 && !sid.Equals(account) {
				others = append(others, name(sid))
			}
		default:
			return fmt.Sprintf("its ACL has an entry of unexpected type %d", ace.Header.AceType)
		}
	}
	if len(others) > 0 {
		return fmt.Sprintf("its ACL grants access to %s, must grant it only to the engine account %s",
			strings.Join(others, ", "), name(account))
	}
	return ""
}

// name is "DOMAIN\account (SID)", or the SID alone when it does not
// resolve.
func name(sid *windows.SID) string {
	s := sid.String()
	account, domain, _, err := sid.LookupAccount("")
	switch {
	case err != nil || account == "":
		return s
	case domain != "":
		return domain + `\` + account + " (" + s + ")"
	default:
		return account + " (" + s + ")"
	}
}
