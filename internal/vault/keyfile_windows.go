package vault

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/mestre-ia/mestre/internal/winsvc/dpapi"
	"golang.org/x/sys/windows"
)

// maxKeyFile bounds what Load reads; a protected keyring is well under 1 KiB.
const maxKeyFile = 64 << 10

// keyLabel is the DPAPI entropy of the key file: a blob protected for
// another purpose does not open as the vault key.
const keyLabel = "mestre vault key v1"

// FileKeyStore keeps the data key in a file (default
// %ProgramData%\Mestre\<inst>\config\cofre.key) encrypted with machine-scope
// DPAPI, owned by the account that runs the engine and with a protected
// DACL that grants access to that account only
// (docs/plataforma/cofre.md#chave-do-cofre). Load refuses the file when the
// ACL grants anyone else (mestre-agente, Users, Everyone...), inherits from
// the folder, another account owns it or it is not a regular file
// (checklist WIN-14).
type FileKeyStore struct {
	path       string
	account    *windows.SID
	accountErr error
}

// NewFileKeyStore returns the key store at path for the account running
// this process: the engine's virtual account (NT SERVICE\Mestre) in
// production, the current user in tests.
func NewFileKeyStore(path string) *FileKeyStore {
	sid, err := dpapi.CurrentAccount()
	return &FileKeyStore{path: path, account: sid, accountErr: err}
}

// NewFileKeyStoreForAccount returns the key store at path for account: the
// file is written owned by it and Load accepts only that owner and ACL.
// Writing for an account other than the running one (the installer writing
// for NT SERVICE\Mestre) needs SeRestorePrivilege enabled.
func NewFileKeyStoreForAccount(path string, account *windows.SID) *FileKeyStore {
	return &FileKeyStore{path: path, account: account}
}

// Path is the key file.
func (s *FileKeyStore) Path() string { return s.path }

// Load checks owner, ACL and file type on the open handle (reparse points
// are not followed), then decrypts the content with DPAPI.
func (s *FileKeyStore) Load(context.Context) ([]byte, error) {
	if s.accountErr != nil {
		return nil, fmt.Errorf("vault: %w", s.accountErr)
	}
	blob, err := dpapi.ReadFile(s.path, s.account, maxKeyFile)
	if err != nil {
		var unsafe *dpapi.UnsafeFileError
		switch {
		case errors.Is(err, os.ErrNotExist):
			return nil, ErrNoKey
		case errors.As(err, &unsafe):
			return nil, &UnsafeKeyFileError{Path: s.path, Reason: unsafe.Reason, Check: "WIN-14"}
		}
		return nil, fmt.Errorf("vault: read key file: %w", err)
	}
	data, err := dpapi.Unprotect(blob, keyLabel)
	if err != nil {
		return nil, fmt.Errorf("vault: decrypt key file: %w", err)
	}
	return data, nil
}

// Save encrypts data with DPAPI and replaces the key file atomically, born
// with the owner-only ACL.
func (s *FileKeyStore) Save(_ context.Context, data []byte) error {
	if s.accountErr != nil {
		return fmt.Errorf("vault: %w", s.accountErr)
	}
	blob, err := dpapi.Protect(data, keyLabel)
	if err != nil {
		return fmt.Errorf("vault: encrypt key file: %w", err)
	}
	if err := dpapi.WriteFile(s.path, blob, s.account); err != nil {
		return fmt.Errorf("vault: write key file: %w", err)
	}
	return nil
}

// replaceFile moves from over to with MoveFileEx: atomic replacement, and
// MOVEFILE_WRITE_THROUGH returns only after the move is flushed to disk (an
// os.Rename may still be in the cache when the power goes); without durable,
// the move is not waited for.
func replaceFile(from, to string, durable bool) error {
	f, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	t, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	flags := uint32(windows.MOVEFILE_REPLACE_EXISTING)
	if durable {
		flags |= windows.MOVEFILE_WRITE_THROUGH
	}
	if err := windows.MoveFileEx(f, t, flags); err != nil {
		return &os.LinkError{Op: "MoveFileEx", Old: from, New: to, Err: err}
	}
	return nil
}

// syncDir is a no-op: Windows cannot open a directory for FlushFileBuffers
// without backup semantics; replaceFile already waits for the move to reach
// the disk.
func syncDir(string) error { return nil }
