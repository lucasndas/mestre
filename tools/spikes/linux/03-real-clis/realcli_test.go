//go:build realcli && linux

// Package realcli is a manual check, outside every suite, that the production
// mestre-pty runs the real provider CLIs (claude, codex, agy) as the account
// of the agents on a Linux development machine
// (docs/desenvolvimento/ambiente.md#conta-mestre-agente-nesta-máquina-linux).
//
// It plays the engine: the production internal/terminal API with the direct
// spawner starts the mestre-pty and, inside it, each CLI with the environment
// of its spike profile. It is meant to run entirely as mestre-agente:
//
//	go test -c -tags realcli -o realcli.test ./tools/spikes/linux/03-real-clis
//	sudo -u mestre-agente env MESTRE_REAL_CLI=1 ./realcli.test -test.v -test.timeout 20m \
//	    -root <scratch> -pty <mestre-pty> -work <scratch>/work
//
// It refuses to run unless MESTRE_REAL_CLI=1, because it uses real logins and
// spends a few tokens per CLI. It never answers a trust screen (that would
// write to the profile): it records it. Screens are masked before printing.
package realcli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/store/machine"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/pkg/clock"
)

var (
	flagRoot     = flag.String("root", "", "scratch folder (sockets, logs, database); owned by the account running this")
	flagPTY      = flag.String("pty", "", "path of the mestre-pty to test")
	flagWork     = flag.String("work", "", "folder the CLIs start in (default <root>/work)")
	flagOnly     = flag.String("only", "", "comma list of CLIs to run (claude, codex, agy); default all")
	flagBin      = flag.String("bin", "/opt/mestre-clis/bin", "folder of the CLI binaries")
	flagProfiles = flag.String("profiles", "/var/lib/mestre/contas", "folder of the account profiles")
	flagRestart  = flag.String("restart-cli", "claude", "CLI used by the engine-restart check")
	flagDebug    = flag.Bool("debug", false, "print every change of the screen while waiting")
	flagAnswer   = flag.String("prompt", "responda apenas: ok", "the trivial prompt typed into each CLI")
)

const helperEngine = "helper-engine"

func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == helperEngine {
		os.Exit(runEngine(os.Args[2]))
	}
	if os.Getenv("MESTRE_REAL_CLI") != "1" {
		fmt.Fprintln(os.Stderr, "realcli: refusing to run: it uses real logins; set MESTRE_REAL_CLI=1")
		os.Exit(2)
	}
	os.Exit(m.Run())
}

// ---- CLIs ---------------------------------------------------------------

type cliSpec struct {
	Name string
	Bin  string
	Args []string
	// Env is the whole environment of the CLI: nothing is inherited.
	Env []string
	// Ready matches the input prompt of a logged-in start screen.
	Ready *regexp.Regexp
}

var (
	loginRe = regexp.MustCompile(`(?i)(select login method|paste code here|sign in with|log in with|please run /login|not logged in|login required|choose the text style|let's get started|authenticate|open this url|device code)`)
	trustRe = regexp.MustCompile(`(?i)(do you trust|trust the contents|trust this folder|yes, i trust|accessing workspace)`)
)

func specs() []cliSpec {
	home := func(p string) string { return filepath.Join(*flagProfiles, p) }
	base := func(h string) []string {
		return []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + h, "TERM=xterm-256color", "LANG=C.UTF-8"}
	}
	all := []cliSpec{
		{
			Name: "claude", Bin: filepath.Join(*flagBin, "claude"),
			Args:  []string{"--permission-mode", "default"},
			Env:   append(base(home("spike-claude")), "CLAUDE_CONFIG_DIR="+home("spike-claude")),
			Ready: regexp.MustCompile(`(?m)^[│| ]*[❯>][ \x{a0}]`),
		},
		{
			Name: "codex", Bin: filepath.Join(*flagBin, "codex"),
			Args:  []string{"--no-daemon", "-s", "workspace-write", "-a", "on-request"},
			Env:   append(base(home("spike-codex")), "CODEX_HOME="+home("spike-codex")),
			Ready: regexp.MustCompile(`(?m)^[│| ]*[›>][ \x{a0}]`),
		},
		{
			Name: "agy", Bin: filepath.Join(*flagBin, "agy"),
			Env:   base(home("spike-agy")),
			Ready: regexp.MustCompile(`(?m)^[│| ]*[❯›>]([ \x{a0}]|$)`),
		},
	}
	if *flagOnly == "" {
		return all
	}
	var out []cliSpec
	for _, s := range all {
		for _, n := range strings.Split(*flagOnly, ",") {
			if strings.TrimSpace(n) == s.Name {
				out = append(out, s)
			}
		}
	}
	return out
}

