//go:build !windows

package vault

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

// maxKeyFile bounds what Load reads; a keyring is well under 1 KiB.
const maxKeyFile = 64 << 10

// FileKeyStore keeps the data key in a file (default
// /etc/mestre/<inst>/cofre.key) with mode 0600, owned by the account that
// runs the engine. Load refuses the file when group or others have any
// access, when another account owns it or when it is not a regular file
// (checklist LIN-05).
type FileKeyStore struct {
	path string
	uid  int
}

// NewFileKeyStore returns the key store at path, owned by the current
// effective user (the service account mestre in production).
func NewFileKeyStore(path string) *FileKeyStore {
	return &FileKeyStore{path: path, uid: os.Geteuid()}
}

// Path is the key file.
func (s *FileKeyStore) Path() string { return s.path }

// Load reads the key file after checking type, owner and mode on the open
// descriptor (no symlinks followed, no check-then-open race).
func (s *FileKeyStore) Load(context.Context) ([]byte, error) {
	f, err := os.OpenFile(s.path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoKey
		}
		if errors.Is(err, syscall.ELOOP) {
			return nil, s.unsafe("it is a symbolic link")
		}
		return nil, fmt.Errorf("vault: open key file: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("vault: stat key file: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, s.unsafe("it is not a regular file")
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return nil, s.unsafe(fmt.Sprintf("mode is %04o, must be 0600 (no access for group or others)", perm))
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != s.uid {
		return nil, s.unsafe(fmt.Sprintf("owned by uid %d, must be owned by the engine account (uid %d)", st.Uid, s.uid))
	}
	data, err := io.ReadAll(io.LimitReader(f, maxKeyFile))
	if err != nil {
		return nil, fmt.Errorf("vault: read key file: %w", err)
	}
	return data, nil
}

func (s *FileKeyStore) unsafe(reason string) error {
	return &UnsafeKeyFileError{Path: s.path, Reason: reason, Check: "LIN-05"}
}

// Save replaces the key file atomically with mode 0600.
func (s *FileKeyStore) Save(_ context.Context, data []byte) error {
	if err := writeFileAtomic(s.path, data); err != nil {
		return fmt.Errorf("vault: write key file: %w", err)
	}
	return nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
