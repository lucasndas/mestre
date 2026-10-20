//go:build full && linux

package localchan_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/testkit/leakcheck"
)

// envInScope tells the test binary it is running in the delegated scope.
const envInScope = "MESTRE_TEST_IN_SCOPE"

// inDelegatedScope makes sure the test runs in a systemd scope with
// Delegate=yes, which is what the unit of the mestre-launcher has and what
// lets the engine put each terminal in a cgroup of its own: outside it, the
// test runs itself again under `systemd-run --user --scope -p Delegate=yes`
// and reports what that run said, and returns false. A machine with no
// systemd user manager skips the test, with the reason. Inside the scope it
// returns the cgroup, as /proc shows it.
func inDelegatedScope(t *testing.T, name string) (cgroup string, inside bool) {
	t.Helper()
	if os.Getenv(envInScope) != "" {
		cg := ownCgroup()
		probe, err := os.MkdirTemp(filepath.Join("/sys/fs/cgroup", cg), "probe-")
		if err != nil {
			t.Skipf("the cgroup %s is not delegated to this user: %v", cg, err)
		}
		_ = os.Remove(probe)
		return cg, true
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		t.Skip("no systemd-run on this machine: a delegated cgroup needs a systemd user manager")
	}
	scope := func(args ...string) *exec.Cmd {
		return exec.CommandContext(t.Context(), "systemd-run",
			append([]string{"--user", "--scope", "--quiet", "-p", "Delegate=yes", "--"}, args...)...)
	}
	if out, err := scope("true").CombinedOutput(); err != nil {
		t.Skipf("no systemd user manager to delegate a cgroup (systemd-run --user --scope: %v: %s)", err, strings.TrimSpace(string(out)))
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := scope(self, "-test.run", "^"+name+"$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), envInScope+"=1", envBuilds+"="+builds.dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("in the delegated scope: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "--- SKIP") {
		t.Skipf("skipped in the delegated scope:\n%s", out)
	}
	t.Logf("in the delegated scope:\n%s", out)
	return "", false
}

// orphansScript is the agent of CA-LOCAL-11: a shell that calls `mestre list`
// once the plain way and then leaves three orphans (nohup, a double fork,
// setsid -f), each from its own folder. An orphan ignores SIGHUP, says it is
// ready through a FIFO and then waits, with no sleep (it spins on kill -0),
// until its parent is gone, so it is an orphan for sure when it calls. The
// shell stays alive until all three have called and said so through a second
// FIFO: when the agent of a terminal ends on its own, the engine ends whatever
// it left behind (docs/arquitetura/terminal.md#encerramento), so an orphan
// that called after the shell was gone would be killed before it could.
// Both FIFOs are held open for reading and writing, so reads never see an end
// of file, and the orphans open them for reading and writing too: opened for
// writing only, the open blocks until someone reads, and an orphan of a shell
// that is gone (a test that failed early) would wait for ever, out of reach of
// the tree and of the cgroup of the terminal.
func orphansScript(mestre, base string) string {
	ready, done := base+"/ready", base+"/done"
	orphan := func(dir string) string {
		return `sh -c "p=\$PPID; trap '' HUP; echo ready 1<> '` + ready + `'; while kill -0 \$p 2>/dev/null; do :; done; ` +
			`cd '` + base + `/` + dir + `' && '` + mestre + `' list; echo done 1<> '` + done + `'" >/dev/null 2>&1`
	}
	return strings.Join([]string{
		`cd '` + base + `/direct' && '` + mestre + `' list >/dev/null 2>&1`,
		`( nohup ` + orphan("nohup") + ` & )`,
		`( ` + orphan("subshell") + ` & )`,
		`setsid -f ` + orphan("setsid"),
		`exec 3<> '` + ready + `' 4<> '` + done + `'; read a <&3; read b <&3; read c <&3; read d <&4; read e <&4; read f <&4`,
	}, "\n")
}

func orphansBase(t *testing.T, e *fullEngine) string {
	t.Helper()
	base := filepath.Join(e.root, "calls")
	// Whatever an orphan of the test is still doing when it ends is ended.
	t.Cleanup(func() { leakcheck.Reap(base + string(filepath.Separator)) })
	for _, m := range []string{"direct", "nohup", "subshell", "setsid"} {
		if err := os.MkdirAll(filepath.Join(base, m), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"ready", "done"} {
		if err := syscall.Mkfifo(filepath.Join(base, name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return base
}

// CA-LOCAL-11, Linux only because it is about cgroups, orphans and the
// shell (its pair on Windows is CA-LOCAL-13, with the Job Object): with a
// real delegated cgroup, the orphans of a terminal are identified by the
// cgroup and accepted with the right terminal; without the cgroup, by the
// process tree alone, the plain call is accepted and the three orphans,
// adopted by init, are refused.
func TestTheOrphansOfATerminalAreIdentifiedByItsCgroup(t *testing.T) {
	t.Parallel()
	cgroup, inside := inDelegatedScope(t, "TestTheOrphansOfATerminalAreIdentifiedByItsCgroup")
	if !inside {
		return
	}

	t.Run("with the cgroup", func(t *testing.T) {
		t.Parallel()
		e := newFullEngine(t, engineOptions{cgroup: cgroup, mode: localchan.ModeCgroup})
		const agent = "char-11"
		base := orphansBase(t, e)
		e.spawn("cg", agent, "/bin/sh", []string{"-c", orphansScript(builds.mestre, base)}, e.env(agent), e.root)

		seen := map[string]bool{}
		for range 4 {
			c := e.nextCall("a call of the shell or of an orphan")
			if !c.Caller.Identified || c.Caller.AgentID != agent || c.Caller.TerminalID != "cg" {
				t.Fatalf("call from %s: %+v", c.Cwd, c.Caller)
			}
			seen[filepath.Base(c.Cwd)] = true
		}
		for _, m := range []string{"direct", "nohup", "subshell", "setsid"} {
			if !seen[m] {
				t.Errorf("no accepted call from %q: %v", m, seen)
			}
		}
	})

	t.Run("without the cgroup", func(t *testing.T) {
		t.Parallel()
		e := newFullEngine(t, engineOptions{mode: localchan.ModeTree})
		const agent = "char-11b"
		base := orphansBase(t, e)
		e.spawn("tree", agent, "/bin/sh", []string{"-c", orphansScript(builds.mestre, base)}, e.env(agent), e.root)

		// The plain call comes first and is accepted: it is a descendant of
		// the mestre-pty, which is alive.
		if c := e.nextCall("the plain call"); filepath.Base(c.Cwd) != "direct" || c.Caller.TerminalID != "tree" {
			t.Fatalf("first call: %+v", c)
		}
		for range 3 {
			if a := e.nextRefusal("an orphan"); a.AgentID != agent {
				t.Fatalf("audit = %+v", a)
			}
		}
		e.noMoreCalls()
	})
}
