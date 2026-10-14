//go:build full && linux

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/pkg/clock"
)

// A descendant that leaves the process group of the hook (`setsid cmd &`, the
// audit's probe) is still ended with the hook: the dispatcher adopts the
// orphans (subreaper) and the runner sweeps them, both when the hook ends by
// itself with 0 and when its time limit kills it. (The two cases run one after
// the other: the sweep ends every new descendant of the process, which in the
// test binary would be the other case's too.)
func TestAHookDescendantThatLeftItsSessionEndsWithTheHook(t *testing.T) {
	t.Parallel()
	escapedDescendantEnds(t, "; exit 0", 20*time.Second)
	escapedDescendantEnds(t, "; sleep 120", 2*time.Second)
}

func escapedDescendantEnds(t *testing.T, tail string, limit time.Duration) {
	t.Helper()
	// The hook starts a process in a session of its own, waits until that
	// process has written its PID, and then ends or hangs.
	const start = `setsid sh -c 'echo $$ > pid.tmp; mv pid.tmp pid; exec sleep 120' </dev/null >/dev/null 2>&1 & while [ ! -f pid ]; do :; done`
	dir := t.TempDir()
	clk := clock.Real{}
	ctx, cancel := clock.WithTimeout(t.Context(), clk, limit)
	defer cancel()
	exit, _, err := classHookRunner{}.Run(ctx, hook.ClassHook{Name: "escapa", Run: start + tail, Dir: dir, TimeoutMS: limit.Milliseconds(), Blocking: true},
		providers.EventPreTool, []byte(`{}`))
	b, rerr := os.ReadFile(filepath.Join(dir, "pid"))
	if rerr != nil {
		t.Fatalf("the hook did not start its process (exit %d, %v): %v", exit, err, rerr)
	}
	pid, perr := strconv.Atoi(strings.TrimSpace(string(b)))
	if perr != nil {
		t.Fatal(perr)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if alive := waitGone(t, pid, 10*time.Second); alive {
		t.Fatalf("the process %d the hook started in its own session is still alive after the hook ended (exit %d, %v)", pid, exit, err)
	}
}
