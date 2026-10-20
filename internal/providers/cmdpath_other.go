//go:build !windows

package providers

// shortPath: only Windows has 8.3 names.
func shortPath(string) (string, bool) { return "", false }
