//go:build linux

package launcher_test

import (
	"errors"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
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
	// The writer reads the descriptor of pw: it ends before pw is closed.
	written := make(chan struct{})
	go func() {
		defer close(written)
		_ = launcher.NewConn(a).WriteFrame([]byte(`{"op":"x"}`), []*os.File{pw})
	}()
	frame, files, err := launcher.NewConn(b).ReadFrame()
	<-written
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
	// The writer reads the descriptors of fs: it ends before the deferred
	// Close calls run.
	written := make(chan struct{})
	go func() {
		defer close(written)
		_ = launcher.NewConn(a).WriteFrame([]byte(`{}`), fs)
	}()
	if _, _, err := launcher.NewConn(b).ReadFrame(); err == nil {
		t.Error("five descriptors accepted")
	}
	<-written
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

// The inheritable set goes too (the real thing, with systemd giving the two
// capabilities, is checked by the real-account tests, CA-TERM-07).
func TestDropInheritable(t *testing.T) {
	t.Parallel()
	if err := launcher.DropInheritable(); errors.Is(err, syscall.ENOTSUP) {
		t.Skip("this test binary uses cgo, where the Go runtime cannot change a capability for all threads; the release is built with CGO_ENABLED=0")
	} else if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "CapInh:") && strings.TrimSpace(strings.TrimPrefix(l, "CapInh:")) != "0000000000000000" {
			t.Errorf("inheritable capabilities left: %s", l)
		}
	}
}

// CA-TERM-15
// The mestre-pty never runs as the account of the launcher, which owns the
// vault and the database (the unit has User=mestre): a launcher with no account
// of the agents, or with its own, refuses to start instead of starting the
// terminals as itself, unless it is told to, which only the full suite does (it
// starts a launcher as the user who runs it).
func TestLookupAgent(t *testing.T) {
	t.Parallel()
	self, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", self.Username} {
		if c, err := launcher.LookupAgent(name, false); c != nil || err == nil {
			t.Errorf("account %q of the launcher itself, in production = %v, %v; want it refused", name, c, err)
		}
		if os.Geteuid() == 0 {
			continue // AllowSelf as root would start the terminals as root; the other cases do not run as root
		}
		if c, err := launcher.LookupAgent(name, true); c != nil || err != nil {
			t.Errorf("account %q with AllowSelf = %v, %v; want to start as this user", name, c, err)
		}
	}
	if _, err := launcher.LookupAgent("no-such-account-mestre", false); err == nil {
		t.Error("an account that does not exist was found")
	}
	if _, err := launcher.LookupAgent("root", false); err == nil && os.Geteuid() != 0 {
		t.Error("root accepted as the agent account")
	}
}

