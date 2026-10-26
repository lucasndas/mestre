//go:build full

package hook_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/store"
	machinedb "github.com/mestre-ia/mestre/internal/store/machine"
	"github.com/mestre-ia/mestre/internal/terminal"
	"github.com/mestre-ia/mestre/internal/terminal/ptyd"
	"github.com/mestre-ia/mestre/internal/terminal/ptywire"
	"github.com/mestre-ia/mestre/internal/testkit/leakcheck"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The full suite of the dispatcher starts real processes: the mestre-pty,
// the `mestre` binary (whose `mestre hook` the fake agent calls) and the fake
// agent, built here, and a helper that is this same test binary started with
// a first argument TestMain recognises, so the tests need no shell tool.
const helperTimer = "helper-hook-timer" // <times file> <program> <args…>: runs the program, appends its duration

var builds struct {
	dir       string
	pty       string
	mestre    string
	fakeagent string
}

const ptyVersion = "1.0.0"

func TestMain(m *testing.M) {
	if len(os.Args) > 3 && os.Args[1] == helperTimer {
		os.Exit(runTimer(os.Args[2], os.Args[3], os.Args[4:]))
	}
	dir, err := os.MkdirTemp("", "mhbin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	builds.dir = dir
	builds.pty = filepath.Join(dir, "pty", ptyVersion, "mestre-pty"+exe)
	builds.mestre = filepath.Join(dir, "mestre"+exe)
	builds.fakeagent = filepath.Join(dir, "fakeagent"+exe)
	for _, b := range [][]string{
		{"-o", builds.pty, "-ldflags", "-X main.version=" + ptyVersion, "github.com/mestre-ia/mestre/cmd/mestre-pty"},
		{"-o", builds.mestre, "github.com/mestre-ia/mestre/cmd/mestre"},
		{"-o", builds.fakeagent, "github.com/mestre-ia/mestre/cmd/fakeagent"},
	} {
		if out, err := exec.Command("go", append([]string{"build"}, b...)...).CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "go build %v: %v\n%s", b, err, out)
			os.Exit(1)
		}
	}
	code := m.Run()
	// A test that left a process of these binaries alive fails the run.
	code = leakcheck.Main(os.Stderr, code, dir)
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// runTimer is the hook command of CA-HOOK-07: it runs `mestre hook` with its
// stdin and output and appends to the times file how long the process took,
// from its start to its exit.
func runTimer(times, program string, args []string) int {
	c := exec.Command(program, args...)
	var out, errb bytes.Buffer
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, &out, &errb
	start := clock.Real{}.Now()
	err := c.Run()
	took := clock.Since(clock.Real{}, start)
	_, _ = os.Stdout.Write(out.Bytes())
	_, _ = os.Stderr.Write(errb.Bytes())
	f, ferr := os.OpenFile(times, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if ferr == nil {
		fmt.Fprintf(f, "%d\n", took.Nanoseconds())
		_ = f.Close()
	}
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		return ee.ExitCode()
	case err != nil:
		return 1
	}
	return 0
}

// outbuf collects the output of a terminal and lets a test wait for a text
// without polling. It keeps the raw bytes, and the screen they draw: a
// pseudo-terminal passes the program's bytes through, but ConPTY repaints
// with cursor positioning ("Tool Bash ran ESC[12;1H> "), so what marks the
// agent as free is a line on the screen, not a byte sequence.
type outbuf struct {
	mu      sync.Mutex
	b       bytes.Buffer
	em      ptyd.Emulator
	changed chan struct{}
}

func newOutbuf(t *testing.T) *outbuf {
	t.Helper()
	o := &outbuf{em: ptyd.NewVTEmulator(ptywire.DefaultCols, ptywire.DefaultRows, ptyd.DefaultHistoryLines, nil)}
	t.Cleanup(o.em.Close)
	return o
}

func (o *outbuf) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.changed != nil {
		close(o.changed)
		o.changed = nil
	}
	o.em.Write(p)
	return o.b.Write(p)
}

func (o *outbuf) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.String()
}

func (o *outbuf) waitFor(t *testing.T, sub string) {
	t.Helper()
	o.wait(t, fmt.Sprintf("%q", sub), func(raw, _ string) bool { return strings.Contains(raw, sub) })
}

// promptAfter is whether the screen holds a line of just ">" below the first
// line that holds after.
func promptAfter(screen, after string) bool {
	lines := strings.Split(screen, "\n")
	for i, l := range lines {
		if strings.Contains(l, after) {
			return slices.Contains(lines[i+1:], ">")
		}
	}
	return false
}

