package safefile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// openDir walks dir from the root, one component at a time, with O_PATH and
// O_NOFOLLOW: each step is relative to the descriptor of the folder before
// it, so a component swapped for a link after it was checked is never
// followed. A component that is a link is ErrLink; with create, a missing
// one is made with DirMode.
func openDir(dir string, create bool) (int, error) {
	root, names := components(dir)
	fd, err := unix.Open(root, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, &os.PathError{Op: "open", Path: root, Err: err}
	}
	for _, name := range names {
		next, err := openStep(fd, name, create)
		_ = unix.Close(fd)
		if err != nil {
			return -1, err
		}
		fd = next
	}
	return fd, nil
}

func openStep(parent int, name string, create bool) (int, error) {
	for range 2 {
		fd, err := unix.Openat(parent, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(err, unix.ENOENT) && create {
			if err := unix.Mkdirat(parent, name, uint32(DirMode)); err != nil && !errors.Is(err, unix.EEXIST) {
				return -1, &os.PathError{Op: "mkdir", Path: name, Err: err}
			}
			continue
		}
		if err != nil {
			return -1, &os.PathError{Op: "open", Path: name, Err: err}
		}
		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); err != nil {
			_ = unix.Close(fd)
			return -1, &os.PathError{Op: "stat", Path: name, Err: err}
		}
		switch st.Mode & unix.S_IFMT {
		case unix.S_IFDIR:
			return fd, nil
		case unix.S_IFLNK:
			_ = unix.Close(fd)
			return -1, fmt.Errorf("%w: %s", ErrLink, name)
		default:
			_ = unix.Close(fd)
			return -1, &os.PathError{Op: "open", Path: name, Err: unix.ENOTDIR}
		}
	}
	return -1, &os.PathError{Op: "mkdir", Path: name, Err: unix.ENOENT}
}

func infoOf(st *unix.Stat_t) Info {
	return Info{Exists: true, Size: st.Size, ModTime: time.Unix(st.Mtim.Unix()), Perm: fs.FileMode(st.Mode & 0o777)}
}

// kindErr is the refusal for a file of mode m that is not regular.
func kindErr(base string, m uint32) error {
	if m&unix.S_IFMT == unix.S_IFLNK {
		return fmt.Errorf("%w: %s", ErrLink, base)
	}
	return fmt.Errorf("%w: %s", ErrNotRegular, base)
}

func open(dir, base string) (*os.File, Info, error) {
	dfd, err := openDir(dir, false)
	if err != nil {
		return nil, Info{}, err
	}
	defer unix.Close(dfd)
	// O_NOFOLLOW refuses a link as the file (ELOOP); O_NONBLOCK keeps a FIFO
	// from blocking the open, and the fstat then refuses it.
	fd, err := unix.Openat(dfd, base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOCTTY, 0)
	if errors.Is(err, unix.ELOOP) {
		return nil, Info{}, fmt.Errorf("%w: %s", ErrLink, base)
	}
	if err != nil {
		return nil, Info{}, &os.PathError{Op: "open", Path: base, Err: err}
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)
		return nil, Info{}, &os.PathError{Op: "stat", Path: base, Err: err}
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Close(fd)
		return nil, Info{}, kindErr(base, st.Mode)
	}
	// A regular file: back to blocking reads.
	if err := unix.SetNonblock(fd, false); err != nil {
		_ = unix.Close(fd)
		return nil, Info{}, err
	}
	return os.NewFile(uintptr(fd), filepath.Join(dir, base)), infoOf(&st), nil
}

// statAt is the state of base in the folder dfd, without following a link.
func statAt(dfd int, base string) (Info, error) {
	var st unix.Stat_t
	err := unix.Fstatat(dfd, base, &st, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return Info{}, nil
	}
	if err != nil {
		return Info{}, &os.PathError{Op: "stat", Path: base, Err: err}
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return Info{}, kindErr(base, st.Mode)
	}
	return infoOf(&st), nil
}

func stat(dir, base string) (Info, error) {
	dfd, err := openDir(dir, false)
	if errors.Is(err, fs.ErrNotExist) {
		return Info{}, nil
	}
	if err != nil {
		return Info{}, err
	}
	defer unix.Close(dfd)
	return statAt(dfd, base)
}

func remove(dir, base string, guard func(Info) error) error {
	dfd, err := openDir(dir, false)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer unix.Close(dfd)
	cur, err := statAt(dfd, base)
	if err != nil {
		return err
	}
	if !cur.Exists {
		return nil
	}
	if guard != nil {
		if err := guard(cur); err != nil {
			return err
		}
	}
	if err := unix.Unlinkat(dfd, base, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return &os.PathError{Op: "remove", Path: base, Err: err}
	}
	return nil
}

func write(dir, base string, data []byte, o Options) error {
	dfd, err := openDir(dir, true)
	if err != nil {
		return err
	}
	defer unix.Close(dfd)
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	tmp := "." + base + ".mestre-" + hex.EncodeToString(rnd[:])
	fd, err := unix.Openat(dfd, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return &os.PathError{Op: "create", Path: tmp, Err: err}
	}
	f := os.NewFile(uintptr(fd), tmp)
	done := false
	defer func() {
		if !done {
			_ = f.Close()
			_ = unix.Unlinkat(dfd, tmp, 0)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := unix.Fchmod(fd, uint32(o.Mode)); err != nil {
		return &os.PathError{Op: "chmod", Path: tmp, Err: err}
	}
	// The group is the one of the folder (the mestre-agente group of the
	// workspaces and profiles), as a setgid folder would give. Best effort:
	// with another group the file is still readable by the others.
	var dst unix.Stat_t
	if err := unix.Fstat(dfd, &dst); err == nil {
		var fst unix.Stat_t
		if unix.Fstat(fd, &fst) == nil && fst.Gid != dst.Gid {
			_ = unix.Fchown(fd, -1, int(dst.Gid))
		}
	}
	if o.Sync {
		if err := f.Sync(); err != nil {
			return err
		}
	}
	if err := f.Close(); err != nil {
		_ = unix.Unlinkat(dfd, tmp, 0)
		done = true
		return err
	}
	done = true
	if o.Guard != nil {
		cur, err := statAt(dfd, base)
		if err == nil {
			err = o.Guard(cur)
		}
		if err != nil {
			_ = unix.Unlinkat(dfd, tmp, 0)
			return err
		}
	}
	// renameat in the held folder: a link in place of the file is replaced
	// itself, never followed.
	if err := unix.Renameat(dfd, tmp, dfd, base); err != nil {
		_ = unix.Unlinkat(dfd, tmp, 0)
		return &os.LinkError{Op: "rename", Old: tmp, New: base, Err: err}
	}
	return nil
}

// mkdirAll makes dir and its missing parents, refusing a link on the way.
func mkdirAll(dir string) error {
	fd, err := openDir(dir, true)
	if err != nil {
		return err
	}
	return unix.Close(fd)
}
