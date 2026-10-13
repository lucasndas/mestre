//go:build full

package localchan_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/store/machine"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/internal/terminal/ptywire"
	"github.com/lucasndas/mestre/internal/testkit"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The full suite of the local channel starts real processes: the mestre-pty,
// the CLI `mestre` and the fake agent (built here), and helper programs,
// which are this same test binary started with a first argument TestMain
// recognises. That keeps the tests free of shell tools, so they run on
// Linux and Windows alike (the Linux orphans, which are about the shell,
// are the exception and say so).
const (
	helperCaller = "helper-caller" // <mestre> <base dir> <modes>: calls `mestre list` the ways of the modes, then waits
	helperOrphan = "helper-orphan" // <mestre>: calls `mestre list` and ends (started by `cmd /c start /b`)
	helperScope  = "helper-scope"  // prints READY and its cgroup, then waits (run under systemd-run --scope)
)

var builds struct {
	dir       string
	pty       string
	mestre    string
	fakeagent string
}

const ptyVersion = "1.0.0"

func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case helperCaller:
			os.Exit(runCaller(os.Args[2:]))
		case helperOrphan:
			os.Exit(runOrphan(os.Args[2:]))
		case helperScope:
			fmt.Printf("READY %s\n", ownCgroup())
			block()
		}
	}
	if dir := os.Getenv(envBuilds); dir != "" {
		// A run of the same test binary in a systemd scope: the binaries of
		// the run that started it.
		useBuilds(dir)
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "mlbin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	useBuilds(dir)
	for _, b := range [][]string{
		{"-o", builds.pty, "-ldflags", "-X main.version=" + ptyVersion, "github.com/lucasndas/mestre/cmd/mestre-pty"},
		{"-o", builds.mestre, "github.com/lucasndas/mestre/cmd/mestre"},
		{"-o", builds.fakeagent, "github.com/lucasndas/mestre/cmd/fakeagent"},
	} {
		if out, err := exec.Command("go", append([]string{"build"}, b...)...).CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "go build %v: %v\n%s", b, err, out)
			os.Exit(1)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// envBuilds names the folder of the binaries of the run that started this one.
const envBuilds = "MESTRE_TEST_BUILDS"

func useBuilds(dir string) {
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	builds.dir = dir
	builds.pty = filepath.Join(dir, "pty", ptyVersion, "mestre-pty"+exe)
	builds.mestre = filepath.Join(dir, "mestre"+exe)
	builds.fakeagent = filepath.Join(dir, "fakeagent"+exe)
}

// block waits for ever. It is not a bare select{}: on Windows a program
// with nothing else running dies of "all goroutines are asleep", and the
// helpers must stay alive until their terminal ends them. A pending timer
// keeps the runtime from calling it a deadlock.
func block() {
	<-clock.Real{}.NewTimer(1000 * time.Hour).C()
}

func ownCgroup() string {
	b, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if p, ok := strings.CutPrefix(l, "0::"); ok {
			return p
		}
	}
	return ""
}

// runCaller is the agent of the identification tests: it calls `mestre list`
// the ways the modes say, each from its own folder (<base>/<mode>) so the
// engine can tell the calls apart, and then waits to be ended with its
// terminal. The commands get no stdout and no stderr of the test: a child
// that held them would keep the test from ending.
func runCaller(args []string) int {
	mestre, base, modes := args[0], args[1], strings.Split(args[2], ",")
	self, err := os.Executable()
	if err != nil {
		return 1
	}
	for _, mode := range modes {
		var c *exec.Cmd
		switch mode {
		case "direct":
			c = exec.Command(mestre, "list")
		case "cmd":
			c = exec.Command("cmd", "/c", mestre, "list")
		case "powershell":
			c = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", "& '"+mestre+"' list")
		case "orphan":
			// start returns at once: the orphan's parent, cmd, is gone by the
			// time it calls.
			c = exec.Command("cmd", "/c", "start", "", "/b", self, helperOrphan, mestre)
		default:
			fmt.Printf("unknown mode %q\r\n", mode)
			return 1
		}
		c.Dir = filepath.Join(base, mode)
		if err := c.Run(); err != nil {
			fmt.Printf("%s: %v\r\n", mode, err)
		}
	}
	fmt.Print("DONE\r\n")
	block()
	return 0
}

func runOrphan(args []string) int {
	if err := exec.Command(args[0], "list").Run(); err != nil {
		fmt.Printf("orphan: %v\r\n", err)
		return 1
	}
	return 0
}

// outbuf collects the output of a terminal and lets a test wait for a text
// without polling.
type outbuf struct {
	mu      sync.Mutex
	b       bytes.Buffer
	changed chan struct{}
}