func workDir() string {
	if *flagWork != "" {
		return *flagWork
	}
	return filepath.Join(*flagRoot, "work")
}

// ---- masking -------------------------------------------------------------

var (
	emailRe = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	tokenRe = regexp.MustCompile(`[A-Za-z0-9_\-]{40,}`)
	nameRe  = regexp.MustCompile(`(?i)(welcome back|olá|hello|hi),? [^!\n│]+`)
)

// mask removes what a screen may show about the account.
func mask(s string) string {
	s = emailRe.ReplaceAllString(s, "<email>")
	s = tokenRe.ReplaceAllString(s, "<token>")
	return nameRe.ReplaceAllString(s, "$1 <name>")
}

// shown is a masked screen without trailing blanks, for the log.
func shown(s string) string {
	lines := strings.Split(mask(s), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	var kept []string // runs of blank lines (the empty middle of a tall screen) collapse to one
	for _, l := range lines {
		if l == "" && len(kept) > 0 && kept[len(kept)-1] == "" {
			continue
		}
		kept = append(kept, l)
	}
	return "    | " + strings.Join(kept, "\n    | ")
}

// ---- machine -------------------------------------------------------------

type config struct {
	Root, DB, Sockets, Logs, PTY string
	CgroupBase                   string
}

func newConfig(t testing.TB) config {
	t.Helper()
	if *flagRoot == "" || *flagPTY == "" {
		t.Fatal("-root and -pty are required")
	}
	root, err := filepath.EvalSymlinks(*flagRoot)
	if err != nil {
		t.Fatal(err)
	}
	c := config{
		Root: root, DB: filepath.Join(root, "maquina.db"), Sockets: filepath.Join(root, "s"), Logs: filepath.Join(root, "l"),
		PTY: *flagPTY,
	}
	if base, err := terminal.DelegatedCgroup(); err == nil {
		c.CgroupBase = base
	}
	return c
}

// engine is one incarnation of the engine side: a Manager on the registry.
type engine struct {
	mgr   *terminal.Manager
	db    *store.DB
	ended chan string
	stab  chan terminal.Event
}

func buildEngine(ctx context.Context, cfg config) (*engine, error) {
	db, err := machine.Open(ctx, cfg.DB, store.Options{})
	if err != nil {
		return nil, err
	}
	e := &engine{db: db, ended: make(chan string, 8), stab: make(chan terminal.Event, 64)}
	sys := terminal.NewSystem()
	lf, err := os.OpenFile(filepath.Join(cfg.Root, "engine.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	m, err := terminal.NewManager(terminal.ManagerConfig{
		DB: db, System: sys, MachineID: "realcli", SocketDir: cfg.Sockets, LogDir: cfg.Logs,
		Spawner:    &terminal.DirectSpawner{System: sys, CgroupBase: cfg.CgroupBase},
		PTYBinary:  func(string) (string, error) { return cfg.PTY, nil },
		PTYVersion: "1.0.0",
		Masker:     logging.NewMasker(),
		Logger:     slog.New(slog.NewTextHandler(lf, nil)),
		OnEnded:    func(id string) { e.ended <- id },
		OnEvent: func(ev terminal.Event) {
			if ev.Kind == terminal.EventStable {
				select {
				case e.stab <- ev:
				default:
				}
			}
		},
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	e.mgr = m
	return e, nil
}

func createSpec(cfg config, s cliSpec, id string) terminal.CreateSpec {
	return terminal.CreateSpec{
		ID: id, CharacterID: "c-" + id, Dir: workDir(),
		Path: s.Bin, Args: s.Args, Env: s.Env, WorkDir: workDir(),
	}
}

// ---- engine as its own process (engine restart) ---------------------------

// runEngine creates the terminal, tells the caller and waits to be killed.
func runEngine(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var req struct {
		Config config
		Spec   terminal.CreateSpec
	}
	if err := json.Unmarshal(b, &req); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx, cancel := clock.WithTimeout(context.Background(), clock.Real{}, 2*time.Minute)
	defer cancel()
	e, err := buildEngine(ctx, req.Config)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	tm, err := e.mgr.Create(ctx, req.Spec)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("READY %d %d\n", tm.PTY().PID, tm.AgentPID())
	// Stay until killed; a timer keeps the runtime from calling it a deadlock.
	<-clock.Real{}.NewTimer(time.Hour).C()
	return 0
}

// ---- screen helpers ----------------------------------------------------------

var errTimeout = errors.New("timeout")

// placeholderRe is the rotating suggestion in an empty prompt ("Try ..."),
// which changes by itself and must not count as the screen changing.
var placeholderRe = regexp.MustCompile(`Try "[^"\n]*"?`)

func norm(s string) string { return placeholderRe.ReplaceAllString(s, `Try "…"`) }

// waitScreen polls the screen of tm until cond holds on a screen that did not
// change for stableFor, or d passes, or the agent ends.
func waitScreen(ctx context.Context, tm *terminal.Terminal, d, stableFor time.Duration, cond func(string) bool) (string, error) {
	clk := clock.Real{}
	ctx, cancel := clock.WithTimeout(ctx, clk, d)
	defer cancel()
	var last string
	changed := clk.Now()
	for {
		sc, err := tm.Screen(ctx, 0)
		if err == nil {
			if norm(sc.Text) != norm(last) {
				changed = clk.Now()
				if *flagDebug {
					fmt.Fprintf(os.Stderr, "[screen changed %s]\n%s\n", changed.Format("15:04:05.000"), shown(sc.Text))
				}
			}
			last = sc.Text
			if cond(last) && clk.Now().Sub(changed) >= stableFor {
				return last, nil
			}
		}
		select {
		case <-tm.Exited():
			return last, errors.New("the agent exited")
		default:
		}
		if err := clock.Sleep(ctx, clk, 250*time.Millisecond); err != nil {
			return last, fmt.Errorf("%w after %v", errTimeout, d)
		}
	}
}

// ---- /proc facts ----------------------------------------------------------------

type procInfo struct {
	UID, PPID, SID int
	Comm           string
}

func readProc(pid int) (procInfo, error) {
	var p procInfo
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return p, err
	}
	for _, l := range strings.Split(string(b), "\n") {
		switch {
		case strings.HasPrefix(l, "Uid:"):
			p.UID, _ = strconv.Atoi(strings.Fields(l)[1]) // real uid
		case strings.HasPrefix(l, "Name:"):
			p.Comm = strings.TrimSpace(strings.TrimPrefix(l, "Name:"))
		}
	}
	st, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return p, err
	}
	s := string(st)
	f := strings.Fields(s[strings.LastIndexByte(s, ')')+1:]) // state ppid pgrp session ...
	if len(f) < 4 {
		return p, fmt.Errorf("short stat of %d", pid)
	}
	p.PPID, _ = strconv.Atoi(f[1])
	p.SID, _ = strconv.Atoi(f[3])
	return p, nil
}

// processesOf lists the PIDs (other than self) running with uid.
func processesOf(uid int) []int {
	ents, _ := os.ReadDir("/proc")
	var out []int
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		if p, err := readProc(pid); err == nil && p.UID == uid {
			out = append(out, pid)
		}
	}
	return out
}

