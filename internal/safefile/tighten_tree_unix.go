//go:build !windows

package safefile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// TightenRules takes the write of the group and of the others off .mestre/, its regras/
// folder and every folder and file under regras/, which the engine owns: an earlier
// version, a checkout with umask 002 or the advance of the main clone leaves them 0664 or
// 0775. Links are not followed, and what the engine does not own is left alone (ReadTrusted
// refuses it). A missing .mestre/ is no error. The first error is returned after the walk.
func TightenRules(mestre string) error {
	var first error
	note := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	note(tightenEntry(mestre))
	root := filepath.Join(mestre, "regras")
	note(filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				note(err)
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		note(tightenEntry(p))
		return nil
	}))
	return first
}

// tightenEntry takes the write bits of the group and the others off a regular file or folder
// owned by this account. It works on the open descriptor (O_NOFOLLOW, then fstat and
// fchmod), so a link or an entry swapped after the walk listed it is never followed and the
// mode is taken from what was really opened: bits are only ever removed, never given.
func tightenEntry(p string) error {
	fd, err := unix.Open(p, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOCTTY, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ELOOP) {
			return nil // gone, or a link: not followed
		}
		return err
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	kind := st.Mode & unix.S_IFMT
	if int(st.Uid) != os.Geteuid() || (kind != unix.S_IFREG && kind != unix.S_IFDIR) || st.Mode&0o022 == 0 {
		return nil
	}
	return unix.Fchmod(fd, st.Mode&0o7777&^0o022)
}
