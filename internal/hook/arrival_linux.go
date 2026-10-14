//go:build linux

package hook

import (
	"io/fs"
	"syscall"
	"time"
)

// arrivalOf is when a file of the queue arrived, as the system saw it: the
// change time (ctime), which a process cannot set, unlike the modification
// time (utimes) and the timestamp in the name the dispatcher wrote. A file
// can only look newer than it is (any later write or rename moves its ctime
// forward), never older and never from the future.
func arrivalOf(info fs.FileInfo) time.Time {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return time.Unix(int64(st.Ctim.Sec), int64(st.Ctim.Nsec))
	}
	return info.ModTime()
}