func (o *outbuf) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.changed != nil {
		close(o.changed)
		o.changed = nil
	}
	return o.b.Write(p)
}

func (o *outbuf) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.String()
}

func (o *outbuf) waitFor(t *testing.T, sub string) {
	t.Helper()
	limit := clock.Real{}.NewTimer(time.Minute)
	defer limit.Stop()
	for {
		o.mu.Lock()
		text := o.b.String()
		if o.changed == nil {
			o.changed = make(chan struct{})
		}
		ch := o.changed
		o.mu.Unlock()
		if strings.Contains(text, sub) {
			return
		}
		select {
		case <-ch:
		case <-t.Context().Done():
			t.Fatalf("output never held %q:\n%s", sub, text)
		case <-limit.C():
			t.Fatalf("output did not hold %q in a minute:\n%s", sub, text)
		}
	}
}

// callWait is how long a test waits for a call (or a refusal) it expects. The
// wait ends the moment it arrives, so the bound only matters for a failure,
// and it is generous: the calls of CA-LOCAL-13 go through cmd and Windows
// PowerShell, whose cold start on a loaded CI runner took more than 20 s once
// (the first two calls of the same terminal were served 8 s into the test).
const callWait = 90 * time.Second

// call is what the `list` handler of the engine saw.
type call struct {
	Cwd    string
	Caller localchan.Caller
}

// engineOptions configure a fullEngine.
type engineOptions struct {
	// cgroup is the delegated cgroup the terminals are created in, as
	// /proc/<pid>/cgroup shows it (Linux); empty means none.
	cgroup string
	mode   localchan.Mode
}

// fullEngine is what the engine of a machine is for these tests: the
// terminals of internal/terminal, the local channel served on a real
// socket, and the registry of both. Everything it starts ends with the test.
type fullEngine struct {
	t      *testing.T
	ctx    context.Context
	root   string
	Sock   string
	mgr    *terminal.Manager
	db     *store.DB
	dir    *localchan.Directory
	sys    terminal.System
	opts   engineOptions
	Calls  chan call
	Audits chan localchan.Audit
}

