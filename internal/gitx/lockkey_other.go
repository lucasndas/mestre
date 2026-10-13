//go:build !windows

package gitx

import "path/filepath"

// lockKey is the canonical form of a common dir for the lock map. git
// already prints it absolute and resolved; paths are case-sensitive here.
func lockKey(common string) string { return filepath.Clean(common) }
