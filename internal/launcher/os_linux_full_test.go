//go:build full && linux

package launcher_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/lucasndas/mestre/internal/launcher"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/internal/testkit/delegated"
)

// osEnv is the Linux side of the fixture: the launcher listens on a Unix
// socket, accepts the uid and the cgroup of the test process (which plays the
// engine), and starts the mestre-pty as the same user, since the test has no
// CAP_SETUID. The cgroup of the terminal exists only where the test process
// has a delegated cgroup v2 (systemd-run --user --scope -p Delegate=yes).
type osEnv struct {
	cfg    launcher.RunConfig
	cgBase string
	stop   context.CancelFunc
	served chan struct{}
}

// helperResolveCgroup is this test binary as the launcher of a service: it
// resolves the cgroup base, which moves the process, and says where it is.
const helperResolveCgroup = "helper-resolve-cgroup"

// needStart: a mestre-pty can always be started on Linux.
func (e *env) needStart(*testing.T) {}

// osCases registers what only this system has to check: nothing on Linux.
func osCases(*testing.T) {}

func osHelper(args []string) (bool, int) {
	if args[0] != helperResolveCgroup {
		return false, 0
	}
	base, err := launcher.ResolveCgroupBase()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return true, 1
	}
	own, err := terminal.ProcCgroup(os.Getpid())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return true, 1
	}
	fmt.Printf("%s\n%s\n", base, own)
	return true, 0
}

// The launcher of a service with Delegate=yes gets a leaf of its own and
// creates the cgroups of the terminals beside it.
func TestResolveCgroupBase(t *testing.T) {
	t.Parallel()
	base, err := terminal.DelegatedCgroup()
	if err != nil {
		t.Skipf("this process has no delegated cgroup v2 (run it with systemd-run --user --scope -p Delegate=yes): %v", err)
	}
	var out, errb bytes.Buffer
	cmd := exec.Command(os.Args[0], helperResolveCgroup)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("%v: %s", err, errb.String())
	}
	lines := strings.Fields(out.String())
	if len(lines) != 2 {
		t.Fatalf("output = %q", out.String())
	}
	if lines[0] != base {
		t.Errorf("base = %q, want the delegated cgroup %q", lines[0], base)
	}
	if want := strings.TrimPrefix(base, "/sys/fs/cgroup") + "/launcher"; lines[1] != want {
		t.Errorf("the launcher is in %q, want its leaf %q", lines[1], want)
	}
}

// TestDropAmbient and TestDropInheritable (fast suite) skip in a binary that uses cgo, where the Go
// runtime cannot change a capability for all threads, and the test binary of
// this package does: it needs os/user and net, which link the C resolver. The
// release is built with CGO_ENABLED=0, so the test runs once more here in a
// binary built that way, and must pass, not skip.
func TestDropAmbientRunsWithoutCgo(t *testing.T) {
	t.Parallel()
	cmd := exec.CommandContext(t.Context(), "go", "test", "-count=1", "-v", "-run", "^(TestDropAmbient|TestDropInheritable)$", ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test with CGO_ENABLED=0: %v\n%s", err, out)
	}
	for _, name := range []string{"TestDropAmbient", "TestDropInheritable"} {
		if !strings.Contains(string(out), "--- PASS: "+name+" ") {
			t.Fatalf("%s did not pass without cgo:\n%s", name, out)
		}
	}
	if strings.Contains(string(out), "--- SKIP") {
		t.Fatalf("a drop of capabilities was skipped without cgo:\n%s", out)
	}
}

func newEnv(t *testing.T) *env {
	t.Helper()
	cg, err := terminal.ProcCgroup(os.Getpid())
	if err != nil {
		t.Skipf("the cgroup of the test process cannot be read: %v", err)
	}
	root := shortDir(t)
	e := &env{root: root, sockets: filepath.Join(root, "p"), audit: &auditLog{}, sys: terminal.NewSystem()}
	base, _ := terminal.DelegatedCgroup() // "" where there is none
	uid := uint32(os.Geteuid())
	e.os.cgBase = base
	e.os.cfg = launcher.RunConfig{
		SocketPath: filepath.Join(root, "launcher.sock"), BinDir: builds.bin, SocketDir: e.sockets,
		Engine:     launcher.PeerPolicy{UID: uid, Cgroup: cg},
		CgroupBase: base, Env: os.Environ(), Audit: e.audit.record,
	}
	e.started = e.listen(t, e.os.cfg)
	e.client = launcher.NewClient(e.os.cfg.SocketPath)
	e.dial = dialUnix(e.os.cfg.SocketPath)

	for _, s := range []struct {
		name   string
		policy launcher.PeerPolicy
	}{
		{"another account", launcher.PeerPolicy{UID: uid + 1, Cgroup: cg}},
		{"another cgroup", launcher.PeerPolicy{UID: uid, Cgroup: "/system.slice/mestre@another.service"}},
	} {
		cfg := e.os.cfg
		cfg.SocketPath = filepath.Join(root, strings.ReplaceAll(s.name, " ", "-")+".sock")
		cfg.Engine = s.policy
		st := stranger{name: s.name, audit: &auditLog{}}
		client := launcher.NewClient(cfg.SocketPath)
		st.try = func(ctx context.Context, spec terminal.SpawnSpec) error {
			_, err := client.Spawn(ctx, spec)
			return err
		}
		cfg.Audit = st.audit.record
		st.started = e.listen(t, cfg)
		e.strangers = append(e.strangers, st)
	}
	return e
}

