package privdir

// check does nothing on Windows: a folder has no mode, and who may
// enter it is the ACL that internal/winsvc sets.
func check(string) error { return nil }
