//go:build full

package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/internal/terminal/ptyd"
	"github.com/lucasndas/mestre/internal/terminal/ptywire"
	"github.com/lucasndas/mestre/internal/testkit"
	"github.com/lucasndas/mestre/pkg/clock"
)

// TestMain lets the hook of the test below be this same test binary, so the
// test needs no shell tool (cat) and runs on Linux and Windows alike.
func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == hookArg {
		os.Exit(copyStdin(os.Args[2]))
	}
	os.Exit(m.Run())
}

const hookArg = "fakeagent-test-copy-stdin"

// copyStdin is the hook: it saves the payload it receives on stdin.
func copyStdin(path string) int {
	b, err := io.ReadAll(os.Stdin)
	if err == nil {
		err = os.WriteFile(path, b, 0o600)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// The fake agent binary, called "claude", runs as a real process started by
// the real launcher and runs its hooks as real processes (sh -c on Linux,
// cmd /c on Windows). CA-DUBLES-11 itself (motor, base and `mestre hook`)
// closes when those exist (M4).
func TestFakeAgentBinaryAsClaudeRunsRealHooks(t *testing.T) {
	t.Parallel()
	// Long, symlink-free path: the agent compares its working folder with
	// the trusted one, and Windows temp folders may use 8.3 short names.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin", "claude")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	work, cfg := filepath.Join(root, "work"), filepath.Join(root, "cfg")
	payload := filepath.Join(root, "payload.json")
	hook, err := json.Marshal(fmt.Sprintf(`"%s" %s "%s"`, self, hookArg, payload))
	if err != nil {
		t.Fatal(err)
	}
	testkit.WriteFiles(t, root, map[string]string{
		"cfg/.credentials.json": "{}",
		"cfg/.claude.json":      fmt.Sprintf(`{"projects": {%q: {"hasTrustDialogAccepted": true}}}`, work),
		"work/.claude/settings.local.json": fmt.Sprintf(
			`{"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": %s}]}]}}`, hook),
	})
	l := terminal.NewExecLauncher(func(string) string { return "full" })
	p, err := l.Start(context.Background(), terminal.Spec{
		Path: bin, Dir: work,
		Env: []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "CLAUDE_CONFIG_DIR=" + cfg},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Kill() }()

	out := &syncBuffer{em: ptyd.NewVTEmulator(ptywire.DefaultCols, ptywire.DefaultRows, ptyd.DefaultHistoryLines, nil)}
	defer out.em.Close()
	go func() { _, _ = io.Copy(out, p.PTY()) }()
	waitPrompt(t, out)
	if _, err := p.PTY().Write([]byte("/fake-dump\r")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, out, `"provider":"claude"`)
	got, err := os.ReadFile(payload)
	if err != nil || !strings.Contains(string(got), `"hook_event_name":"SessionStart"`) {
		t.Fatalf("hook payload = %s, %v", got, err)
	}
	_ = p.PTY().Close()
	if err := p.Wait(); err != nil {
		t.Fatalf("exit: %v\n%s", err, out.String())
	}
}

// syncBuffer collects output and signals every write, so the test waits
// for text without polling. It keeps the raw bytes and the screen they
// draw: a PTY passes the agent's bytes through, but ConPTY repaints with
// cursor positioning, so the prompt is a line on the screen, not a byte
// sequence.
type syncBuffer struct {
	mu      sync.Mutex
	b       bytes.Buffer
	em      ptyd.Emulator
	changed chan struct{}
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.changed != nil {
		close(s.changed)
		s.changed = nil
	}
	s.em.Write(p)
	return s.b.Write(p)
}

// snapshot returns the raw text, the screen (scrollback included) and a
// channel closed on the next write.
func (s *syncBuffer) snapshot() (raw, screen string, changed <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.changed == nil {
		s.changed = make(chan struct{})
	}
	return s.b.String(), s.em.History(-1), s.changed
}

func (s *syncBuffer) String() string {
	raw, _, _ := s.snapshot()
	return raw
}

func waitFor(t *testing.T, out *syncBuffer, s string) {
	t.Helper()
	out.wait(t, fmt.Sprintf("%q", s), func(raw, _ string) bool { return strings.Contains(raw, s) })
}

// waitPrompt waits for the agent to be ready for input: the screen shows a
// line of just ">".
func waitPrompt(t *testing.T, out *syncBuffer) {
	t.Helper()
	out.wait(t, "the prompt", func(_, screen string) bool { return slices.Contains(strings.Split(screen, "\n"), ">") })
}

func (s *syncBuffer) wait(t *testing.T, what string, holds func(raw, screen string) bool) {
	t.Helper()
	timer := clock.Real{}.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		raw, screen, changed := s.snapshot()
		if holds(raw, screen) {
			return
		}
		select {
		case <-changed:
		case <-timer.C():
			t.Fatalf("timed out waiting for %s in:\n%s\nscreen:\n%s", what, raw, screen)
		}
	}
}
