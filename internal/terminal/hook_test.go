package terminal_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/terminal"
	"github.com/mestre-ia/mestre/internal/terminal/ptyd"
	"github.com/mestre-ia/mestre/internal/terminal/ptywire"
)

// fakeHooks plays the shell: it records what it was asked and answers as told.
type fakeHooks struct {
	mu     sync.Mutex
	specs  []ptyd.HookSpec
	result ptyd.HookResult
	block  chan struct{} // when set, Run waits for it to close
	in     chan struct{} // told when a Run began
	// untilEnd makes Run wait for the end of its context, as a shell that runs
	// until it is killed.
	untilEnd bool
}

func (f *fakeHooks) Run(ctx context.Context, spec ptyd.HookSpec) (ptyd.HookResult, error) {
	f.mu.Lock()
	f.specs = append(f.specs, spec)
	first := len(f.specs) == 1
	f.mu.Unlock()
	if f.in != nil {
		select {
		case f.in <- struct{}{}:
		default:
		}
	}
	if f.untilEnd {
		<-ctx.Done()
		return ptyd.HookResult{}, ctx.Err()
	}
	if f.block != nil && first { // only the first waits: a second one that gets in is a failure of the test, not a hang
		<-f.block
	}
	return f.result, nil
}

func (f *fakeHooks) last() ptyd.HookSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.specs[len(f.specs)-1]
}

// CA-TERM-18
func TestAHookOfAClassRunsAsTheAgentInItsFolderAndOnlyThere(t *testing.T) {
	t.Parallel()
	hooks := &fakeHooks{result: ptyd.HookResult{Exit: 3, Output: "dito", TimedOut: false}}
	r := newRig(t, rigOptions{hooks: hooks})
	c := r.dial(t)

	// before the agent started
	if _, err := c.RunHook(t.Context(), "echo", "", "turn_end", nil, 5*time.Second); !isRemote(err, ptywire.CodeNotRunning) {
		t.Fatalf("before the start: %v; want not_running", err)
	}

	w := absRoot()
	if _, err := c.Start(t.Context(), ptywire.Msg{Path: "agent", Dir: w, Env: []string{"MESTRE_AGENT_ID=a1", "A=1"}}); err != nil {
		t.Fatal(err)
	}
	res, err := c.RunHook(t.Context(), "npm test", filepath.Join(w, "sub"), "turn_end", []byte(`{"event":"turn_end"}`), 90*time.Second)
	if err != nil || res.Exit != 3 || res.Output != "dito" || res.TimedOut || res.Truncated {
		t.Fatalf("result = %+v, %v", res, err)
	}
	got := hooks.last()
	if got.Run != "npm test" || got.Dir != filepath.Join(w, "sub") || got.Event != "turn_end" || string(got.Stdin) != `{"event":"turn_end"}` || got.Timeout != 90*time.Second ||
		!slices.Contains(got.Env, "MESTRE_AGENT_ID=a1") || !slices.Contains(got.Env, "A=1") {
		t.Fatalf("what the shell was asked = %+v", got)
	}
	// no folder asked: the one of the agent
	if _, err := c.RunHook(t.Context(), "ls", "", "turn_end", nil, time.Second); err != nil || hooks.last().Dir != w {
		t.Fatalf("folder of the agent: %v, %+v", err, hooks.last())
	}
	// outside the folder of the agent, or not absolute
	for _, dir := range []string{filepath.Join(filepath.VolumeName(w)+string(filepath.Separator), "etc"), w + string(filepath.Separator) + ".." + string(filepath.Separator) + "etc", w + "x", "sub", ".."} {
		if _, err := c.RunHook(t.Context(), "ls", dir, "turn_end", nil, time.Second); !isRemote(err, ptywire.CodeBadRequest) {
			t.Errorf("folder %q: %v; want bad_request", dir, err)
		}
	}
	// a request that is not valid
	for name, d := range map[string]time.Duration{"too short": 100 * time.Millisecond, "too long": 2 * time.Hour} {
		if _, err := c.RunHook(t.Context(), "ls", "", "turn_end", nil, d); !isRemote(err, ptywire.CodeBadRequest) {
			t.Errorf("%s: %v; want bad_request", name, err)
		}
	}
	if _, err := c.RunHook(t.Context(), "", "", "turn_end", nil, time.Second); !isRemote(err, ptywire.CodeBadRequest) {
		t.Errorf("no command: %v; want bad_request", err)
	}
}

// CA-TERM-18: one hook at a time in a terminal, and a mestre-pty that has not the
// message is never replaced by another account.
func TestOnlyOneHookRunsAtATimeAndAnOldMestrePtyIsNeverReplaced(t *testing.T) {
	t.Parallel()
	hooks := &fakeHooks{block: make(chan struct{}), in: make(chan struct{}, 1)}
	r := newRig(t, rigOptions{hooks: hooks})
	c := r.dial(t)
	if _, err := c.Start(t.Context(), ptywire.Msg{Path: "agent", Dir: absRoot()}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := c.RunHook(t.Context(), "sleep", "", "turn_end", nil, time.Minute); done <- err }()
	<-hooks.in
	if _, err := c.RunHook(t.Context(), "ls", "", "turn_end", nil, time.Second); !isRemote(err, ptywire.CodeBusy) {
		t.Fatalf("a second hook: %v; want busy", err)
	}
	close(hooks.block)
	if err := <-done; err != nil {
		t.Fatalf("the first hook: %v", err)
	}

	old := newRig(t, rigOptions{proto: ptywire.ProtoInJob, hooks: &fakeHooks{}})
	oc := old.dial(t)
	if _, err := oc.Start(t.Context(), ptywire.Msg{Path: "agent", Dir: absRoot()}); err != nil {
		t.Fatal(err)
	}
	if _, err := oc.RunHook(t.Context(), "ls", "", "turn_end", nil, time.Second); !errors.Is(err, terminal.ErrUnsupported) {
		t.Fatalf("a mestre-pty of protocol 3: %v; want ErrUnsupported", err)
	}
	if len(old.hooks().specs) != 0 {
		t.Fatal("a mestre-pty without the message ran the hook")
	}
}

func isRemote(err error, code string) bool {
	var re *terminal.RemoteError
	return errors.As(err, &re) && re.Code == code
}

// CA-TERM-18: the end of the terminal ends the hook that runs, with its tree.
func TestTheEndOfTheTerminalEndsTheHookThatRuns(t *testing.T) {
	t.Parallel()
	hooks := &fakeHooks{untilEnd: true, in: make(chan struct{}, 1)}
	r := newRig(t, rigOptions{hooks: hooks})
	c := r.dial(t)
	if _, err := c.Start(t.Context(), ptywire.Msg{Path: "agent", Dir: absRoot()}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := c.RunHook(t.Context(), "sleep 600", "", "turn_end", nil, time.Hour); done <- err }()
	<-hooks.in
	if err := r.sess.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("the hook ended without an error when the terminal was killed")
	}
}

// absRoot is an absolute folder of the system that runs the test (the hook
// folder check wants one; "/w" is not absolute on Windows).
func absRoot() string {
	return filepath.Join(filepath.VolumeName(os.TempDir())+string(filepath.Separator), "w")
}
