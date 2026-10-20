package providers

import "golang.org/x/sys/windows"

// shortPath is the 8.3 name of an existing path; false where the volume keeps
// no short names (fsutil 8dot3name) or the path does not exist.
func shortPath(p string) (string, bool) {
	in, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return "", false
	}
	buf := make([]uint16, windows.MAX_PATH)
	n, err := windows.GetShortPathName(in, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		return "", false
	}
	return windows.UTF16ToString(buf[:n]), true
}