// screenNow is the screen as it is now (the scrollback and the visible rows).
func (o *outbuf) screenNow() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.em.History(-1)
}

// sayUntil types text and waits for done to hold of the screen. A call to the engine that
// passes the 800 ms the dispatcher waits (classes.DegradedReason: a loaded machine, the
// Windows runner) is decided alone, opens no request and keeps no waiting state (the
// engine drops what comes after the deadline), so the message is typed again once the
// agent is free; a step that is not about that deadline does not fail on it.
func sayUntil(t *testing.T, ctx context.Context, term *terminal.Terminal, o *outbuf, text string, done func(screen string) bool) {
	t.Helper()
	for try := 1; ; try++ {
		seen := strings.Count(o.screenNow(), classes.DegradedReason)
		say(t, ctx, term, text)
		o.wait(t, fmt.Sprintf("the answer to %q", text), func(_, screen string) bool {
			return done(screen) || strings.Count(screen, classes.DegradedReason) > seen
		})
		if done(o.screenNow()) {
			return
		}
		if try == 5 {
			t.Fatalf("the engine did not answer %q in time in %d tries:\n%s", text, try, o.screenNow())
		}
		// the turn is over when the last line of the screen is the prompt: only then the agent takes the message again
		o.wait(t, "the agent free again", func(_, screen string) bool {
			lines := strings.Split(strings.TrimRight(screen, " \n"), "\n")
			return len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == ">"
		})
	}
}

// wait waits until holds says so of the raw output and of the screen (the
// scrollback and the visible rows, one line per row, trailing blanks off).
func (o *outbuf) wait(t *testing.T, what string, holds func(raw, screen string) bool) {
	t.Helper()
	limit := clock.Real{}.NewTimer(90 * time.Second)
	defer limit.Stop()
	for {
		o.mu.Lock()
		raw, screen := o.b.String(), o.em.History(-1)
		if o.changed == nil {
			o.changed = make(chan struct{})
		}
		ch := o.changed
		o.mu.Unlock()
		if holds(raw, screen) {
			return
		}
		select {
		case <-ch:
		case <-t.Context().Done():
			t.Fatalf("output never held %s:\n%s\nscreen:\n%s", what, raw, screen)
		case <-limit.C():
			t.Fatalf("output did not hold %s in 90 s:\n%s\nscreen:\n%s", what, raw, screen)
		}
	}
}

// fullEngine is the engine side of a machine for these tests: the terminals
// of internal/terminal, the local channel on a real socket with the real
// identification of the caller, and the hook handler of the engine over the
// effective rules in a real machine database.
type fullEngine struct {
	t     *testing.T
	ctx   context.Context
	root  string
	sock  string
	mgr   *terminal.Manager
	db    *store.DB
	sys   terminal.System
	Rules *classes.Store
	Hook  *hook.Engine
}

// character is the terminal registry of the test: the one character, in
// its worktree (the work folder of the engine's root).
type character struct{ id, worktree string }

func (c character) Env(_ context.Context, id string) (classes.Env, error) {
	if id != c.id {
		return classes.Env{}, errors.New("unknown character")
	}
	return classes.Env{AgentID: id, Worktree: c.worktree, OS: runtime.GOOS, Normalize: providers.Options{}.NormalizePath}, nil
}