func names(pids []int) string {
	var s []string
	for _, p := range pids {
		if i, err := readProc(p); err == nil {
			s = append(s, fmt.Sprintf("%d(%s)", p, i.Comm))
		}
	}
	return strings.Join(s, " ")
}

// checkProcesses verifies the claims of the document about the process tree.
func checkProcesses(t *testing.T, tm *terminal.Terminal) {
	t.Helper()
	me := os.Getuid()
	pty, agent := tm.PTY().PID, tm.AgentPID()
	pi, err := readProc(pty)
	if err != nil {
		t.Errorf("mestre-pty %d: %v", pty, err)
		return
	}
	ai, err := readProc(agent)
	if err != nil {
		t.Errorf("agent %d: %v", agent, err)
		return
	}
	t.Logf("mestre-pty pid=%d uid=%d ppid=%d sid=%d (%s)", pty, pi.UID, pi.PPID, pi.SID, pi.Comm)
	t.Logf("agent      pid=%d uid=%d ppid=%d sid=%d (%s)", agent, ai.UID, ai.PPID, ai.SID, ai.Comm)
	if pi.UID != me || ai.UID != me {
		t.Errorf("uids: mestre-pty %d, agent %d, want %d", pi.UID, ai.UID, me)
	}
	if ai.PPID != pty {
		t.Errorf("the agent is not a child of the mestre-pty: ppid %d, mestre-pty %d", ai.PPID, pty)
	}
	if ai.SID != agent || ai.SID == pi.SID {
		t.Errorf("the agent is not the leader of a new session: sid %d (mestre-pty sid %d, agent pid %d)", ai.SID, pi.SID, agent)
	}
	// dumpable 0 on the mestre-pty: no other process of the same account reads
	// its environment, its memory or attaches to it. This process is one.
	_, err = os.ReadFile("/proc/" + strconv.Itoa(pty) + "/environ")
	if !errors.Is(err, os.ErrPermission) {
		t.Errorf("reading /proc/%d/environ of the mestre-pty: got %v, want permission denied", pty, err)
	} else {
		t.Logf("read /proc/%d/environ of the mestre-pty: %v (denied, as required)", pty, err)
	}
	if _, err := os.Open("/proc/" + strconv.Itoa(pty) + "/mem"); !errors.Is(err, os.ErrPermission) {
		t.Errorf("opening /proc/%d/mem of the mestre-pty: got %v, want permission denied", pty, err)
	}
	if err := ptraceProbe(pty); err == nil {
		t.Errorf("ptrace attach to the mestre-pty %d succeeded; it must be denied", pty)
	} else {
		t.Logf("ptrace attach to the mestre-pty: %v (denied, as required)", err)
	}
	// Control: the agent itself is an ordinary dumpable process.
	if _, err := os.ReadFile("/proc/" + strconv.Itoa(agent) + "/environ"); err != nil {
		t.Logf("control: /proc/%d/environ of the agent is not readable either: %v", agent, err)
	} else {
		t.Logf("control: /proc/%d/environ of the agent is readable (so the denial above is the mestre-pty's own)", agent)
	}
}