func dialUnix(path string) func(context.Context) (net.Conn, error) {
	return func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}
}

// listen starts a launcher with cfg in the test process, around a counting
// backend, and returns the counter. It runs until the test ends.
func (e *env) listen(t *testing.T, cfg launcher.RunConfig) func() int {
	t.Helper()
	be := &countingBackend{Backend: &launcher.LinuxBackend{CgroupBase: cfg.CgroupBase, Env: cfg.Env, System: e.sys}}
	srv, err := launcher.NewServer(launcher.Config{
		Backend: be, Check: cfg.Engine.Check, BinDir: cfg.BinDir, SocketDir: cfg.SocketDir, Audit: cfg.Audit,
	})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := launcher.ListenUnix(cfg.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(cfg.SocketPath); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("launcher socket = %v, %v; want mode 0600", fi, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	if cfg.SocketPath == e.os.cfg.SocketPath {
		e.os.stop, e.os.served = cancel, served
	}
	t.Cleanup(cancel)
	go func() { defer close(served); _ = srv.Serve(ctx, ln) }()
	return func() int { return int(be.starts.Load()) }
}

// checkStarted: the mestre-pty has no capability to hand to the agents, is
// the leader of a session of its own and, where there is a delegated
// cgroup, was born in the cgroup of its terminal.
func (e *env) checkStarted(t *testing.T, _ context.Context, spec terminal.SpawnSpec, sp terminal.Spawned, _ *terminal.Client) {
	t.Helper()
	status, err := os.ReadFile("/proc/" + strconv.Itoa(sp.PID) + "/status")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(status), "\n") {
		for _, cap := range []string{"CapEff:", "CapAmb:"} {
			if v, ok := strings.CutPrefix(l, cap); ok && strings.TrimSpace(v) != "0000000000000000" {
				t.Errorf("the mestre-pty carries capabilities: %s", l)
			}
		}
	}
	if sid, err := unix.Getsid(sp.PID); err != nil || sid != sp.PID {
		t.Errorf("session of the mestre-pty = %d, %v; want its own (%d)", sid, err, sp.PID)
	}
	t.Run("in the cgroup of the terminal", func(t *testing.T) {
		t.Parallel()
		if e.os.cgBase == "" {
			// No delegated cgroup here: the whole test runs again in a
			// systemd scope that has one.
			delegated.Rerun(t, envBuilds+"="+builds.dir)
			return
		}
		want := filepath.Join(e.os.cgBase, "term-"+spec.ID)
		if sp.CgroupDir != want {
			t.Errorf("CgroupDir = %q, want %q", sp.CgroupDir, want)
		}
		got, err := terminal.ProcCgroup(sp.PID)
		if err != nil || !strings.HasSuffix(got, "/term-"+spec.ID) {
			t.Errorf("cgroup of the mestre-pty = %q, %v; want term-%s", got, err, spec.ID)
		}
	})
}

// checkRestart: restarting the launcher does not touch the mestre-pty it
// started, and the new launcher, which keeps no state, can still end it.
func (e *env) checkRestart(t *testing.T) {
	t.Helper()
	ctx := ctxFor(t)
	spec := e.spec("t07-restart")
	sp, err := e.client.Spawn(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = e.client.KillCgroup(context.Background(), spec.ID)
		_ = e.sys.KillTree(context.Background(), sp.ProcID, nil)
	})

	e.os.stop()
	<-e.os.served
	cfg := e.os.cfg
	srv, ln, err := launcher.Prepare(cfg) // the production way to build it
	if err != nil {
		t.Fatal(err)
	}
	sctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	t.Cleanup(func() { cancel(); <-served })
	go func() { defer close(served); _ = srv.Serve(sctx, ln) }()
	// Registered after the server's, so it runs before it stops.
	t.Cleanup(func() { _ = e.client.KillCgroup(context.Background(), spec.ID) })

	if !terminal.Alive(e.sys, sp.ProcID) {
		t.Fatal("the mestre-pty did not survive the restart of the launcher")
	}
	if e.os.cgBase == "" {
		_ = e.sys.KillTree(ctx, sp.ProcID, nil)
		// The new launcher ends the terminal by its cgroup: run again in a
		// systemd scope that has a delegated one.
		delegated.Rerun(t, envBuilds+"="+builds.dir)
		return
	}
	if err := e.client.KillCgroup(ctx, spec.ID); err != nil {
		t.Fatalf("the new launcher cannot end the terminal: %v", err)
	}
	if err := e.sys.WaitExit(ctx, sp.PID); err != nil {
		t.Fatalf("mestre-pty %d still alive after kill: %v", sp.PID, err)
	}
	if _, err := os.Stat(sp.CgroupDir); err == nil {
		t.Errorf("cgroup %s was not removed", sp.CgroupDir)
	}
}
