package safefile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func trustedStat(st *unix.Stat_t) bool {
	return (int(st.Uid) == os.Geteuid() || st.Uid == 0) && st.Mode&0o022 == 0
}

// ReadTrusted is Read for a file the agents may read and must not write
// (docs/arquitetura/persistencia.md). Besides what Read refuses, it fails closed with
// ErrUntrusted when the file, or a folder from the last `.mestre` of the path down to it, is
// not owned by the account of the engine (or root) or has write for the group or the others.
// The check is made on the descriptors of the very walk that opens the file, step by step
// (openat with O_NOFOLLOW), so a folder swapped meanwhile is neither followed nor trusted.
func ReadTrusted(path string, limit int64) ([]byte, Info, error) {
	f, info, err := openTrusted(path)
	if err != nil {
		return nil, Info{}, wrap(path, err)
	}
	defer f.Close()
	return readOpen(path, f, info, limit)
}

func openTrusted(path string) (*os.File, Info, error) {
	dir, base, err := split(path)
	if err != nil {
		return nil, Info{}, err
	}
	root, names := components(dir)
	from := len(names) // the steps from the last .mestre down are checked
	for i, n := range names {
		if n == ".mestre" {
			from = i
		}
	}
	fd, err := unix.Open(root, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, Info{}, &os.PathError{Op: "open", Path: root, Err: err}
	}
	for i, name := range names {
		next, err := openStep(fd, name, false)
		_ = unix.Close(fd)
		if err != nil {
			return nil, Info{}, err
		}
		fd = next
		if i >= from {
			var st unix.Stat_t
			if err := unix.Fstat(fd, &st); err != nil {
				_ = unix.Close(fd)
				return nil, Info{}, &os.PathError{Op: "stat", Path: name, Err: err}
			}
			if !trustedStat(&st) {
				_ = unix.Close(fd)
				return nil, Info{}, fmt.Errorf("%w: the folder %s", ErrUntrusted, name)
			}
		}
	}
	defer unix.Close(fd)
	ffd, err := unix.Openat(fd, base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOCTTY, 0)
	if errors.Is(err, unix.ELOOP) {
		return nil, Info{}, fmt.Errorf("%w: %s", ErrLink, base)
	}
	if err != nil {
		return nil, Info{}, &os.PathError{Op: "open", Path: base, Err: err}
	}
	var st unix.Stat_t
	if err := unix.Fstat(ffd, &st); err != nil {
		_ = unix.Close(ffd)
		return nil, Info{}, &os.PathError{Op: "stat", Path: base, Err: err}
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Close(ffd)
		return nil, Info{}, kindErr(base, st.Mode)
	}
	if !trustedStat(&st) {
		_ = unix.Close(ffd)
		return nil, Info{}, fmt.Errorf("%w: the file", ErrUntrusted)
	}
	if err := unix.SetNonblock(ffd, false); err != nil {
		_ = unix.Close(ffd)
		return nil, Info{}, err
	}
	return os.NewFile(uintptr(ffd), filepath.Join(dir, base)), infoOf(&st), nil
}
