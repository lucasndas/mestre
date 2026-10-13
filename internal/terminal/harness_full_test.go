//go:build full

package terminal_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/store/machine"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/internal/terminal/ptywire"
	"github.com/lucasndas/mestre/internal/testkit"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The full suite of the terminal starts real processes: the mestre-pty
// (built here, in the current version and in a "previous" one), the fake
// agent, and helper programs, which are this same test binary started with
// a first argument TestMain recognises. That keeps the tests free of shell
// tools, so they run on Linux and Windows alike.
const (
	helperTree    = "helper-tree"    // <pids file> <escape 0|1>: starts grandchildren, then waits
	helperSleeper = "helper-sleeper" // ignores SIGTERM and waits
	helperPrinter = "helper-printer" // <text>: prints it, then waits
	helperEngine  = "helper-engine"  // <config>: creates a terminal and waits to be killed
	helperLeaver  = "helper-leaver"  // <pids file>: leaves a process behind and exits on its own
	helperBreak   = "helper-break"   // <result file>: tries to create a process outside its Job Object (Windows)
)

// builds holds the binaries TestMain built.
var builds struct {
	dir        string
	pty        string // current mestre-pty
	ptyOld     string // the "previous version": protocol 1, version 0.9.0
	fakeagent  string
	curVersion string
}

