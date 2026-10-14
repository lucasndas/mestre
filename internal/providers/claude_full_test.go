//go:build full

package providers_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/terminal/ptyd"
	"github.com/lucasndas/mestre/pkg/clock"
)

// blockWatch is the character state of the test: it tells the test when the
// supervision blocked the character.
type blockWatch struct {
	*world
	blocked chan string
}

func (b *blockWatch) Block(ctx context.Context, agent string, r providers.BlockReason, detail string) error {
	err := b.world.Block(ctx, agent, r, detail)
	select {
	case b.blocked <- agent + "/" + string(r):
	default:
	}
	return err
}

// ptyKeyboard types into the PTY of the process and keeps what it typed.
type ptyKeyboard struct {
	agent ptyd.Agent
	typed *world
}

func (k ptyKeyboard) Input(ctx context.Context, data []byte) error {
	_ = k.typed.Input(ctx, data)
	_, err := k.agent.Write(data)
	return err
}

// screen follows the output of a process on a PTY with the emulator of the
// product, and tells the waiters when it changed.
type screen struct {
	mu      sync.Mutex
	em      ptyd.Emulator
	changed chan struct{}
}

func (s *screen) feed(p []byte) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.em.Write(p)
	close(s.changed)
	s.changed = make(chan struct{})
	return s.em.Screen()
}

func (s *screen) current() (string, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.em.Screen(), s.changed
}

// waitFor waits until the screen satisfies ok, without polling.
func (s *screen) waitFor(t *testing.T, what string, ok func(string) bool) {
	t.Helper()
	limit := clock.Real{}.NewTimer(90 * time.Second)
	defer limit.Stop()
	for {
		text, changed := s.current()
		if ok(text) {
			return
		}
		select {
		case <-changed:
		case <-t.Context().Done():
			t.Fatalf("waiting for %s: %v\n%s", what, t.Context().Err(), text)
		case <-limit.C():
			t.Fatalf("the screen did not show %s in 90 s:\n%s", what, text)
		}
	}
}

// CA-CLAUDE-05
func TestUsageLimitMenuOfTheFakeAgentInARealTerminal(t *testing.T) {
	t.Parallel()
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	bin := filepath.Join(t.TempDir(), "fakeagent"+exe)
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/lucasndas/mestre/cmd/fakeagent").CombinedOutput(); err != nil {
		t.Fatalf("go build fakeagent: %v\n%s", err, out)
	}
	rig := newFakeRig(t, map[string]string{"script.yaml": "prompts:\n  - match: go\n    steps:\n      - limit: session\n"})

	agent, err := (&ptyd.PTYRunner{Suite: "full"}).Start(t.Context(), ptyd.StartSpec{
		Path: bin, Args: []string{"--provider", "claude"}, Dir: rig.work, Cols: 200, Rows: 50,
		Env: append(os.Environ(), "HOME="+rig.home, "MESTRE_AGENT_ID=a1", "CLAUDE_CONFIG_DIR="+rig.cfg,
			"FAKEAGENT_SCRIPT="+filepath.Join(rig.root, "script.yaml")),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = agent.Kill()
		_ = agent.Close()
	})

	claude := providers.NewClaudeCode(providers.Options{})
	w := &blockWatch{world: &world{}, blocked: make(chan string, 4)}
	sup := providers.NewSupervisor(providers.SupervisorConfig{
		Clock: clock.Real{}, Adapter: claude, AgentID: "a1", TableID: "3-login",
		Keyboard: ptyKeyboard{agent, w.world}, Characters: w, TableMaster: w.world, Checklist: w.world, Inbox: w.world, Decisions: w.world,
	})
	t.Cleanup(sup.Close)
	sup.Start(t.Context())

	scr := &screen{em: ptyd.NewVTEmulator(200, 50, 100, nil), changed: make(chan struct{})}
	t.Cleanup(scr.em.Close)
	go func() { // the reader of the terminal: the screen goes to the supervision, as the engine does
		buf := make([]byte, 4096)
		for {
			n, err := agent.Read(buf)
			if n > 0 {
				sup.Screen(scr.feed(buf[:n]), true)
			}
			if err != nil {
				return
			}
		}
	}()

	scr.waitFor(t, "the prompt", func(s string) bool {
		return claude.Screens(providers.ScreenInput{Text: s, Stable: true}).State == providers.StateReady
	})
	if err := sup.Deliver(t.Context(), "d1", "go"); err != nil {
		t.Fatal(err)
	}
	scr.waitFor(t, "the usage limit menu", func(s string) bool { return strings.Contains(s, "What do you want to do?") })

	limit := clock.Real{}.NewTimer(90 * time.Second)
	defer limit.Stop()
	select {
	case got := <-w.blocked:
		if got != "a1/quota" {
			t.Errorf("blocked = %s, want a1/quota", got)
		}
	case <-limit.C():
		text, _ := scr.current()
		t.Fatalf("the character was not blocked in 90 s:\n%s", text)
	}
	// The agent took the option that waits: that is what the screen shows.
	scr.waitFor(t, "the wait for the limit", func(s string) bool { return strings.Contains(s, "Stopped. Waiting for the limit to reset") })
	if s := w.snapshot(); !slices.Equal(s.blocked, []string{"a1/quota"}) || !slices.Equal(s.typed, []string{"\x1b[200~go\x1b[201~\r", "\r"}) {
		t.Errorf("blocked %v, typed %q; want the quota block, the message and the Enter of the first option", s.blocked, s.typed)
	}
}
