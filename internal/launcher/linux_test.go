//go:build linux

package launcher_test

import (
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/lucasndas/mestre/internal/launcher"
	"github.com/lucasndas/mestre/internal/terminal"
)

// socketPair is two connected Unix sockets, without touching the disk.
func socketPair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	conn := func(fd int) *net.UnixConn {
		f := os.NewFile(uintptr(fd), "pair")
		defer f.Close()
		c, err := net.FileConn(f)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c.(*net.UnixConn)
	}
	return conn(fds[0]), conn(fds[1])
}

// Descriptors travel with the frame (SCM_RIGHTS) and arrive usable.
func TestFramesCarryDescriptors(t *testing.T) {
	t.Parallel()
	a, b := socketPair(t)
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	go func() { _ = launcher.NewConn(a).WriteFrame([]byte(`{"op":"x"}`), []*os.File{pw}) }()
	frame, files, err := launcher.NewConn(b).ReadFrame()
	if err != nil || string(frame) != `{"op":"x"}` || len(files) != 1 {
		t.Fatalf("ReadFrame = %q, %v, %v", frame, files, err)
	}
	defer files[0].Close()
	if _, err := files[0].Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	_ = pw.Close()
	_ = files[0].Close()
	got, _ := io.ReadAll(pr)
	if string(got) != "hi" {
		t.Errorf("read %q through the received descriptor", got)
	}
}

func TestFramesRefuseTooManyDescriptors(t *testing.T) {
	t.Parallel()
	a, b := socketPair(t)
	var fs []*os.File
	for range 5 {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		defer w.Close()
		fs = append(fs, r)
	}
	go func() { _ = launcher.NewConn(a).WriteFrame([]byte(`{}`), fs) }()
	if _, _, err := launcher.NewConn(b).ReadFrame(); err == nil {
		t.Error("five descriptors accepted")
	}
}

func TestPeerPolicy(t *testing.T) {
	t.Parallel()
	cg, err := terminal.ProcCgroup(os.Getpid())
	if err != nil {
		t.Skipf("no cgroup v2 to read: %v", err)
	}
	uid := uint32(os.Geteuid())
	cases := []struct {
		name    string
		policy  launcher.PeerPolicy
		allowed bool
		mention string
	}{
		{"the engine", launcher.PeerPolicy{UID: uid, Cgroup: cg}, true, ""},
		{"another account", launcher.PeerPolicy{UID: uid + 1, Cgroup: cg}, false, "uid"},
		{"another cgroup", launcher.PeerPolicy{UID: uid, Cgroup: "/system.slice/mestre@other.service"}, false, "cgroup"},
		{"a cgroup that only shares the prefix", launcher.PeerPolicy{UID: uid, Cgroup: cg[:len(cg)-1]}, false, "cgroup"},
		{"no cgroup configured", launcher.PeerPolicy{UID: uid}, false, "cgroup"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, _ := socketPair(t)
			who, err := tc.policy.Check(a)
			if tc.allowed != (err == nil) {
				t.Fatalf("Check = %q, %v; allowed want %v", who, err, tc.allowed)
			}
			if err != nil && !strings.Contains(err.Error(), tc.mention) {
				t.Errorf("error %q does not mention %q", err, tc.mention)
			}
			if !strings.Contains(who, "uid=") {
				t.Errorf("who = %q, want the pid and uid for the log", who)
			}
		})
	}
	t.Run("not a Unix socket", func(t *testing.T) {
		t.Parallel()
		c1, _ := net.Pipe()
		if _, err := (launcher.PeerPolicy{UID: uid, Cgroup: cg}).Check(c1); err == nil {
			t.Error("a pipe passed")
		}
	})
}

// A process in a cgroup below the one configured is the engine too (the
// parent of the test's cgroup is configured).
func TestPeerPolicyAcceptsACgroupBelow(t *testing.T) {
	t.Parallel()
	cg, err := terminal.ProcCgroup(os.Getpid())
	if err != nil {
		t.Skipf("no cgroup v2 to read: %v", err)
	}
	i := strings.LastIndex(cg, "/")
	if i <= 0 {
		t.Skipf("the cgroup %q has no parent below the root", cg)
	}
	a, _ := socketPair(t)
	if _, err := (launcher.PeerPolicy{UID: uint32(os.Geteuid()), Cgroup: cg[:i]}).Check(a); err != nil {
		t.Errorf("a process below the cgroup of the engine was refused: %v", err)
	}
}

func TestDropAmbient(t *testing.T) {
	t.Parallel()
	if err := launcher.DropAmbient(); errors.Is(err, syscall.ENOTSUP) {
		t.Skip("this test binary uses cgo, where the Go runtime cannot change a capability for all threads; the release is built with CGO_ENABLED=0")
	} else if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "CapAmb:") && strings.TrimSpace(strings.TrimPrefix(l, "CapAmb:")) != "0000000000000000" {
			t.Errorf("ambient capabilities left: %s", l)
		}
	}
}

func TestLookupAgent(t *testing.T) {
	t.Parallel()
	if c, err := launcher.LookupAgent(""); c != nil || err != nil {
		t.Errorf("no name = %v, %v; want to start as this user", c, err)
	}
	if _, err := launcher.LookupAgent("no-such-account-mestre"); err == nil {
		t.Error("an account that does not exist was found")
	}
	if _, err := launcher.LookupAgent("root"); err == nil && os.Geteuid() != 0 {
		t.Error("root accepted as the agent account")
	}
}

// CA-TESTES-05 (launcher): in the fast suite the Linux backend starts no
// process, even before it looks at the request.
func TestLinuxBackendRefusesInTheFastSuite(t *testing.T) {
	t.Parallel()
	be := &launcher.LinuxBackend{Suite: "fast"}
	started, err := be.Start(t.Context(), launcher.StartRequest{ID: "t1", Binary: "/nonexistent/mestre-pty"})
	if !errors.Is(err, terminal.ErrExternalProcessInFastSuite) || started.PID != 0 {
		t.Fatalf("Start = %+v, %v; want ErrExternalProcessInFastSuite", started, err)
	}
}