// ptraceProbe tries to attach to pid; on success it detaches at once.
func ptraceProbe(pid int) error {
	errc := make(chan error, 1)
	go func() {
		runtime.LockOSThread() // ptrace requests belong to one thread
		err := syscall.PtraceAttach(pid)
		if err == nil {
			var ws syscall.WaitStatus
			_, _ = syscall.Wait4(pid, &ws, syscall.WALL, nil)
			_ = syscall.PtraceDetach(pid)
		}
		errc <- err
	}()
	return <-errc
}

// ---- the check of one CLI ------------------------------------------------------

func TestVersions(t *testing.T) {
	for _, s := range specs() {
		ctx, cancel := clock.WithTimeout(t.Context(), clock.Real{}, 60*time.Second)
		cmd := exec.CommandContext(ctx, s.Bin, "--version")
		cmd.Env = s.Env
		cmd.Dir = workDir()
		out, err := cmd.CombinedOutput()
		cancel()
		t.Logf("%s --version (uid %d): %s", s.Name, os.Getuid(), strings.TrimSpace(mask(string(out))))
		if err != nil {
			t.Errorf("%s --version: %v", s.Name, err)
		}
	}
}

func TestRealCLIs(t *testing.T) {
	cfg := newConfig(t)
	for _, s := range specs() {
		// mestre:allow parallel: real logins in turn; the leftover-process check needs the account to itself
		t.Run(s.Name, func(t *testing.T) { checkCLI(t, cfg, s) })
	}
}