const (
	versionCurrent  = "1.0.0"
	versionPrevious = "0.9.0"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case helperTree:
			os.Exit(runTree(os.Args[2:]))
		case helperSleeper:
			signal.Ignore(syscall.SIGTERM)
			block()
		case helperPrinter:
			fmt.Print(os.Args[2] + "\r\n")
			signal.Ignore(syscall.SIGTERM)
			block()
		case helperEngine:
			os.Exit(runEngine(os.Args[2]))
		case helperLeaver:
			os.Exit(runLeaver(os.Args[2]))
		case helperBreak:
			os.Exit(runBreak(os.Args[2]))
		}
	}
	if dir := os.Getenv(envBuilds); dir != "" {
		// A test run again in a delegated scope (delegated.Rerun): the
		// binaries of the run that started it, which also removes them.
		useBuilds(dir)
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "mtbin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	useBuilds(dir)
	for _, b := range [][]string{
		{"-o", builds.pty, "-ldflags", "-X main.version=" + versionCurrent, "github.com/lucasndas/mestre/cmd/mestre-pty"},
		{"-o", builds.ptyOld, "-tags", "ptylegacy", "-ldflags", "-X main.version=" + versionPrevious, "github.com/lucasndas/mestre/cmd/mestre-pty"},
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
	builds.pty = filepath.Join(dir, "pty", versionCurrent, "mestre-pty"+exe)
	builds.ptyOld = filepath.Join(dir, "pty", versionPrevious, "mestre-pty"+exe)
	builds.fakeagent = filepath.Join(dir, "fakeagent"+exe)
}

// runTree is the agent of the termination tests: it starts a grandchild
// that ignores SIGTERM and, when asked and where the system allows, one
// that leaves the session (what `setsid -f` does), writes their PIDs and
// reports READY. It ignores SIGTERM itself.
func runTree(args []string) int {
	signal.Ignore(syscall.SIGTERM)
	self, err := os.Executable()
	if err != nil {
		return 1
	}
	var pids []string
	spawn := func(attr *syscall.SysProcAttr) bool {
		c := exec.Command(self, helperSleeper)
		c.SysProcAttr = attr
		if c.Start() != nil {
			return false
		}
		pids = append(pids, strconv.Itoa(c.Process.Pid))
		return true
	}
	spawn(nil)
	if len(args) > 1 && args[1] == "1" {
		spawn(leaveSessionAttr())
	}
	if err := os.WriteFile(args[0], []byte(strings.Join(pids, "\n")), 0o600); err != nil {
		return 1
	}
	fmt.Print("READY\r\n")
	block()
	return 0
}

// runLeaver is an agent that ends on its own and leaves a process behind (what
// `nohup cmd &` does): the sleeper stays in the session of the agent, and in
// its cgroup or Job Object, when the agent exits.
func runLeaver(pidsFile string) int {
	self, err := os.Executable()
	if err != nil {
		return 1
	}
	c := exec.Command(self, helperSleeper)
	if c.Start() != nil {
		return 1
	}
	if err := os.WriteFile(pidsFile, []byte(strconv.Itoa(c.Process.Pid)), 0o600); err != nil {
		return 1
	}
	fmt.Print("READY\r\n")
	return 0
}

// endedSignals maps a terminal id to the channel closed when the Manager
// reports that its agent ended on its own (OnEnded).
var endedSignals sync.Map

// block waits for ever. It is not a bare select{}: on Windows a program with
// nothing else running dies of "all goroutines are asleep", and the helpers
// must stay alive until their terminal ends them. A pending timer keeps the
// runtime from calling it a deadlock.
func block() {
	<-clock.Real{}.NewTimer(1000 * time.Hour).C()
}

// harnessConfig is what the test and the helper engine process share.
type harnessConfig struct {
	Root, DB, Sockets, Logs string
	PTYBins                 map[string]string
	CgroupBase              string
	Version                 string
}

func buildManager(ctx context.Context, cfg harnessConfig) (*terminal.Manager, *store.DB, error) {
	db, err := machine.Open(ctx, cfg.DB, store.Options{})
	if err != nil {
		return nil, nil, err
	}
	sys := terminal.NewSystem()
	m, err := terminal.NewManager(terminal.ManagerConfig{
		DB: db, System: sys, MachineID: "m1", SocketDir: cfg.Sockets, LogDir: cfg.Logs,
		Spawner:    &terminal.DirectSpawner{Suite: "full", System: sys, CgroupBase: cfg.CgroupBase},
		PTYBinary:  func(v string) (string, error) { return cfg.PTYBins[v], nil },
		PTYVersion: cfg.Version,
		OnEnded: func(id string) {
			if c, ok := endedSignals.Load(id); ok {
				close(c.(chan struct{}))
			}
		},
	})
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	return m, db, nil
}

// runEngine is the engine of CA-TERM-08 as its own process: it creates the
// terminal, tells the test, and stays until it is killed.
func runEngine(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var req struct {
		Config harnessConfig
		Spec   terminal.CreateSpec
	}
	if err := json.Unmarshal(b, &req); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	m, _, err := buildManager(context.Background(), req.Config)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	t, err := m.Create(context.Background(), req.Spec)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("READY %d %d\n", t.PTY().PID, t.AgentPID())
	// Stay until the test kills this process (or its stdin closes).
	buf := make([]byte, 1)
	for {
		if _, err := os.Stdin.Read(buf); err != nil {
			block()
		}
	}
}

// harness is one machine of the test: its folders, registry and engine.
type harness struct {
	t   *testing.T
	ctx context.Context // ends with the test or after a minute and a half, whichever is first
	cfg harnessConfig
	mgr *terminal.Manager
	db  *store.DB
	sys terminal.System

	tracked []*terminal.Terminal
}

type harnessOptions struct {
	// version of the mestre-pty new terminals get (default the current one).
	version string
}

func newHarness(t *testing.T, o harnessOptions) *harness {
	t.Helper()
	// Unix socket paths are short (about 100 bytes): a folder of its own.
	root, err := os.MkdirTemp("", "mt")
	if err != nil {
		t.Fatal(err)
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	cfg := harnessConfig{
		Root: root, DB: filepath.Join(root, "maquina.db"), Sockets: filepath.Join(root, "s"), Logs: filepath.Join(root, "l"),
		PTYBins: map[string]string{versionCurrent: builds.pty, versionPrevious: builds.ptyOld},
		Version: versionCurrent,
	}
	if o.version != "" {
		cfg.Version = o.version
	}
	if base, err := terminal.DelegatedCgroup(); err == nil {
		cfg.CgroupBase = base
	}
	ctx, cancel := clock.WithTimeout(t.Context(), clock.Real{}, 90*time.Second)
	t.Cleanup(cancel)
	h := &harness{t: t, ctx: ctx, cfg: cfg, sys: terminal.NewSystem()}
	h.open()
	t.Cleanup(h.cleanup)
	return h
}

// open (re)opens the engine side on the same folders and registry: what a
// restarted engine does.
func (h *harness) open() {
	h.t.Helper()
	m, db, err := buildManager(h.ctx, h.cfg)
	if err != nil {
		h.t.Fatal(err)
	}
	h.mgr, h.db = m, db
}

// track registers a terminal for the cleanup.
func (h *harness) track(ts ...*terminal.Terminal) {
	h.tracked = append(h.tracked, ts...)
}

// cleanup ends every mestre-pty the test started, whatever happened.
func (h *harness) cleanup() {
	ctx := context.Background()
	spawner := &terminal.DirectSpawner{CgroupBase: h.cfg.CgroupBase}
	recs, _ := h.mgr.Records(ctx)
	for _, r := range recs {
		_ = spawner.KillCgroup(ctx, r.ID)
		if r.PTY.PID != 0 {
			_ = h.sys.KillTree(ctx, r.PTY, nil)
		}
	}
	for _, t := range h.tracked {
		t.Detach()
		_ = h.sys.KillTree(ctx, t.PTY(), []int{t.AgentPID()})
	}
	_ = h.db.Close()
}

// agentSpec is the fake agent as Claude Code, already trusting its folder,
// so the start screen is the input prompt.
func (h *harness) agentSpec(id string) terminal.CreateSpec {
	h.t.Helper()
	work, cfg := filepath.Join(h.cfg.Root, "work-"+id), filepath.Join(h.cfg.Root, "cfg-"+id)
	testkit.WriteFiles(h.t, h.cfg.Root, map[string]string{
		"work-" + id + "/.git/HEAD":        "ref: refs/heads/main\n",
		"cfg-" + id + "/.credentials.json": "{}",
		"cfg-" + id + "/.claude.json": fmt.Sprintf(`{"numStartups": 3, "projects": {%q: {"hasTrustDialogAccepted": true}}}`,
			work),
	})
	return terminal.CreateSpec{
		ID: id, CharacterID: "c-" + id, Dir: work,
		Path: builds.fakeagent, Args: []string{"--provider", "claude"}, WorkDir: work,
		Env: append(os.Environ(), "HOME="+h.cfg.Root, "CLAUDE_CONFIG_DIR="+cfg, "MESTRE_AGENT_ID=a-"+id),
	}
}

// selfSpec runs a helper mode of this test binary as the agent.
func (h *harness) selfSpec(id string, args ...string) terminal.CreateSpec {
	h.t.Helper()
	self, err := os.Executable()
	if err != nil {
		h.t.Fatal(err)
	}
	return terminal.CreateSpec{
		ID: id, CharacterID: "c-" + id, Dir: h.cfg.Root, Path: self, Args: args, WorkDir: h.cfg.Root,
		Env: os.Environ(),
	}
}

// create makes a terminal and registers it for the cleanup.
func (h *harness) create(spec terminal.CreateSpec) *terminal.Terminal {
	h.t.Helper()
	t, err := h.mgr.Create(h.ctx, spec)
	if err != nil {
		h.t.Fatal(err)
	}
	h.track(t)
	return t
}

// observe opens another connection to the terminal and collects all of its
// output, from the start of the buffer.
func (h *harness) observe(rec terminal.Record) (*syncBuffer, *terminal.Client) {
	h.t.Helper()
	c, err := terminal.Dial(h.ctx, clock.Real{}, rec.SocketPath, rec.Secret, 0)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = c.Close() })
	buf := &syncBuffer{}
	if _, err := c.Follow(h.ctx, 0, func(f ptywire.Frame) { _, _ = buf.Write(f.Data) }); err != nil {
		h.t.Fatal(err)
	}
	return buf, c
}

func (h *harness) record(id string) terminal.Record {
	h.t.Helper()
	rec, err := h.mgr.Record(h.ctx, id)
	if err != nil {
		h.t.Fatal(err)
	}
	return rec
}

// gone fails the test unless the process is gone by the time it returns:
// it waits on the process itself, not on a timer.
func (h *harness) gone(pid int, what string) {
	h.t.Helper()
	if err := h.sys.WaitExit(h.ctx, pid); err != nil {
		h.t.Fatalf("%s (PID %d) still alive: %v", what, pid, err)
	}
}