func newFullEngine(t *testing.T, o engineOptions) *fullEngine {
	t.Helper()
	// Unix socket paths are short (about 100 bytes): a folder of its own.
	root, err := os.MkdirTemp("", "ml")
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
	cgBase := ""
	if o.cgroup != "" {
		cgBase = filepath.Join("/sys/fs/cgroup", o.cgroup)
	}
	db, err := machine.Open(ctx, filepath.Join(root, "maquina.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	spawner := &terminal.DirectSpawner{Suite: "full", System: sys, CgroupBase: cgBase}
	dir := localchan.NewDirectory(sys)
	mgr, err := terminal.NewManager(terminal.ManagerConfig{
		DB: db, System: sys, MachineID: "m1", SocketDir: filepath.Join(root, "s"), LogDir: filepath.Join(root, "l"),
		Spawner:    spawner,
		PTYBinary:  func(string) (string, error) { return builds.pty, nil },
		PTYVersion: ptyVersion,
		// The terminal is known to the local channel before its agent
		// starts: an agent can call in its first instant.
		OnStarting: dir.Starting, OnDiscarded: dir.Remove,
	})
	if err != nil {
		t.Fatal(err)
	}
	e := &fullEngine{
		t: t, ctx: ctx, root: root, Sock: filepath.Join(root, "cli.sock"), mgr: mgr, db: db, sys: sys, opts: o,
		dir: dir, Calls: make(chan call, 32), Audits: make(chan localchan.Audit, 32),
	}
	fwd := localchan.NewLocalForwarder()
	fwd.Handle("list", localchan.HandlerFunc(func(_ context.Context, c *localchan.Call) localchan.Response {
		e.Calls <- call{Cwd: c.Request.Cwd, Caller: c.Caller}
		return localchan.OK(map[string]any{"text": "the list", "you": c.Caller.AgentID})
	}))
	srv := localchan.NewServer(localchan.Config{
		Clock: clock.Real{},
		Identifier: localchan.NewSystemIdentifier(localchan.SystemConfig{
			Terminals: e.dir, Jobs: e.dir, System: sys, Mode: o.mode, CgroupBase: o.cgroup,
		}),
		Forwarder: fwd, Instance: "test", EngineVersion: "test",
		OnAudit: func(a localchan.Audit) {
			select {
			case e.Audits <- a:
			default:
			}
		},
	})
	ln, err := localchan.Listen(e.Sock, localchan.NoAccess{})
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan struct{})
	sctx, scancel := context.WithCancel(ctx)
	go func() { defer close(served); _ = srv.Serve(sctx, ln) }()
	t.Cleanup(func() { scancel(); <-served; e.cleanup(spawner) })
	return e
}

// cleanup ends every mestre-pty and every process of the terminals,
// whatever happened.
func (e *fullEngine) cleanup(spawner *terminal.DirectSpawner) {
	ctx := context.Background()
	recs, _ := e.mgr.Records(ctx)
	for _, r := range recs {
		_ = spawner.KillCgroup(ctx, r.ID)
		if r.PTY.PID != 0 {
			_ = e.sys.KillTree(ctx, r.PTY, nil)
		}
	}
	_ = e.db.Close()
}

// env is the environment the engine gives a character.
func (e *fullEngine) env(agent string, extra ...string) []string {
	env := append(os.Environ(),
		localchan.EnvAgentID+"="+agent,
		localchan.EnvEndpoint+"="+localchan.EndpointURL(e.Sock),
		localchan.EnvCLI+"="+builds.mestre,
	)
	return append(env, extra...)
}

// spawn creates a terminal of the character; the local channel knows it
// before the agent starts (OnStarting), as the engine arranges.
func (e *fullEngine) spawn(id, agent, path string, args []string, env []string, workDir string) *terminal.Terminal {
	e.t.Helper()
	term, err := e.mgr.Create(e.ctx, terminal.CreateSpec{
		ID: id, CharacterID: agent, Dir: workDir, Path: path, Args: args, Env: env, WorkDir: workDir,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(term.Detach)
	return term
}

// watch follows the output of a terminal from another connection.
func (e *fullEngine) watch(id string) *outbuf {
	e.t.Helper()
	rec, err := e.mgr.Record(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	c, err := terminal.Dial(e.ctx, clock.Real{}, rec.SocketPath, rec.Secret, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = c.Close() })
	out := &outbuf{}
	if _, err := c.Follow(e.ctx, 0, func(f ptywire.Frame) { _, _ = out.Write(f.Data) }); err != nil {
		e.t.Fatal(err)
	}
	return out
}

// nextCall waits for the next call that reached the handler.
func (e *fullEngine) nextCall(what string) call {
	e.t.Helper()
	limit := clock.Real{}.NewTimer(callWait)
	defer limit.Stop()
	select {
	case c := <-e.Calls:
		return c
	case <-limit.C():
		e.t.Fatalf("no call in 90 s: %s\naudits: %s", what, e.drainAudits())
	case <-e.ctx.Done():
		e.t.Fatalf("the test ended before a call: %s", what)
	}
	return call{}
}

func (e *fullEngine) drainAudits() string {
	var b strings.Builder
	for {
		select {
		case a := <-e.Audits:
			fmt.Fprintf(&b, "\n  %+v", a)
		default:
			return b.String()
		}
	}
}

// nextAudit waits for the next audit record of a refusal.
func (e *fullEngine) nextRefusal(what string) localchan.Audit {
	e.t.Helper()
	limit := clock.Real{}.NewTimer(callWait)
	defer limit.Stop()
	for {
		select {
		case a := <-e.Audits:
			if a.Code == localchan.CodeNotIdentified {
				return a
			}
		case <-limit.C():
			e.t.Fatalf("no refusal in 90 s: %s", what)
		case <-e.ctx.Done():
			e.t.Fatalf("the test ended before a refusal: %s", what)
		}
	}
}

// noMoreCalls fails if a call reached the handler that the test did not
// expect: by the time it runs, every call it started has been answered.
func (e *fullEngine) noMoreCalls() {
	e.t.Helper()
	select {
	case c := <-e.Calls:
		e.t.Fatalf("an unexpected call reached the handler: %+v", c)
	default:
	}
}

// outside runs `mestre list` from the test process, which is in no
// terminal, with the id of a character.
func (e *fullEngine) outside(agent string) (exit int, stdout string) {
	e.t.Helper()
	cmd := exec.CommandContext(e.ctx, builds.mestre, "list", "--json")
	cmd.Env = e.env(agent)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), out.String()
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return 0, out.String()
}

// scriptFile writes the script of the fake agent: any prompt makes it run
// the commands of the lines.
func scriptFile(t *testing.T, dir string, commands ...string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("prompts:\n  - match: \".*\"\n    steps:\n")
	for _, c := range commands {
		fmt.Fprintf(&b, "      - run: '%s'\n", strings.ReplaceAll(c, "'", "''"))
	}
	testkit.WriteFiles(t, dir, map[string]string{"script.yaml": b.String()})
	return filepath.Join(dir, "script.yaml")
}
