package sessionzero

import "path/filepath"

// realPath is the path with its links resolved, or the path itself when it does
// not resolve (the folder is not there yet).
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
