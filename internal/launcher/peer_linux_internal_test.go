package launcher

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// pidfdPID reads the PID a pidfd points to, as the check of the peer does
// after it read the cgroup.
func TestPidfdPID(t *testing.T) {
	t.Parallel()
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		t.Skipf("no pidfd_open on this kernel: %v", err)
	}
	defer unix.Close(fd)
	if got, err := pidfdPID(fd); err != nil || got != os.Getpid() {
		t.Fatalf("pidfdPID = %d, %v; want %d", got, err, os.Getpid())
	}
	if _, err := pidfdPID(1 << 20); err == nil {
		t.Error("a descriptor that is not open was read")
	}
}
