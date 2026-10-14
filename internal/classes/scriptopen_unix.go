//go:build !windows

package classes

import (
	"os"
	"syscall"
)

// openScript opens a file of the worktree for reading without following a
// link and without waiting on a FIFO.
func openScript(p string) (*os.File, error) {
	return os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
