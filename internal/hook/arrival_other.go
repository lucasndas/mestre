//go:build !linux

package hook

import (
	"io/fs"
	"time"
)

// arrivalOf is when a file of the queue arrived, as the system saw it: its
// modification time, where the system keeps no change time a process cannot
// set (the caller clamps it to the time of the read, so a time in the future
// does not put a file ahead of the others).
func arrivalOf(info fs.FileInfo) time.Time { return info.ModTime() }