// startAndWaitReady creates the terminal and waits for the start screen.
func startAndWaitReady(ctx context.Context, t *testing.T, e *engine, cfg config, s cliSpec, id string) (*terminal.Terminal, string, bool) {
	t.Helper()
	tm, err := e.mgr.Create(ctx, createSpec(cfg, s, id))
	if err != nil {
		t.Fatalf("create the terminal: %v", err)
	}
	t.Cleanup(func() { cleanupTerminal(tm) })
	t.Logf("created terminal %s: mestre-pty %d, agent %d, mestre-pty version %s", id, tm.PTY().PID, tm.AgentPID(), tm.PTYVersion())
	scr, err := waitScreen(ctx, tm, 90*time.Second, 2*time.Second, func(s string) bool {
		return strings.TrimSpace(s) != "" && (cliReady(s) || loginRe.MatchString(s) || trustRe.MatchString(s))
	})
	t.Logf("start screen (masked):\n%s", shown(scr))
	if err != nil {
		t.Errorf("start screen: %v", err)
		return tm, scr, false
	}
	if trustRe.MatchString(scr) {
		t.Errorf("TRUST SCREEN shown for %s in %s (not answered: it would write to the profile)", s.Name, workDir())
		return tm, scr, false
	}
	if loginRe.MatchString(scr) && !s.Ready.MatchString(scr) {
		t.Errorf("LOGIN SCREEN shown for %s", s.Name)
		return tm, scr, false
	}
	if !s.Ready.MatchString(scr) {
		t.Errorf("the start screen of %s has no input prompt matching %s", s.Name, s.Ready)
		return tm, scr, false
	}
	return tm, scr, true
}

func cliReady(s string) bool {
	return regexp.MustCompile(`(?m)^[│| ]*[❯›>]([ \x{a0}]|$)`).MatchString(s)
}

func checkCLI(t *testing.T, cfg config, s cliSpec) {
	ctx, cancel := clock.WithTimeout(t.Context(), clock.Real{}, 6*time.Minute)
	defer cancel()
	e, err := buildEngine(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.db.Close() })
	id := fmt.Sprintf("rc-%s-%d", s.Name, os.Getpid())
	tm, _, ready := startAndWaitReady(ctx, t, e, cfg, s, id)
	if !ready {
		return
	}
	checkProcesses(t, tm)

	// A trivial prompt, no tools.
	if err := tm.Input(ctx, []byte(*flagAnswer)); err != nil {
		t.Fatal(err)
	}
	_ = clock.Sleep(ctx, clock.Real{}, 400*time.Millisecond)
	if err := tm.Input(ctx, []byte("\r")); err != nil {
		t.Fatal(err)
	}
	replyRe := regexp.MustCompile(`(?im)^\W{0,4}\s*ok[.!]?\s*$`)
	scr, err := waitScreen(ctx, tm, 150*time.Second, 3*time.Second, func(s string) bool { return replyRe.MatchString(s) })
	t.Logf("after the prompt (masked):\n%s", shown(scr))
	if err != nil {
		t.Errorf("no reply to %q: %v", *flagAnswer, err)
	} else {
		t.Logf("reply found on screen")
	}

	// Ctrl-C twice ends the process.
	exited := ctrlCUntilExit(ctx, t, tm)
	if !exited {
		scr, _ := tm.Screen(ctx, 0)
		t.Errorf("the agent did not exit after Ctrl-C; screen:\n%s", shown(scr.Text))
	}
	checkAfterExit(ctx, t, e, tm, id, exited)
}

// ctrlCUntilExit sends Ctrl-C twice (then up to two more times) and reports
// whether the agent ended, bounded in time.
func ctrlCUntilExit(ctx context.Context, t *testing.T, tm *terminal.Terminal) bool {
	t.Helper()
	for i := 1; i <= 4; i++ {
		if err := tm.Input(ctx, []byte{0x03}); err != nil {
			t.Logf("Ctrl-C %d: %v", i, err)
		}
		select {
		case <-tm.Exited():
			t.Logf("the agent exited after %d Ctrl-C", i)
			return true
		case <-clock.Real{}.NewTimer(700 * time.Millisecond).C():
		}
		if i == 2 {
			select {
			case <-tm.Exited():
				return true
			case <-clock.Real{}.NewTimer(8 * time.Second).C():
			}
			sc, _ := tm.Screen(ctx, 0)
			t.Logf("still running after two Ctrl-C:\n%s", shown(sc.Text))
		}
	}
	select {
	case <-tm.Exited():
		return true
	case <-clock.Real{}.NewTimer(8 * time.Second).C():
		return false
	}
}

