//go:build full

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/pkg/clock"
)

// envHookHelper makes the test binary a process of a hook: "spawn" starts
// a child that inherits stderr, writes its PID to the file named by
// envHookPIDFile and then waits for ever, like the child; "hold" only waits.
const (
	envHookHelper  = "MESTRE_TEST_HOOK_HELPER"
	envHookPIDFile = "MESTRE_TEST_HOOK_PIDFILE"
)

func TestMain(m *testing.M) {
	switch os.Getenv(envHookHelper) {
	case "spawn":
		self, _ := os.Executable()
		c := exec.Command(self)
		c.Env = append(os.Environ(), envHookHelper+"=hold")
		// mestre:allow child-stdio: this is the hook itself, and its child must hold the stderr the runner reads
		c.Stderr = os.Stderr
		if err := c.Start(); err != nil {
			os.Exit(3)
		}
		_ = os.WriteFile(os.Getenv(envHookPIDFile), []byte(strconv.Itoa(c.Process.Pid)), 0o600)
		block()
	case "hold":
		block()
	}
	os.Exit(m.Run())
}

// block waits for ever without sleeping: on a pipe nobody writes to.
func block() {
	r, w, err := os.Pipe()
	if err != nil {
		os.Exit(3)
	}
	var b [1]byte
	_, _ = r.Read(b[:])
	_ = w.Close()
	os.Exit(0)
}

// Part of CA-CLASSES-03, on the real runner of `mestre hook`: a hook that
// leaves a child holding its stderr still ends at its time limit, as a
// timeout (which denies when the hook is blocking), and the child goes with
// it: the hook runs in a process group of its own (Linux) or a Job Object
// (Windows), killed whole.
func TestTheRealRunnerEndsTheHookAndItsChildrenAtTheTimeLimit(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	t.Setenv(envHookHelper, "spawn") // the runner passes the environment on
	t.Setenv(envHookPIDFile, pidFile)
	const limit = 2 * time.Second
	clk := clock.Real{}
	ctx, cancel := clock.WithTimeout(t.Context(), clk, limit)
	defer cancel()
	start := clk.Now()
	exit, _, err := classHookRunner{}.Run(ctx, hook.ClassHook{Name: "segura", Run: `"` + self + `"`, Dir: dir, TimeoutMS: limit.Milliseconds(), Blocking: true},
		providers.EventPreTool, []byte(`{}`))
	took := clock.Since(clk, start)
	if err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("the hook ended with exit %d, err %v; want the time limit", exit, err)
	}
	if took > limit+hookWaitDelay+time.Second {
		t.Fatalf("the runner returned after %v; the limit was %v", took, limit)
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the hook did not start its child within the limit: %v", err)
	}
	pid, err := strconv.Atoi(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if alive := waitGone(t, pid, 10*time.Second); alive {
		t.Fatalf("the child %d of the hook is still alive after the time limit", pid)
	}
}
