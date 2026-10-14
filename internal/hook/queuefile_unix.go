//go:build !windows

package hook

import (
	"errors"
	"os"
	"syscall"
)

// openQueued opens a file of the queue for the engine to read, without
// following a link an agent left in place of a file (O_NOFOLLOW), without
// waiting on a FIFO swapped in after the listing (O_NONBLOCK), and refusing
// a file with more than one link: a hard link of a file the agent could not
// read itself.
func openQueued(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if s, ok := st.Sys().(*syscall.Stat_t); ok && s.Nlink > 1 {
		_ = f.Close()
		return nil, errors.New("a file with more than one link")
	}
	return f, nil
}
