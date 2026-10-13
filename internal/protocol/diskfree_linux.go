//go:build linux

package protocol

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// diskFree is what the disk of dir has free for this user.
func diskFree(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, fmt.Errorf("statfs %s: %w", dir, err)
	}
	return st.Bavail * uint64(st.Bsize), nil
}
