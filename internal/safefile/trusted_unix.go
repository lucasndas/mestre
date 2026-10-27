//go:build !windows

package safefile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ErrUntrusted is a file under .mestre/ (or a folder on the way to it) that the engine
// did not make: it is not owned by the account of the engine or by root, or the group or
// the others can write it. The agents run in a group that writes the main clone, and a
// file they made in place of the real one is not the policy of the Guild.
var ErrUntrusted = errors.New("not made by the engine, or writable by the group or the others")

func trusted(st *syscall.Stat_t) bool {
	return (int(st.Uid) == os.Geteuid() || st.Uid == 0) && st.Mode&0o022 == 0
}

// ReadTrusted is Read for a file the agents may read and must not write
// (docs/arquitetura/persistencia.md). Besides what Read refuses, it fails closed with
// ErrUntrusted when the file, or a folder from the last `.mestre` of the path down to
// it, is not owned by the account of the engine (or root) or has write for the group or
// the others. The file is judged on the open file itself; the folders by lstat, and a
// folder swapped meanwhile only leads to a file that fails the check of its owner.
func ReadTrusted(path string, limit int64) ([]byte, Info, error) {
	f, info, err := Open(path)
	if err != nil {
		return nil, Info{}, err
	}
	defer f.Close()
	if err := checkTrusted(path, f); err != nil {
		return nil, Info{}, wrap(path, err)
	}
	return readOpen(path, f, info, limit)
}

func checkTrusted(path string, f *os.File) error {
	fst, err := f.Stat()
	if err != nil {
		return err
	}
	if st, ok := fst.Sys().(*syscall.Stat_t); !ok || !trusted(st) {
		return fmt.Errorf("%w: the file", ErrUntrusted)
	}
	parts := strings.Split(filepath.Clean(path), string(filepath.Separator))
	from := -1
	for i, p := range parts {
		if p == ".mestre" {
			from = i
		}
	}
	if from < 0 {
		return nil
	}
	for i := from; i < len(parts)-1; i++ {
		dir := string(filepath.Separator) + filepath.Join(parts[:i+1]...)
		dst, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if st, ok := dst.Sys().(*syscall.Stat_t); !ok || !trusted(st) {
			return fmt.Errorf("%w: the folder %s", ErrUntrusted, filepath.Base(dir))
		}
	}
	return nil
}
