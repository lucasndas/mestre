//go:build full && !linux

package localchan_test

import (
	"net"
	"testing"
)

// leaveStaleSocket leaves at path the socket file of an engine that died. Where
// descriptors are not inherited by forked processes, a listener closed without
// unlinking is that.
func leaveStaleSocket(t *testing.T, path string) {
	t.Helper()
	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = stale.Close()
}
