//go:build full

package terminal_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// CA-TERM-19: a hook of a class runs in a real terminal as its agent, in the
// folder asked and with the environment of the agent; the time limit kills the
// tree of the hook; a hook that writes a lot comes back cut.
func TestAHookOfAClassRunsInARealTerminalAsItsAgent(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	spec := h.agentSpec("h1")
	sub := filepath.Join(spec.WorkDir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	term := h.create(spec)
	c := term.Client()

	line := `printf '%s|%s|%s' "$MESTRE_AGENT_ID" "$MESTRE_HOOK_EVENT" "$(pwd)"; cat`
	if runtime.GOOS == "windows" {
		line = `echo %MESTRE_AGENT_ID%^|%MESTRE_HOOK_EVENT%^|%CD% & more`
	}
	res, err := c.RunHook(h.ctx, line, sub, "turn_end", []byte("evento"), 20*time.Second)
	if err != nil || res.Exit != 0 || res.TimedOut {
		t.Fatalf("result = %+v, %v", res, err)
	}
	out := strings.TrimSpace(res.Output)
	if !strings.HasPrefix(out, "a-h1|turn_end|") || !strings.Contains(out, "sub") || !strings.Contains(out, "evento") {
		t.Fatalf("the hook saw %q; want the environment of the agent, the event, the folder asked and the event on stdin", out)
	}

	if runtime.GOOS == "windows" {
		return // the tree and the large output are checked on Unix, where the shell gives the tools
	}
	pidFile := filepath.Join(h.cfg.Root, "neto.pid")
	res, err = c.RunHook(h.ctx, `sleep 600 & echo $! > `+pidFile+`; wait`, "", "turn_end", nil, time.Second)
	if err != nil || !res.TimedOut {
		t.Fatalf("a hook past its time: %+v, %v; want timed_out", res, err)
	}
	for _, pid := range readPIDs(t, pidFile) {
		h.gone(pid, "the grandchild of the hook")
	}

	res, err = c.RunHook(h.ctx, `head -c 200000 /dev/zero | tr '\0' x`, "", "turn_end", nil, 20*time.Second)
	if err != nil || !res.Truncated || len(res.Output) != 64<<10 {
		t.Fatalf("a hook that writes 200 KB: %d bytes, truncated %v, %v; want 64 KiB cut", len(res.Output), res.Truncated, err)
	}
}
