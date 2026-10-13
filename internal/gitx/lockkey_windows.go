package gitx

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// lockKey is the canonical form of a common dir for the lock map: the long
// name of every component (C:\Users\RUNNER~1 and C:\Users\runneradmin are
// the same folder) in lower case (NTFS folders are case-insensitive), so
// two spellings of one repository share its lock.
func lockKey(common string) string {
	p := filepath.Clean(common)
	if u, err := windows.UTF16FromString(p); err == nil {
		buf := make([]uint16, windows.MAX_LONG_PATH)
		if n, err := windows.GetLongPathName(&u[0], &buf[0], uint32(len(buf))); err == nil && n > 0 && int(n) < len(buf) {
			p = windows.UTF16ToString(buf[:n])
		}
	}
	return strings.ToLower(p)
}
