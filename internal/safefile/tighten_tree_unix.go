//go:build !windows

package safefile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
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

// tightenEntry chmods a regular file or folder owned by this account; a link and what
// belongs to someone else are left alone.
func tightenEntry(p string) error {
	st, err := os.Lstat(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || int(sys.Uid) != os.Geteuid() || (!st.Mode().IsRegular() && !st.IsDir()) || st.Mode().Perm()&0o022 == 0 {
		return nil
	}
	return os.Chmod(p, st.Mode().Perm()&^0o022)
}
