//go:build !windows

package vault

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// maxKeyFile bounds what Load reads; a keyring is well under 1 KiB.
const maxKeyFile = 64 << 10

// FileKeyStore keeps the data key in a file (default
// /etc/mestre/<inst>/cofre.key) with mode 0600, owned by the account that
// runs the engine, in a folder with mode 0700 owned by that account too.
// Load refuses the file when group or others have any access to it or to
// its folder, when another account owns either, when the folder or the file
// is a symbolic link or the file is not a regular file (checklist LIN-05).
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

// Load checks the folder (opened without following a symbolic link) and then
// the file, opened relative to that folder, on the open descriptors: type,
// owner and mode, with no check-then-open race.
func (s *FileKeyStore) Load(context.Context) ([]byte, error) {
	dir, err := s.openDir()
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	fd, err := unix.Openat(int(dir.Fd()), filepath.Base(s.path), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil, ErrNoKey
		}
		if errors.Is(err, unix.ELOOP) {
			return nil, s.unsafe("it is a symbolic link")
		}
		return nil, fmt.Errorf("vault: open key file: %w", err)
	}
	f := os.NewFile(uintptr(fd), s.path)
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

// openDir opens the folder of the key file, refusing a symbolic link, a
// folder another account owns or one that group or others can reach.
func (s *FileKeyStore) openDir() (*os.File, error) {
	path := filepath.Dir(s.path)
	d, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			return nil, ErrNoKey
		case errors.Is(err, syscall.ELOOP):
			return nil, s.unsafe("its folder " + path + " is a symbolic link")
		}
		return nil, fmt.Errorf("vault: open key folder: %w", err)
	}
	fi, err := d.Stat()
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("vault: stat key folder: %w", err)
	}
	if !fi.IsDir() {
		d.Close()
		return nil, s.unsafe("its folder " + path + " is not a directory")
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		d.Close()
		return nil, s.unsafe(fmt.Sprintf("its folder %s has mode %04o, must be 0700 (no access for group or others)", path, perm))
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != s.uid {
		d.Close()
		return nil, s.unsafe(fmt.Sprintf("its folder %s is owned by uid %d, must be owned by the engine account (uid %d)", path, st.Uid, s.uid))
	}
	return d, nil
}

func (s *FileKeyStore) unsafe(reason string) error {
	return &UnsafeKeyFileError{Path: s.path, Reason: reason, Check: "LIN-05"}
}

// replaceFile moves from over to; syncDir then makes the rename durable.
func replaceFile(from, to string, _ bool) error { return os.Rename(from, to) }

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
