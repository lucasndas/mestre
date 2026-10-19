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

// The folder of the character on the machine is setgid: the engine (group
// mestre) makes it inside agentes/, and without the bit the folder, and the
// policy.json and env.json inside it, would be of the group mestre, which
// the agent is not in, instead of the group of the agents of the instance.
// CA-SZ-PERS-14
func TestTheFolderOfTheCharacterOnTheMachineIsSetgid(t *testing.T) {
	t.Parallel()
	r := newCharRig(t, nil)
	plan := r.prepare(r.req("dev", "claude-code", sessionzero.RoleCharacter))
	for dir, want := range map[string]os.FileMode{
		plan.MachineDir: os.ModeSetgid | 0o750,
		plan.Tmp:        os.ModeSetgid | 0o770,
	} {
		fi, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode() & (os.ModePerm | os.ModeSetgid | os.ModeSticky); got != want {
			t.Errorf("%s: mode %v, want %v", dir, got, want)
		}
	}
}
