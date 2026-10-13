//go:build realaccount

package realaccount_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/realaccount"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/internal/terminal/ptywire"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The tests of this package run as the engine of the instance, in the
// account and the unit (Linux) or service (Windows) tools/realaccount made
// for it, against the real mestre-launcher it started. Everything the agents
// do happens in a terminal the launcher started, as the agent account: the
// agent of a terminal is a helper mode of this very test binary
// ("helper-agent <mode> ...").

const (
	envRole = realaccount.EnvRole
	// helperAgent is the first argument of the agent of a terminal.
	helperAgent = "helper-agent"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		if handled, code := osMain(m, os.Args[1:]); handled {
			os.Exit(code)
		}
		if os.Args[1] == helperAgent {
			os.Exit(runAgent(os.Args[2:]))
		}
	}
	if err := realaccount.CheckDisposable(os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if os.Getenv(envRole) != "engine" {
		fmt.Fprintf(os.Stderr, "refusing to run: %s is not engine. This binary runs as the engine the machine was prepared for: use `go run ./tools/realaccount run`\n", envRole)
		os.Exit(2)
	}
	os.Exit(m.Run())
}

// runAgent is the agent a terminal runs. The modes common to both systems
// are here; osAgent has the ones of one system.
func runAgent(args []string) int {
	if len(args) == 0 {
		return 2
	}
	switch args[0] {
	case "sleeper": // [quiet]: ignores SIGTERM and waits
		signal.Ignore(syscall.SIGTERM)
		if len(args) < 2 || args[1] != "quiet" {
			fmt.Print("READY\r\n")
		}
		block()
		return 0
	case "tree": // <pids file> <escape 0|1>: grandchildren that ignore SIGTERM, one out of the session
		return runTree(args[1:])
	}
	return osAgent(args)
}

// block waits for ever. It is not a bare select{}: on Windows a program with
// nothing else running dies of "all goroutines are asleep"; a pending timer
// keeps the runtime from calling it a deadlock.
func block() {
	<-clock.Real{}.NewTimer(1000 * time.Hour).C()
}

// runTree starts a grandchild that ignores SIGTERM and, when asked and where
// the system allows, one that leaves the session (what `setsid -f` does),
// writes their PIDs and reports READY.
func runTree(args []string) int {
	signal.Ignore(syscall.SIGTERM)
	self, err := os.Executable()
	if err != nil || len(args) < 2 {
		return 1
	}
	var pids []string
	spawn := func(attr *syscall.SysProcAttr) bool {
		c := exec.Command(self, helperAgent, "sleeper", "quiet")
		c.SysProcAttr = attr
		if c.Start() != nil {
			return false
		}
		pids = append(pids, strconv.Itoa(c.Process.Pid))
		return true
	}
	spawn(nil)
	if args[1] == "1" {
		spawn(leaveSessionAttr())
	}
	if err := writeAtomic(args[0], []byte(strings.Join(pids, "\n"))); err != nil {
		return 1
	}
	fmt.Print("READY\r\n")
	block()
	return 0
}

// writeAtomic writes path so a reader sees all of it or none.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// syncBuffer collects the output of a terminal and wakes whoever waits for a
// text in it, without polling.
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

// waitFor blocks until the text holds sub.
func (s *syncBuffer) waitFor(t *testing.T, sub string) string {
	t.Helper()
	limit := clock.Real{}.NewTimer(2 * time.Minute) // a hang is a failure, not a 10-minute timeout
	defer limit.Stop()
	for {
		s.mu.Lock()
		text := s.b.String()
		if s.changed == nil {
			s.changed = make(chan struct{})
		}
		ch := s.changed
		s.mu.Unlock()
		if strings.Contains(text, sub) {
			return text
		}
		select {
		case <-ch:
		case <-t.Context().Done():
			t.Fatalf("output never held %q:\n%s", sub, text)
		case <-limit.C():
			t.Fatalf("output did not hold %q in two minutes:\n%s", sub, text)
		}
	}
}

// observe follows the output of a terminal from its start, on a connection of
// its own.
func observe(ctx context.Context, t *testing.T, term *terminal.Terminal) *syncBuffer {
	t.Helper()
	rec := term.Record()
	c, err := terminal.Dial(ctx, clock.Real{}, rec.SocketPath, rec.Secret, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	buf := &syncBuffer{}
	if _, err := c.Follow(ctx, 0, func(f ptywire.Frame) { _, _ = buf.Write(f.Data) }); err != nil {
		t.Fatal(err)
	}
	return buf
}

// ctxFor is a context that ends with the test or after a minute and a half.
func ctxFor(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := clock.WithTimeout(t.Context(), clock.Real{}, 90*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// until polls cond every 100 ms until it holds or ctx ends: the release of a
// logon session or of a profile after the mestre-pty ends is something the
// system does later, with no event the tests can wait on.
func until(ctx context.Context, t *testing.T, what string, cond func() bool) {
	t.Helper()
	for !cond() {
		if err := clock.Sleep(ctx, clock.Real{}, 100*time.Millisecond); err != nil {
			t.Fatalf("%s: still not true when the wait ended: %v", what, err)
		}
	}
}

var idCounter atomic.Int64

// newID is a terminal id no other terminal of the run has.
func newID(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, idCounter.Add(1))
}

// ended reports whether the process of p is gone.
func gone(p terminal.ProcID) bool { return !terminal.Alive(terminal.NewSystem(), p) }
