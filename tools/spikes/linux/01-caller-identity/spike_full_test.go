//go:build full && linux

package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The scenario processes are this test binary with a role argument.
func TestMain(m *testing.M) {
	if code, ok := RunRole(os.Args[1:]); ok {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

const scenarioDeadline = 20 * time.Second

func runMatrix(t *testing.T, mode Mode, cgroupBase string) {
	for _, sc := range Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			t.Parallel()
			if sc.Needs != "" {
				if _, err := exec.LookPath(sc.Needs); err != nil {
					t.Skipf("no %s", sc.Needs)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), scenarioDeadline)
			defer cancel()
			res, err := sc.Run(ctx, mode, cgroupBase)
			if err != nil {
				t.Fatal(err)
			}
			if res.Identified != sc.Want[mode] {
				t.Errorf("identified = %v, want %v (reason %q, chain %s)", res.Identified, sc.Want[mode], res.Reason, chainString(res.Chain))
			}
			if !res.Identified && res.Reason == "" {
				t.Error("refused without a reason")
			}
		})
	}
}

// The default of seguranca.md: climb to the terminal root.
func TestTreeWalk(t *testing.T) {
	t.Parallel()
	runMatrix(t, ModeTree, "")
}

// Tree walk accepting the terminal session (setsid of the root).
func TestTreeWalkWithSession(t *testing.T) {
	t.Parallel()
	runMatrix(t, ModeTreeSession, "")
}

// Plan B with PR_SET_CHILD_SUBREAPER on the terminal root.
func TestSubreaperRoot(t *testing.T) {
	t.Parallel()
	runMatrix(t, ModeSubreaper, "")
}

// Plan B with one cgroup per terminal. Needs a delegated cgroup: inside one
// it runs the matrix; otherwise it re-runs itself under
// `systemd-run --user --scope -p Delegate=yes`, and skips without it.
func TestCgroupPerTerminal(t *testing.T) {
	t.Parallel()
	if base, err := DelegatedCgroup(); err == nil {
		runMatrix(t, ModeCgroup, base)
		return
	}
	if os.Getenv("SPIKE_IN_SCOPE") != "" {
		t.Fatal("inside the scope, the cgroup is still not delegated")
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		t.Skip("no delegated cgroup and no systemd-run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*scenarioDeadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, "systemd-run", "--user", "--scope", "-p", "Delegate=yes", "--quiet", "--collect", "--",
		os.Args[0], "-test.run=^TestCgroupPerTerminal$", "-test.v", "-test.count=1")
	cmd.Env = append(os.Environ(), "SPIKE_IN_SCOPE=1")
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "Failed to connect to bus") {
		t.Skipf("no systemd user manager: %s", out)
	}
	if err != nil || !strings.Contains(string(out), "--- PASS: TestCgroupPerTerminal") {
		t.Fatalf("cgroup matrix in a delegated scope: %v\n%s", err, out)
	}
}

// fakeProc serves a scripted /proc so the races are deterministic.
type fakeProc struct {
	reads map[int][]Stat // successive answers per pid; the last one repeats
}

func (f *fakeProc) Stat(pid int) (Stat, error) {
	answers := f.reads[pid]
	if len(answers) == 0 {
		return Stat{}, os.ErrNotExist
	}
	s := answers[0]
	if len(answers) > 1 {
		f.reads[pid] = answers[1:]
	}
	if s.PID == 0 {
		return Stat{}, os.ErrNotExist
	}
	return s, nil
}

func isRoot(s Stat) bool { return s.PID == 100 && s.Start == 10 }

// The parent dies during the walk and its PID goes to a newer process: the
// walk sees a parent that started after the child, restarts from the
// caller and follows the reaper instead of trusting the stranger.
func TestWalkRestartsOnReusedParentPID(t *testing.T) {
	t.Parallel()
	p := &fakeProc{reads: map[int][]Stat{
		300: { // caller: first under 200, then reparented to the root (subreaper)
			{PID: 300, PPID: 200, Start: 30},
			{PID: 300, PPID: 100, Start: 30},
		},
		200: {{PID: 200, PPID: 999, Start: 50}}, // reused: started after the caller
		100: {{PID: 100, PPID: 1, Start: 10}},
	}}
	got, chain, err := Walk(p, 300, isRoot)
	if err != nil || got.PID != 100 {
		t.Fatalf("got %v, %v (chain %s)", got, err, chainString(chain))
	}
}

