package terminal

// checkPrivateDir does nothing on Windows: a folder has no mode, and who may
// enter it is the ACL that internal/winsvc sets.
func checkPrivateDir(string) error { return nil }