// checkAfterExit checks what the engine does when the agent ends: the log is
// complete, nothing of the terminal is left, the row says interrupted.
func checkAfterExit(ctx context.Context, t *testing.T, e *engine, tm *terminal.Terminal, id string, exited bool) {
	t.Helper()
	if !exited {
		return
	}
	ex := tm.Client().Exit()
	t.Logf("exit of the agent: code=%d signal=%q", ex.Code, ex.Signal)
	select {
	case got := <-e.ended:
		t.Logf("OnEnded called for %s", got)
	case <-clock.Real{}.NewTimer(40 * time.Second).C():
		t.Errorf("OnEnded was not called within 40 s")
	}
	sys := terminal.NewSystem()
	wctx, cancel := clock.WithTimeout(ctx, clock.Real{}, 20*time.Second)
	defer cancel()
	for _, p := range []struct {
		name string
		pid  int
	}{{"mestre-pty", tm.PTY().PID}, {"agent", tm.AgentPID()}} {
		if err := sys.WaitExit(wctx, p.pid); err != nil {
			t.Errorf("%s %d still alive after the agent ended: %v", p.name, p.pid, err)
		}
	}
	rec, err := e.mgr.Record(ctx, id)
	if err != nil {
		t.Errorf("record: %v", err)
	} else {
		t.Logf("registry status after the exit: %s", rec.Status)
	}
	if _, err := os.Stat(rec.SocketPath); err == nil {
		t.Errorf("socket %s still exists", rec.SocketPath)
	}
	if left := leftovers(); len(left) > 0 {
		t.Errorf("processes of this account left after the terminal ended: %s", names(left))
	} else {
		t.Logf("no process of this account is left")
	}
}

// leftovers are the processes of this account other than this test, which a
// terminal that ended must not leave.
func leftovers() []int {
	var out []int
	for _, pid := range processesOf(os.Getuid()) {
		p, err := readProc(pid)
		if err != nil || p.PPID == os.Getpid() && (p.Comm == "sudo" || p.Comm == "env") {
			continue
		}
		out = append(out, pid)
	}
	return out
}

// cleanupTerminal ends whatever a failed check left.
func cleanupTerminal(tm *terminal.Terminal) {
	ctx, cancel := clock.WithTimeout(context.Background(), clock.Real{}, 30*time.Second)
	defer cancel()
	select {
	case <-tm.Exited():
	default:
		_, _ = tm.Stop(ctx, terminal.StopPlan{GracefulWait: time.Second, TerminateWait: 3 * time.Second})
	}
	tm.Detach()
	_ = terminal.NewSystem().KillTree(ctx, tm.PTY(), []int{tm.AgentPID()})
}

// ---- CA-TERM-08 with a real CLI --------------------------------------------------