// A walk that keeps seeing reused PIDs gives up instead of guessing.
func TestWalkGivesUpOnPersistentRace(t *testing.T) {
	t.Parallel()
	p := &fakeProc{reads: map[int][]Stat{
		300: {{PID: 300, PPID: 200, Start: 30}},
		200: {{PID: 200, PPID: 100, Start: 50}},
	}}
	if _, _, err := Walk(p, 300, isRoot); !errors.Is(err, ErrRace) {
		t.Fatalf("err = %v, want ErrRace", err)
	}
}

// The caller is gone before the walk: refused, never matched by PID alone.
func TestWalkCallerGone(t *testing.T) {
	t.Parallel()
	if _, _, err := Walk(&fakeProc{reads: map[int][]Stat{}}, 300, isRoot); !errors.Is(err, ErrCallerGone) {
		t.Fatalf("err = %v, want ErrCallerGone", err)
	}
}

// SO_PEERPIDFD pins the caller: once the caller is reaped (its PID free for
// reuse) the pidfd says so, and the engine refuses whatever /proc showed.
// A caller reaped before accept gets no usable pidfd at all.
func TestPeerPidfdTracksReap(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), scenarioDeadline)
	defer cancel()
	dir, err := os.MkdirTemp("", "spk-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stop := context.AfterFunc(ctx, func() { ln.Close() })
	defer stop()
	env := append(cleanEnv(), "SPIKE_SOCK="+sock)

	alive := exec.CommandContext(ctx, os.Args[0], roleClient, "alive") // waits for an answer
	alive.Env = env
	if err := alive.Start(); err != nil {
		t.Fatal(err)
	}
	c, err := ln.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cred, pidfd, err := peer(c)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(pidfd)
	if got := pidfdPID(pidfd); got != int(cred.Pid) || got != alive.Process.Pid {
		t.Fatalf("pidfd pid = %d, SO_PEERCRED = %d, child = %d", got, cred.Pid, alive.Process.Pid)
	}
	_ = alive.Process.Kill()
	_ = alive.Wait() // reaped: the PID may now be reused
	if got := pidfdPID(pidfd); got != -1 {
		t.Fatalf("pidfd pid after reap = %d, want -1", got)
	}

	gone := exec.CommandContext(ctx, os.Args[0], roleClient, "-exit-after-write", "gone")
	gone.Env = env
	if err := gone.Run(); err != nil { // exits and is reaped before accept
		t.Fatal(err)
	}
	c2, err := ln.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	cred2, pidfd2, err := peer(c2)
	if cred2 == nil || int(cred2.Pid) != gone.Process.Pid {
		t.Fatalf("SO_PEERCRED of a reaped peer = %+v (%v)", cred2, err)
	}
	if err == nil {
		defer syscall.Close(pidfd2)
		if got := pidfdPID(pidfd2); got != -1 {
			t.Fatalf("pidfd of a peer reaped before accept points to %d", got)
		}
	}
	t.Logf("peer reaped before accept: SO_PEERCRED pid %d, SO_PEERPIDFD error %v", cred2.Pid, err)
}

func TestParseStatWithOddComm(t *testing.T) {
	t.Parallel()
	line := "4242 (a) b (c)) S 1 4242 4242 0 -1 4194560 1 0 0 0 0 0 0 0 20 0 1 0 98765 1 1 1\n"
	s, err := ParseStat([]byte(line))
	if err != nil || s.Comm != "a) b (c)" || s.PPID != 1 || s.SID != 4242 || s.Start != 98765 || s.State != 'S' {
		t.Fatalf("got %+v, %v", s, err)
	}
}

func BenchmarkTreeWalk(b *testing.B) {
	for b.Loop() {
		_, _, _ = Walk(SysProc{}, os.Getpid(), func(Stat) bool { return false })
	}
}

func BenchmarkCgroupRead(b *testing.B) {
	for b.Loop() {
		_, _ = Cgroup(os.Getpid())
	}
}
