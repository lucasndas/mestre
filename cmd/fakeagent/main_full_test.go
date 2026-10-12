//go:build full

package main_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/internal/testkit"
	"github.com/lucasndas/mestre/internal/testkit/fakeagent"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The fake agent binary, called "claude", runs as a real process started by
// the real launcher and runs its hooks as real processes. CA-DUBLES-11
// itself (motor, base and `mestre hook`) closes when those exist (M3/M4).
func TestFakeAgentBinaryAsClaudeRunsRealHooks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	bin := filepath.Join(root, "bin", "claude")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	work, cfg := filepath.Join(root, "work"), filepath.Join(root, "cfg")
	payload := filepath.Join(root, "payload.json")
	testkit.WriteFiles(t, root, map[string]string{
		"cfg/.credentials.json": "{}",
		"cfg/.claude.json":      fmt.Sprintf(`{"projects": {%q: {"hasTrustDialogAccepted": true}}}`, work),
		"work/.claude/settings.local.json": fmt.Sprintf(
			`{"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "cat > %s"}]}]}}`, payload),
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

	out := &syncBuffer{}
	go func() { _, _ = io.Copy(out, p.PTY()) }()
	waitFor(t, out, fakeagent.PromptMarker)
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
// for text without polling.
type syncBuffer struct {
	mu      sync.Mutex
	b       bytes.Buffer
	changed chan struct{}
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.changed != nil {
		close(s.changed)
		s.changed = nil
	}
	return s.b.Write(p)
}

// snapshot returns the text and a channel closed on the next write.
func (s *syncBuffer) snapshot() (string, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.changed == nil {
		s.changed = make(chan struct{})
	}
	return s.b.String(), s.changed
}

func (s *syncBuffer) String() string {
	str, _ := s.snapshot()
	return str
}

func waitFor(t *testing.T, out *syncBuffer, s string) {
	t.Helper()
	timer := clock.Real{}.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		text, changed := out.snapshot()
		if strings.Contains(text, s) {
			return
		}
		select {
		case <-changed:
		case <-timer.C():
			t.Fatalf("timed out waiting for %q in:\n%s", s, text)
		}
	}
}
