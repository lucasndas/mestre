package terminal

import (
	"fmt"
	"os"
	"syscall"
)

// checkPrivateDir verifies the owner and sets the mode 0700.
func checkPrivateDir(dir string) error {
	fi, err := os.Lstat(dir) // Lstat: a link to a folder is not the folder
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a folder (a link or a file is in its place)", dir)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("%s belongs to user %d, not to the account of this process (%d)", dir, st.Uid, os.Geteuid())
	}
	if fi.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("set mode 0700 on %s: %w", dir, err)
		}
	}
	return nil
}