// CA-TERM-15
// Prepare, the way the service builds the launcher, refuses before it opens its
// socket: the configuration has no account of the agents.
func TestPrepareRefusesALauncherWithoutTheAccountOfTheAgents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := launcher.RunConfig{SocketPath: filepath.Join(dir, "l.sock"), BinDir: dir, SocketDir: dir}
	srv, ln, err := launcher.Prepare(cfg)
	if err == nil {
		_ = ln.Close()
		t.Fatalf("Prepare with no account of the agents = %v", srv)
	}
	if _, err := os.Stat(cfg.SocketPath); err == nil {
		t.Error("the launcher opened its socket before it refused")
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

// fakeAccounts is the account database of a test, and a launcher that runs as
// uid/gid.
func fakeAccounts(uid, gid int, users map[string][2]int) launcher.Accounts {
	return launcher.Accounts{
		UID: uid, GID: gid,
		Lookup: func(name string) (*user.User, error) {
			ids, ok := users[name]
			if !ok {
				return nil, user.UnknownUserError(name)
			}
			return &user.User{Username: name, Uid: strconv.Itoa(ids[0]), Gid: strconv.Itoa(ids[1])}, nil
		},
	}
}

// CA-TERM-15
// A launcher that runs as root (or as any account that is not the owner of
// the vault) still never starts the mestre-pty as the owner of the vault, as
// root, or with the group root: the account of the agents is none of them.
func TestTheAgentAccountIsNeitherTheOwnerOfTheVaultNorRootNorTheGroupRoot(t *testing.T) {
	t.Parallel()
	users := map[string][2]int{
		"mestre": {990, 990}, "mestre-agente": {991, 991}, "root": {0, 0},
		"rootgroup": {995, 0}, "sameasvault": {990, 991}, "enginegroup": {996, 990},
	}
	for _, runAs := range []int{0, 990} {
		a := fakeAccounts(runAs, runAs, users)
		cred, err := a.Agent("mestre-agente", false)
		if err != nil || cred == nil || cred.Uid != 991 || cred.Gid != 991 {
			t.Errorf("running as %d: agent = %v, %v; want 991:991", runAs, cred, err)
		}
		for _, name := range []string{"mestre", "root", "rootgroup", "sameasvault", "enginegroup", "no-such-account"} {
			if cred, err := a.Agent(name, false); err == nil || cred != nil {
				t.Errorf("running as %d: account %q accepted: %v", runAs, name, cred)
			}
		}
	}
	// where there is no account of the owner of the vault (a developer machine
	// with the fast suite), the other checks stand.
	bare := fakeAccounts(1000, 1000, map[string][2]int{"agent": {1001, 1001}})
	if cred, err := bare.Agent("agent", false); err != nil || cred == nil {
		t.Errorf("without an owner of the vault: %v, %v", cred, err)
	}
	// AllowSelf does not let root or the group root through.
	self := fakeAccounts(1000, 1000, map[string][2]int{"rootgroup": {995, 0}, "me": {1000, 1000}})
	if cred, err := self.Agent("rootgroup", true); err == nil || cred != nil {
		t.Errorf("group root accepted with AllowSelf: %v", cred)
	}
	if cred, err := self.Agent("me", true); err != nil || cred != nil {
		t.Errorf("AllowSelf, the user itself = %v, %v; want to start as this user", cred, err)
	}
}

// CA-TERM-15
// The account of the agents of a launcher is the one of its own instance: a
// launcher of one instance never starts a mestre-pty as the agents of another.
func TestPrepareBindsTheAgentAccountToTheInstance(t *testing.T) {
	t.Parallel()
	users := map[string][2]int{"mestre-agente": {991, 991}, "mestre-agente-x": {992, 992}}
	for _, c := range []struct {
		instance, agent string
		ok              bool
	}{
		{"", "mestre-agente", true}, {"principal", "mestre-agente", true}, {"x", "mestre-agente-x", true},
		{"x", "mestre-agente", false}, {"principal", "mestre-agente-x", false}, {"y", "mestre-agente-x", false},
	} {
		dir := t.TempDir()
		cfg := launcher.RunConfig{
			SocketPath: filepath.Join(dir, "l.sock"), BinDir: dir, SocketDir: dir,
			Instance: c.instance, Agent: c.agent, Accounts: fakeAccounts(0, 0, users),
		}
		_, ln, err := launcher.Prepare(cfg)
		if ln != nil {
			_ = ln.Close()
		}
		if (err == nil) != c.ok {
			t.Errorf("instance %q, agent %q: err = %v, want ok = %v", c.instance, c.agent, err, c.ok)
		}
	}
}

// CA-TERM-15
// A backend with no credential starts nothing unless it is told it may (the
// full suite): the mestre-pty would be the account of the launcher.
func TestLinuxBackendWithoutACredentialRefusesUnlessAllowed(t *testing.T) {
	t.Parallel()
	be := &launcher.LinuxBackend{}
	if _, err := be.Start(t.Context(), launcher.StartRequest{ID: "t1", Binary: "/nonexistent/mestre-pty"}); err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("Start without a credential = %v; want a refusal", err)
	}
}
