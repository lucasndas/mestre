//go:build !linux

package delegated

import "testing"

// InScope is always false: there are no delegated cgroups outside Linux.
func InScope() bool { return false }

// Rerun skips: Windows groups the tree of a terminal in a Job Object, and
// there is no cgroup to delegate.
func Rerun(t *testing.T, _ ...string) {
	t.Helper()
	t.Skip("no cgroups on this system: the Job Object holds the tree")
}
