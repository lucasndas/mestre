//go:build full && linux

package launcher_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/lucasndas/mestre/internal/launcher"
	"github.com/lucasndas/mestre/internal/terminal"
)

// proc is a process of the test, reaped when it ends and killed with its
// group when the test ends.
type proc struct {
	cmd  *exec.Cmd
	done chan struct{}
	id   terminal.ProcID
}

// spawn starts argv in a session of its own (so its PID is also its session
// and group ID), with no stdout or stderr of the test.
func spawn(t *testing.T, argv ...string) *proc {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &proc{cmd: cmd, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(p.done) }()
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-p.done
	})
	start, err := terminal.NewSystem().Identify(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	p.id = terminal.ProcID{PID: cmd.Process.Pid, Start: start}
	return p
}

func (p *proc) alive() bool { return terminal.Alive(terminal.NewSystem(), p.id) }

// sessionMembers counts the live processes whose session is sid.
func sessionMembers(t *testing.T, sid int) int {
	t.Helper()
	ents, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range ents {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		f := bytes.Fields(b[bytes.LastIndexByte(b, ')')+1:])
		if len(f) > 3 && f[0][0] != 'Z' && f[0][0] != 'X' && string(f[3]) == strconv.Itoa(sid) {
			n++
		}
	}
	return n
}

// CA-TERM-05 (Linux without a delegated cgroup): the engine, which runs as
// another account than the agents, cannot signal their processes, so the
// launcher ends the mestre-pty and the session of the agent, by identity.
// Here the account is the same one (the test has no CAP_SETUID), so the
// switch of the user ID of the thread is not exercised; that part is in the
// real-account tests (internal/realaccount). What is: the identity of
// the mestre-pty, the account filter and the sessions.
func TestTheLauncherEndsATerminalWithoutACgroupByIdentity(t *testing.T) {
	t.Parallel()
	be := &launcher.LinuxBackend{System: terminal.NewSystem()} // no CgroupBase: no cgroup to kill
	uid := os.Geteuid()

	t.Run("ends the mestre-pty and the whole session", func(t *testing.T) {
		t.Parallel()
		pty := spawn(t, "sleep", "600")
		// The agent started after the mestre-pty, in a session of its own,
		// with a child that keeps the session when the leader goes. The
		// shell writes to a FIFO once the child is forked, so the session
		// has both before the count (opening the FIFO blocks until then).
		fifo := filepath.Join(t.TempDir(), "forked")
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Fatal(err)
		}
		agent := spawn(t, "sh", "-c", `sleep 600 & echo > "$1"; exec sleep 600`, "sh", fifo)
		if b, err := os.ReadFile(fifo); err != nil || len(b) == 0 {
			t.Fatalf("the agent did not report its child: %q, %v", b, err)
		}
		bystander := spawn(t, "sleep", "600")
		if n := sessionMembers(t, agent.id.PID); n < 2 {
			t.Fatalf("the session of the agent has %d processes, want the leader and its child", n)
		}
		if err := be.KillProcs(t.Context(), "t1", pty.id, []int{agent.id.PID}); err != nil {
			t.Fatal(err)
		}
		if pty.alive() || agent.alive() || sessionMembers(t, agent.id.PID) != 0 {
			t.Errorf("mestre-pty alive: %v; agent alive: %v; %d left in its session", pty.alive(), agent.alive(), sessionMembers(t, agent.id.PID))
		}
		if !bystander.alive() {
			t.Error("a process of another session was ended")
		}
	})

	t.Run("a PID with another start identity is not touched", func(t *testing.T) {
		t.Parallel()
		pty := spawn(t, "sleep", "600")
		agent := spawn(t, "sleep", "600")
		wrong := terminal.ProcID{PID: pty.id.PID, Start: pty.id.Start + "0"} // a later start: another process
		if err := be.KillProcs(t.Context(), "t1", wrong, []int{agent.id.PID}); err != nil {
			t.Fatal(err)
		}
		if !pty.alive() {
			t.Error("a process whose start identity differs was ended")
		}
		if !agent.alive() {
			t.Error("a session that started before the identity given was ended")
		}
	})

	t.Run("only the account of the agents", func(t *testing.T) {
		t.Parallel()
		pty := spawn(t, "sleep", "600")
		agent := spawn(t, "sleep", "600")
		err := terminal.KillTreeOf(t.Context(), pty.id, []int{agent.id.PID}, uid+1)
		if err == nil {
			t.Error("processes of another account were taken for the agents'")
		}
		if !pty.alive() || !agent.alive() {
			t.Error("a process of another account was ended")
		}
	})

	t.Run("the mestre-pty already gone: its sessions still end", func(t *testing.T) {
		t.Parallel()
		pty := spawn(t, "sleep", "600")
		agent := spawn(t, "sleep", "600")
		_ = syscall.Kill(pty.id.PID, syscall.SIGKILL)
		<-pty.done
		if err := be.KillProcs(t.Context(), "t1", pty.id, []int{agent.id.PID}); err != nil {
			t.Fatal(err)
		}
		if agent.alive() {
			t.Error("the session of the agent survived its mestre-pty")
		}
	})
}
