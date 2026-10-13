package terminal

import (
	"fmt"
	"os"
)

// EnsurePrivateDir makes dir (and its parents) if it does not exist and makes
// sure it is private to the account of this process. os.MkdirAll only sets the
// mode of a folder it creates: one that was already there, from an earlier
// version, a restored backup or somebody's hand, keeps whatever mode and owner
// it had. On Linux the folder must then be a real folder (not a link), owned by
// this account, and gets mode 0700; the socket folder of the terminals and the
// log folder hold the secrets' neighbours and must not be reachable by the
// agents' account. On Windows there is no folder mode: the ACL is
// internal/winsvc's.
func EnsurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	return checkPrivateDir(dir)
}
