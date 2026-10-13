package terminal

import "fmt"

// DelegatedCgroup has no cgroups to offer on Windows: the mestre-pty groups
// the tree of each terminal in a Job Object it holds.
func DelegatedCgroup() (string, error) {
	return "", fmt.Errorf("%w: Windows uses a Job Object per terminal", ErrNoCgroup)
}
