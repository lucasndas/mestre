//go:build windows

package safefile

// TightenRules does nothing on Windows: the ACL of the folder decides.
func TightenRules(string) error { return nil }
