//go:build linux

package sessionzero_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/lucasndas/mestre/internal/sessionzero"
)

// An agent can leave a fifo or a socket in its MESTRE_TMP and in its folder:
// the end for good takes them away with the rest, and the folder of the
// character on the machine goes too.
func TestEndForGoodRemovesFifosSocketsAndTheFolderOfTheMachine(t *testing.T) {
	t.Parallel()
	r := newCharRig(t, nil)
	q := r.req("dev", "claude-code", sessionzero.RoleCharacter)
	plan := r.prepare(q)
	if err := syscall.Mkfifo(filepath.Join(plan.Tmp, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(plan.AgentDir, "pipe2"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A socket path is short: when the folder of the test is too long for it,
	// the fifos alone make the case.
	if fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0); err == nil {
		defer syscall.Close(fd)
		_ = syscall.Bind(fd, &syscall.SockaddrUnix{Name: filepath.Join(plan.Tmp, "s")})
	}
	if err := r.ch.End(r.ctx, sessionzero.EndRequest{ID: "dev", Dir: r.dir, Provider: plan.Provider, Final: true}); err != nil {
		t.Fatalf("End: %v", err)
	}
	for _, p := range []string{plan.AgentDir, plan.Tmp, plan.MachineDir} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s is still there", p)
		}
	}
}