func newFullEngine(t *testing.T, agent string) *fullEngine {
	t.Helper()
	// Unix socket paths are short (about 100 bytes): a folder of its own.
	root, err := os.MkdirTemp("", "mh")
	if err != nil {
		t.Fatal(err)
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	ctx, cancel := clock.WithTimeout(t.Context(), clock.Real{}, 2*time.Minute)
	t.Cleanup(cancel)

	sys := terminal.NewSystem()
	db, err := machinedb.Open(ctx, filepath.Join(root, "maquina.db"), store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	spawner := &terminal.DirectSpawner{Suite: "full", System: sys}
	dir := localchan.NewDirectory(sys)
	mgr, err := terminal.NewManager(terminal.ManagerConfig{
		DB: db, System: sys, MachineID: "m1", SocketDir: filepath.Join(root, "s"), LogDir: filepath.Join(root, "l"),
		Spawner:          spawner,
		HandshakeTimeout: time.Minute, // a loaded CI machine can be slow to schedule the mestre-pty it just started
		PTYBinary:        func(string) (string, error) { return builds.pty, nil },
		PTYVersion:       ptyVersion,
		OnStarting:       dir.Starting, OnDiscarded: dir.Remove, OnEnded: dir.Remove,
	})
	if err != nil {
		t.Fatal(err)
	}
	rules := &classes.Store{DB: db, Clock: clock.Real{}, Agents: filepath.Join(root, "agentes")}
	e := &fullEngine{t: t, ctx: ctx, root: root, sock: filepath.Join(root, "cli.sock"), mgr: mgr, db: db, sys: sys, Rules: rules}
	e.Hook = &hook.Engine{Clock: clock.Real{}, Rules: rules, Ledger: rules, Agents: rules.Agents,
		Characters: character{id: agent, worktree: filepath.Join(root, "work")}}
	srv := localchan.NewServer(localchan.Config{
		Clock: clock.Real{},
		Identifier: localchan.NewSystemIdentifier(localchan.SystemConfig{
			Terminals: dir, Jobs: dir, System: sys, Mode: localchan.ModeTree,
		}),
		Local:    map[string]localchan.Handler{localchan.CmdHook: e.Hook},
		Instance: "test", EngineVersion: "test",
	})
	ln, err := localchan.Listen(e.sock, localchan.NoAccess{})
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan struct{})
	sctx, scancel := context.WithCancel(ctx)
	go func() { defer close(served); _ = srv.Serve(sctx, ln) }()
	t.Cleanup(func() {
		scancel()
		<-served
		recs, _ := mgr.Records(context.Background())
		for _, r := range recs {
			_ = spawner.KillCgroup(context.Background(), r.ID)
			if r.PTY.PID != 0 {
				_ = sys.KillTree(context.Background(), r.PTY, nil)
			}
		}
		_ = db.Close()
	})
	return e
}

// claude starts the fake agent as Claude Code in a terminal of the
// character, with the hook command on PreToolUse and the script, trusted in
// its folder, and returns the terminal and its screen once it is ready.
func (e *fullEngine) claude(id, agent, hookCommand, script string) (*terminal.Terminal, *outbuf, string) {
	e.t.Helper()
	work := filepath.Join(e.root, "work")
	cfg := filepath.Join(e.root, "cfg")
	hooks := fmt.Sprintf(`{"hooks": {"PreToolUse": [{"matcher": "*", "hooks": [{"type": "command", "command": %s, "timeout": 30, "onFailure": "block"}]}]}}`,
		quoteJSON(hookCommand))
	for rel, content := range map[string]string{
		"work/.git/HEAD":                   "ref: refs/heads/main\n",
		"work/.claude/settings.local.json": hooks,
		"cfg/.credentials.json":            "{}",
		"cfg/.claude.json":                 fmt.Sprintf(`{"numStartups": 3, "projects": {%s: {"hasTrustDialogAccepted": true}}}`, quoteJSON(filepath.ToSlash(work))),
		"script.yaml":                      script,
	} {
		p := filepath.Join(e.root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			e.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			e.t.Fatal(err)
		}
	}
	env := append(os.Environ(),
		localchan.EnvAgentID+"="+agent,
		localchan.EnvEndpoint+"="+localchan.EndpointURL(e.sock),
		localchan.EnvCLI+"="+builds.mestre,
		"HOME="+e.root, "CLAUDE_CONFIG_DIR="+cfg, "FAKEAGENT_SCRIPT="+filepath.Join(e.root, "script.yaml"),
		// The helper of CA-HOOK-07 is this test binary: built with -race it
		// would wait a second at each exit.
		"GORACE=atexit_sleep_ms=0",
	)
	term, err := e.mgr.Create(e.ctx, terminal.CreateSpec{
		ID: id, CharacterID: agent, Dir: work, Path: builds.fakeagent, Args: []string{"--provider", "claude"}, Env: env, WorkDir: work,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(term.Detach)
	rec, err := e.mgr.Record(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	c, err := terminal.Dial(e.ctx, clock.Real{}, rec.SocketPath, rec.Secret, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = c.Close() })
	out := newOutbuf(e.t)
	if _, err := c.Follow(e.ctx, 0, func(f ptywire.Frame) { _, _ = out.Write(f.Data) }); err != nil {
		e.t.Fatal(err)
	}
	out.waitFor(e.t, ">")
	return term, out, work
}

// say delivers a message to the agent the way the inbox does: typed by the
// adapter of the provider.
func say(t *testing.T, ctx context.Context, term *terminal.Terminal, text string) {
	t.Helper()
	b, err := providers.NewClaudeCode(providers.Options{}).TypeMessage(text)
	if err != nil {
		t.Fatal(err)
	}
	if err := term.Input(ctx, b); err != nil {
		t.Fatal(err)
	}
}

// quoteJSON is s as a JSON string.
func quoteJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// quoteArg quotes a path for the shell of the hook (sh -c, or cmd /s /c).
func quoteArg(s string) string { return `"` + s + `"` }