func TestEngineRestart(t *testing.T) {
	var s cliSpec
	for _, c := range specs() {
		if c.Name == *flagRestart {
			s = c
		}
	}
	if s.Name == "" {
		t.Skipf("%s is not among the CLIs being run", *flagRestart)
	}
	cfg := newConfig(t)
	ctx, cancel := clock.WithTimeout(t.Context(), clock.Real{}, 6*time.Minute)
	defer cancel()
	id := fmt.Sprintf("rr-%s-%d", s.Name, os.Getpid())

	req, err := json.Marshal(map[string]any{"Config": cfg, "Spec": createSpec(cfg, s, id)})
	if err != nil {
		t.Fatal(err)
	}
	reqFile := filepath.Join(cfg.Root, "engine-req.json")
	if err := os.WriteFile(reqFile, req, 0o600); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	engine1 := exec.Command(self, helperEngine, reqFile)
	stdout, err := engine1.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	elog, err := os.Create(filepath.Join(cfg.Root, "engine1.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer elog.Close()
	engine1.Stderr = elog
	engine1.Env = append(os.Environ(), "MESTRE_REAL_CLI=1")
	if err := engine1.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine1.Process.Kill() })
	line := make(chan string, 1)
	go func() {
		b := make([]byte, 128)
		n, _ := io.ReadAtLeast(stdout, b, 1)
		line <- string(b[:n])
	}()
	var ptyPID, agentPID int
	select {
	case l := <-line:
		f := strings.Fields(l)
		if len(f) != 3 || f[0] != "READY" {
			t.Fatalf("engine said %q (see engine1.log)", l)
		}
		ptyPID, _ = strconv.Atoi(f[1])
		agentPID, _ = strconv.Atoi(f[2])
	case <-clock.Real{}.NewTimer(100 * time.Second).C():
		t.Fatal("the first engine did not report within 100 s")
	}
	t.Logf("engine 1 (pid %d) created %s: mestre-pty %d, agent %d", engine1.Process.Pid, id, ptyPID, agentPID)

	// Observe through a plain connection until the start screen is ready, so
	// the first engine is killed with a CLI at rest.
	e0, err := buildEngine(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := e0.mgr.Record(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	obs, err := terminal.Dial(ctx, clock.Real{}, rec.SocketPath, rec.Secret, 0)
	if err != nil {
		t.Fatal(err)
	}
	var before string
	var beforeOff uint64
	clk := clock.Real{}
	deadline := clk.Now().Add(90 * time.Second)
	var lastText string
	var lastChange = clock.Real{}.Now()
	for clk.Now().Before(deadline) {
		m, err := obs.Screen(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		if norm(m.Text) != norm(lastText) {
			lastText, lastChange = m.Text, clk.Now()
		}
		if s.Ready.MatchString(lastText) && clk.Now().Sub(lastChange) >= 2*time.Second {
			before, beforeOff = m.Text, m.Offset
			break
		}
		_ = clock.Sleep(ctx, clock.Real{}, 250*time.Millisecond)
	}
	_ = obs.Close()
	if before == "" {
		t.Fatalf("the start screen never came up; last screen:\n%s", shown(lastText))
	}
	t.Logf("screen before the engine died (masked), offset %d:\n%s", beforeOff, shown(before))

	// Kill the first engine hard.
	if err := engine1.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = engine1.Wait()
	for _, p := range []int{ptyPID, agentPID} {
		if _, err := readProc(p); err != nil {
			t.Fatalf("pid %d died with the engine: %v", p, err)
		}
	}
	t.Logf("engine 1 killed with SIGKILL; mestre-pty %d and agent %d are alive", ptyPID, agentPID)

	// A new engine on the same registry.
	e2, err := buildEngine(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e2.db.Close() })
	rep, err := e2.mgr.Reconcile(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	t.Logf("reconcile: %d reconnected, interrupted %v, killed %v", len(rep.Reconnected), rep.Interrupted, rep.Killed)
	// Rows left by earlier failed runs may be found dead; only ours counts.
	var tm *terminal.Terminal
	for _, r := range rep.Reconnected {
		if r.ID() == id {
			tm = r
		}
	}
	for _, other := range append(append([]string{}, rep.Interrupted...), rep.Killed...) {
		if other == id {
			t.Fatalf("reconcile took the live terminal %s for dead", id)
		}
	}
	if tm == nil {
		t.Fatalf("reconcile did not reconnect the terminal %s", id)
	}
	t.Cleanup(func() { cleanupTerminal(tm) })
	if tm.AgentPID() != agentPID || tm.PTY().PID != ptyPID {
		t.Errorf("pids changed: agent %d -> %d, mestre-pty %d -> %d", agentPID, tm.AgentPID(), ptyPID, tm.PTY().PID)
	} else {
		t.Logf("same agent PID %d and mestre-pty PID %d after the engine came back", agentPID, ptyPID)
	}
	rec2, err := e2.mgr.Record(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if rec2.Status == terminal.StatusInterrupted {
		t.Errorf("the row went through interrupted")
	}
	t.Logf("registry status after the restart: %s", rec2.Status)
	sc, err := tm.Screen(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if norm(sc.Text) != norm(before) {
		t.Errorf("the screen after the restart differs from the one before:\n%s", shown(sc.Text))
	} else {
		t.Logf("screen after the restart is identical to the one before (offset %d)", sc.Offset)
	}

	// Typing still works: a marker appears on the input line, then is cleared.
	marker := "zzmarker"
	if err := tm.Input(ctx, []byte(marker)); err != nil {
		t.Fatal(err)
	}
	scr, err := waitScreen(ctx, tm, 20*time.Second, time.Second, func(s string) bool { return strings.Contains(s, marker) })
	if err != nil {
		t.Errorf("typed text did not reach the agent after the restart: %v\n%s", err, shown(scr))
	} else {
		t.Logf("typing after the restart reached the agent (screen, masked):\n%s", shown(scr))
	}
	_ = tm.Input(ctx, []byte{0x15}) // Ctrl-U: clear the line
	_, _ = waitScreen(ctx, tm, 10*time.Second, time.Second, func(s string) bool { return !strings.Contains(s, marker) })

	exited := ctrlCUntilExit(ctx, t, tm)
	if !exited {
		t.Errorf("the agent did not exit after Ctrl-C")
	}
	checkAfterExit(ctx, t, e2, tm, id, exited)
}
