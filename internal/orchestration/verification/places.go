package verification

import (
	"os"
	"path/filepath"
	"strings"
)

// within turns a path an output printed into a path relative to dir, when it
// names a regular file of the worktree: absolute paths under dir lose the prefix
// (the prefix of the ephemeral worktree is not shown), and a path that leaves the
// worktree, is a link or does not exist is no place.
func within(dir, p string) (string, bool) {
	p = filepath.FromSlash(strings.ReplaceAll(p, `\`, "/"))
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return "", false
		}
		p = rel
	}
	p = filepath.Clean(p)
	if p == "." || p == ".." || strings.HasPrefix(p, ".."+string(filepath.Separator)) || filepath.IsAbs(p) {
		return "", false
	}
	st, err := os.Lstat(filepath.Join(dir, p))
	if err != nil || !st.Mode().IsRegular() {
		return "", false
	}
	return filepath.ToSlash(p), true
}
