//go:build realaccount && linux

package realaccount_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lucasndas/mestre/internal/launcher"
	"github.com/lucasndas/mestre/internal/realaccount"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/store/machine"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The Linux side. The mestre-launcher is the unit of packaging/systemd (with a
// drop-in that only swaps ExecStart for this binary and the account), started
// by systemd with Delegate=yes, the two capabilities and nothing else; the
// tests are its engine: a process of the engine account, in the unit the
// launcher accepts. What the agents do runs in a terminal, as the agent
// account.

func launcherSocket(inst string) string { return "/run/mestre/" + inst + "/launcher.sock" }
func ptySocketDir(inst string) string   { return "/run/mestre/" + inst + "/pty" }
func launcherUnit(inst string) string   { return "mestre-launcher@" + inst + ".service" }

// stateRoot is the folder of the engine (db and logs), only its account's.
const stateRoot = "/var/lib/mestre-ci"

func leaveSessionAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

// osMain handles the modes of this binary that are not tests: the launcher of
// the unit.
func osMain(_ *testing.M, args []string) (bool, int) {
	if args[0] == "helper-launcher" {
		return true, runLauncher(args[1:])
	}
	return false, 0
}

// runLauncher is the mestre-launcher of the unit: launcher.Run, as the
// service does (the release has a `mestre launcher` command for it).
func runLauncher(args []string) int {
	fl := flag.NewFlagSet("helper-launcher", flag.ContinueOnError)
	inst := fl.String("instance", "", "instance")
	bin := fl.String("bin", "", "folder of the installed binaries")
	agent := fl.String("agent", "", "account of the agents")
	engineUID := fl.Uint("engine-uid", 0, "uid of the engine")
	engineCgroup := fl.String("engine-cgroup", "", "cgroup of the engine")
	noCgroup := fl.Bool("no-cgroup", false, "run without a delegated cgroup (LIN-11 failed)")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	cfg := launcher.RunConfig{
		SocketPath: launcherSocket(*inst), BinDir: *bin, SocketDir: ptySocketDir(*inst), Agent: *agent,
		Engine: launcher.PeerPolicy{UID: uint32(*engineUID), Cgroup: *engineCgroup},
		Audit:  func(e launcher.Event) { fmt.Fprintf(os.Stderr, "audit: %+v\n", e) },
	}
	if !*noCgroup {
		base, err := launcher.ResolveCgroupBase()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		cfg.CgroupBase = base
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := launcher.Run(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// osAgent has the agent modes of Linux: the process that tries to reach the
// mestre-pty of another terminal, as another process of the agent account.
func osAgent(args []string) int {
	if args[0] == "probe" && len(args) == 7 {
		return runProbe(args[1:])
	}
	return 2
}

// errName names the errno that matters; "ok" is no error.
func errName(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, syscall.EACCES):
		return "EACCES"
	case errors.Is(err, syscall.EPERM):
		return "EPERM"
	}
	return err.Error()
}

// runProbe is the agent of CA-TERM-11: <report> <mestre-pty PID> <control PID>
// <launcher socket> <socket of the mestre-pty> <folder of the sockets>. It
// tries, as a process of the agent account in a terminal of its own, what the
// agents must not do to the mestre-pty of another terminal, and to the engine's
// sockets; the control PID is an agent process that is dumpable (the same
// account, nothing protecting it), to tell "the mestre-pty protects itself"
// from "nothing is allowed at all".
func runProbe(args []string) int {
	report, sockets := args[0], args[3:]
	pty, err1 := strconv.Atoi(args[1])
	ctl, err2 := strconv.Atoi(args[2])
	if err := errors.Join(err1, err2); err != nil {
		return 2
	}
	rep := map[string]string{}
	read := func(path string) error { _, err := os.ReadFile(path); return err }
	open := func(path string) error {
		f, err := os.Open(path)
		if err == nil {
			_ = f.Close()
		}
		return err
	}
	attach := func(pid int) error {
		runtime.LockOSThread() // ptrace works on the thread that attaches
		defer runtime.UnlockOSThread()
		if err := unix.PtraceAttach(pid); err != nil {
			return err
		}
		var ws syscall.WaitStatus
		_, _ = syscall.Wait4(pid, &ws, syscall.WALL, nil)
		return unix.PtraceDetach(pid)
	}
	getfd := func(pid, fd int) error {
		pidfd, err := unix.PidfdOpen(pid, 0)
		if err != nil {
			return err
		}
		defer unix.Close(pidfd)
		got, err := unix.PidfdGetfd(pidfd, fd, 0)
		if err == nil {
			_ = unix.Close(got)
		}
		return err
	}
	dial := func(path string) error {
		c, err := net.Dial("unix", path)
		if err == nil {
			_ = c.Close()
		}
		return err
	}
	rep["environ of the mestre-pty"] = errName(read(fmt.Sprintf("/proc/%d/environ", pty)))
	rep["memory of the mestre-pty"] = errName(open(fmt.Sprintf("/proc/%d/mem", pty)))
	rep["ptrace of the mestre-pty"] = errName(attach(pty))
	rep["pidfd_getfd of the mestre-pty"] = errName(getfd(pty, 3))
	rep["environ of the control"] = errName(read(fmt.Sprintf("/proc/%d/environ", ctl)))
	rep["ptrace of the control"] = errName(attach(ctl))
	rep["pidfd_getfd of the control"] = errName(getfd(ctl, 0))
	rep["socket of the launcher"] = errName(dial(sockets[0]))
	rep["socket of the mestre-pty"] = errName(dial(sockets[1]))
	_, err := os.ReadDir(sockets[2])
	rep["folder of the sockets"] = errName(err)
	b, err := json.Marshal(rep)
	if err != nil || writeAtomic(report, b) != nil {
		return 1
	}
	fmt.Print("DONE\r\n")
	block()
	return 0
}

// A rig is the engine side of one launcher instance: a terminal manager that
// asks that launcher for the mestre-pty of each terminal.
type rig struct {
	inst   string
	mgr    *terminal.Manager
	client *launcher.Client
	root   string // the state of this rig, the engine's alone
	shared string // where the agents leave their reports: any account writes
}

func newRig(t *testing.T, inst string) *rig {
	t.Helper()
	root, err := os.MkdirTemp(stateRoot, "t")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	shared, err := os.MkdirTemp("", "ra")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shared) })
	db, err := machine.Open(context.Background(), filepath.Join(root, "maquina.db"), store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	client := launcher.NewClient(launcherSocket(inst))
	mgr, err := terminal.NewManager(terminal.ManagerConfig{
		DB: db, System: terminal.NewSystem(), Spawner: client, MachineID: "m1",
		SocketDir: ptySocketDir(inst), LogDir: filepath.Join(root, "logs"),
		PTYBinary: func(v string) (string, error) {
			return filepath.Join(realaccount.LinuxStage, "bin", v, "mestre-pty"), nil
		},
		PTYVersion: realaccount.PTYVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &rig{inst: inst, mgr: mgr, client: client, root: root, shared: shared}
}

// start makes a terminal whose agent is the helper mode args of this binary,
// and ends it with the test, whatever happened.
func (r *rig) start(t *testing.T, id string, args ...string) *terminal.Terminal {
	t.Helper()
	term, err := r.mgr.Create(ctxFor(t), terminal.CreateSpec{
		ID: id, CharacterID: "c-" + id, Dir: r.root,
		Path: filepath.Join(realaccount.LinuxStage, "realaccount.test"), Args: append([]string{helperAgent}, args...),
		Env: []string{"PATH=/usr/bin:/bin"}, WorkDir: "/",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := clock.WithTimeout(context.Background(), clock.Real{}, 30*time.Second)
		defer cancel()
		_, _ = term.Stop(ctx, terminal.StopPlan{TerminateWait: 300 * time.Millisecond})
		_ = r.client.KillCgroup(ctx, id)
		_ = r.client.KillTree(ctx, id, term.PTY(), []int{term.AgentPID()})
	})
	return term
}

// ready starts a terminal and waits for its agent to say it is.
func (r *rig) ready(t *testing.T, id string, args ...string) *terminal.Terminal {
	t.Helper()
	term := r.start(t, id, args...)
	observe(ctxFor(t), t, term).waitFor(t, "READY")
	return term
}

// status is /proc/<pid>/status as a map.
func status(t *testing.T, pid int) map[string]string {
	t.Helper()
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for l := range strings.SplitSeq(string(b), "\n") {
		if k, v, ok := strings.Cut(l, ":"); ok {
			m[k] = strings.TrimSpace(v)
		}
	}
	return m
}

func lookup(t *testing.T, name string) *user.User {
	t.Helper()
	u, err := user.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// runsAs checks that all four user IDs and group IDs of the process are the
// account's, with no supplementary group.
func runsAs(t *testing.T, what string, pid int, u *user.User) {
	t.Helper()
	st := status(t, pid)
	if got := strings.Fields(st["Uid"]); len(got) != 4 || got[0] != u.Uid || got[1] != u.Uid || got[2] != u.Uid || got[3] != u.Uid {
		t.Errorf("%s: Uid = %q, want %s four times", what, st["Uid"], u.Uid)
	}
	if got := strings.Fields(st["Gid"]); len(got) != 4 || got[0] != u.Gid || got[1] != u.Gid || got[2] != u.Gid || got[3] != u.Gid {
		t.Errorf("%s: Gid = %q, want %s four times", what, st["Gid"], u.Gid)
	}
	if st["Groups"] != "" {
		t.Errorf("%s: supplementary groups = %q, want none", what, st["Groups"])
	}
}

// noCaps checks that the process has no capability in any set it could use.
func noCaps(t *testing.T, what string, pid int) {
	t.Helper()
	st := status(t, pid)
	for _, k := range []string{"CapInh", "CapPrm", "CapEff", "CapAmb"} {
		if st[k] != "0000000000000000" {
			t.Errorf("%s: %s = %s, want none", what, k, st[k])
		}
	}
}

// unitPID is the main process of a unit.
func unitPID(t *testing.T, unit string) int {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "systemctl", "show", "-p", "MainPID", "--value", unit).Output()
	if err != nil {
		t.Fatalf("systemctl show %s: %v", unit, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid == 0 {
		t.Fatalf("main PID of %s = %q, %v", unit, out, err)
	}
	return pid
}

// canNotSignal checks that the engine, another account than the agents, is not
// allowed to signal the process: it has no CAP_KILL and the user ID differs.
func canNotSignal(t *testing.T, what string, pid int) {
	t.Helper()
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.EPERM) {
		t.Errorf("%s (PID %d): kill(0) = %v, want EPERM: the engine must not reach the processes of the agents", what, pid, err)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); !errors.Is(err, syscall.EPERM) {
		t.Errorf("%s (PID %d): SIGKILL = %v, want EPERM", what, pid, err)
	}
}

// goneAll waits for every process to be gone.
func goneAll(ctx context.Context, t *testing.T, what string, pids ...int) {
	t.Helper()
	sys := terminal.NewSystem()
	for _, pid := range pids {
		if err := sys.WaitExit(ctx, pid); err != nil {
			t.Errorf("%s (PID %d) still alive: %v", what, pid, err)
		}
	}
}

func readPIDs(t *testing.T, path string) []int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var pids []int
	for f := range strings.FieldsSeq(string(b)) {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatal(err)
		}
		pids = append(pids, n)
	}
	return pids
}

// CA-TERM-07 (real accounts): the launcher of the unit starts the mestre-pty
// as the agent account, with no supplementary group, born in the cgroup
// term-<id> under the cgroup systemd delegated to the launcher; the agent of
// the terminal runs as the same account in the same cgroup.
func TestLinuxTheMestrePTYRunsAsTheAgentAccountInTheCgroupOfItsTerminal(t *testing.T) {
	t.Parallel()
	r := newRig(t, realaccount.Instance)
	id := newID("la")
	term := r.ready(t, id, "sleeper")
	agent := lookup(t, realaccount.LinuxAgentUser)
	engine := lookup(t, realaccount.LinuxEngineUser)
	if agent.Uid == engine.Uid {
		t.Fatalf("the agent account (%s) is the engine's: the test proves nothing", agent.Uid)
	}
	if strconv.Itoa(os.Geteuid()) != engine.Uid {
		t.Fatalf("the test runs as uid %d, not as the engine account (%s)", os.Geteuid(), engine.Uid)
	}

	runsAs(t, "mestre-pty", term.PTY().PID, agent)
	runsAs(t, "agent", term.AgentPID(), agent)

	// The cgroup of the terminal is a child of the delegated cgroup of the
	// launcher unit, and both processes are in it.
	launcherCg, err := terminal.ProcCgroup(unitPID(t, launcherUnit(realaccount.Instance)))
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(launcherCg) // the process sits in the leaf "launcher" of the unit
	if !strings.HasSuffix(base, "/"+launcherUnit(realaccount.Instance)) {
		t.Fatalf("the launcher is in %s: not the leaf of its unit", launcherCg)
	}
	want := base + "/term-" + id
	for _, p := range []struct {
		what string
		pid  int
	}{{"mestre-pty", term.PTY().PID}, {"agent", term.AgentPID()}} {
		if got, err := terminal.ProcCgroup(p.pid); err != nil || got != want {
			t.Errorf("cgroup of the %s = %q, %v; want %s", p.what, got, err, want)
		}
	}
	if sid, err := unix.Getsid(term.PTY().PID); err != nil || sid != term.PTY().PID {
		t.Errorf("session of the mestre-pty = %d, %v; want its own", sid, err)
	}
}

// CA-TERM-07 (real accounts): the launcher keeps CAP_SETUID and CAP_SETGID
// (permitted and effective) and no ambient capability, in the account of the
// engine; the mestre-pty and the agent have none, in any set.
func TestLinuxNeitherTheMestrePTYNorTheAgentHoldsCapabilities(t *testing.T) {
	t.Parallel()
	r := newRig(t, realaccount.Instance)
	term := r.ready(t, newID("lc"), "sleeper")
	engine := lookup(t, realaccount.LinuxEngineUser)

	lpid := unitPID(t, launcherUnit(realaccount.Instance))
	st := status(t, lpid)
	const setuidSetgid = "00000000000000c0" // CAP_SETGID (6) and CAP_SETUID (7)
	for _, k := range []string{"CapPrm", "CapEff", "CapBnd"} {
		if st[k] != setuidSetgid {
			t.Errorf("launcher: %s = %s, want %s (CAP_SETUID and CAP_SETGID only)", k, st[k], setuidSetgid)
		}
	}
	if st["CapAmb"] != "0000000000000000" {
		t.Errorf("launcher: CapAmb = %s: the ambient set was not emptied, and what it starts would inherit the capabilities", st["CapAmb"])
	}
	if got := strings.Fields(st["Uid"]); len(got) == 0 || got[0] != engine.Uid {
		t.Errorf("launcher: Uid = %q, want the engine account %s", st["Uid"], engine.Uid)
	}
	noCaps(t, "mestre-pty", term.PTY().PID)
	noCaps(t, "agent", term.AgentPID())
}

// CA-TERM-05 (real accounts): with a delegated cgroup, the engine cannot
// signal anything of the agent account, and the forced stop still leaves
// nothing: the launcher's cgroup.kill reaches the mestre-pty, the agent, the
// grandchild that ignores SIGTERM and the one that left the session, and the
// cgroup is removed.
func TestLinuxTheEngineEndsATreeOnlyThroughTheLauncherCgroup(t *testing.T) {
	t.Parallel()
	r := newRig(t, realaccount.Instance)
	id := newID("lk")
	report := filepath.Join(r.shared, "pids")
	term := r.ready(t, id, "tree", report, "1")
	rec := term.Record()
	grand := readPIDs(t, report)
	if len(grand) != 2 {
		t.Fatalf("the agent reported %v, want two grandchildren", grand)
	}
	cg, err := terminal.ProcCgroup(term.PTY().PID)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := terminal.ProcCgroup(grand[1]); err != nil || got != cg {
		t.Fatalf("the grandchild that left the session is in %q, %v; want the cgroup of the terminal %s", got, err, cg)
	}
	if sid, _ := unix.Getsid(grand[1]); sid == term.AgentPID() {
		t.Fatal("the grandchild did not leave the session of the agent: the test proves nothing")
	}
	canNotSignal(t, "mestre-pty", term.PTY().PID)
	canNotSignal(t, "agent", term.AgentPID())
	canNotSignal(t, "grandchild", grand[1])

	ctx := ctxFor(t)
	step, err := term.Stop(ctx, terminal.StopPlan{TerminateWait: 300 * time.Millisecond})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if step != terminal.StepForced {
		t.Errorf("ended at step %v, want the forced one (the grandchildren ignore SIGTERM)", step)
	}
	goneAll(ctx, t, "a process of the tree", append([]int{term.PTY().PID, term.AgentPID()}, grand...)...)
	if _, err := os.Stat("/sys/fs/cgroup" + cg); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the cgroup of the terminal is still there: %v", err)
	}
	if _, err := os.Stat(rec.SocketPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the socket is still there: %v", err)
	}
}

// CA-TERM-05 and CA-TERM-07 (real accounts): without a delegated cgroup, the
// kill by identity asks the launcher, which takes the user ID of the agent
// account on a thread of its own and ends the mestre-pty and the sessions of
// the agent; the bystander, a terminal of the same account in a session of its
// own, is not touched, and neither is a process whose start identity differs.
func TestLinuxTheLauncherEndsByIdentityWithoutACgroup(t *testing.T) {
	t.Parallel()
	r := newRig(t, "nocg")
	report := filepath.Join(r.shared, "pids")
	victim := r.ready(t, newID("ln"), "tree", report, "0")
	bystander := r.ready(t, newID("lb"), "sleeper")
	grand := readPIDs(t, report)
	if len(grand) != 1 {
		t.Fatalf("the agent reported %v, want one grandchild", grand)
	}
	if got, err := terminal.ProcCgroup(victim.PTY().PID); err != nil || strings.Contains(got, "term-") {
		t.Fatalf("the mestre-pty is in %q, %v: this launcher must have no cgroup for the terminal", got, err)
	}
	agent := lookup(t, realaccount.LinuxAgentUser)
	runsAs(t, "bystander", bystander.PTY().PID, agent)
	canNotSignal(t, "mestre-pty", victim.PTY().PID)
	canNotSignal(t, "agent", victim.AgentPID())
	canNotSignal(t, "grandchild", grand[0])

	ctx := ctxFor(t)
	// A process whose start identity is not the one asked for is not touched.
	wrong := terminal.ProcID{PID: bystander.PTY().PID, Start: bystander.PTY().Start + "0"}
	_ = r.client.KillTree(ctx, "wrong", wrong, nil)
	if !terminal.Alive(terminal.NewSystem(), bystander.PTY()) {
		t.Fatal("a process whose start identity differs was ended")
	}

	if err := r.client.KillTree(ctx, "victim", victim.PTY(), []int{victim.AgentPID()}); err != nil {
		t.Fatalf("kill by identity: %v", err)
	}
	goneAll(ctx, t, "a process of the terminal", victim.PTY().PID, victim.AgentPID(), grand[0])
	if !terminal.Alive(terminal.NewSystem(), bystander.PTY()) {
		t.Error("the mestre-pty of the other terminal, same account, was ended")
	}
	if errors.Is(syscall.Kill(bystander.AgentPID(), 0), syscall.ESRCH) {
		t.Error("the agent of the other terminal, same account, was ended")
	}
}

// CA-TERM-07 (real accounts): restarting the unit of the launcher (systemctl
// restart, with KillMode=process) ends its own process only: the terminals go
// on, the new launcher serves, and it ends the old terminal through its
// cgroup, which it finds again.
func TestLinuxTerminalsSurviveARestartOfTheLauncherUnit(t *testing.T) {
	t.Parallel()
	r := newRig(t, "rst")
	old := r.ready(t, newID("lr"), "sleeper")
	before := unitPID(t, launcherUnit("rst"))
	cg, err := terminal.ProcCgroup(old.PTY().PID)
	if err != nil || !strings.Contains(cg, "/term-") {
		t.Fatalf("the mestre-pty is in %q, %v: not in the cgroup of its terminal", cg, err)
	}

	if out, err := exec.CommandContext(t.Context(), "sudo", "-n", "systemctl", "restart", launcherUnit("rst")).CombinedOutput(); err != nil {
		t.Fatalf("restart the launcher: %v\n%s", err, out)
	}
	if after := unitPID(t, launcherUnit("rst")); after == before {
		t.Fatalf("the launcher kept PID %d: it was not restarted", after)
	}
	if !terminal.Alive(terminal.NewSystem(), old.PTY()) {
		t.Fatal("the mestre-pty did not survive the restart of the launcher unit")
	}
	if got, err := terminal.ProcCgroup(old.PTY().PID); err != nil || got != cg {
		t.Errorf("the cgroup of the mestre-pty changed to %q, %v", got, err)
	}
	// The new launcher starts terminals, and ends the one it did not start.
	fresh := r.ready(t, newID("lr"), "sleeper")
	ctx := ctxFor(t)
	if err := r.client.KillCgroup(ctx, old.ID()); err != nil {
		t.Fatalf("the new launcher cannot end the old terminal: %v", err)
	}
	goneAll(ctx, t, "a process of the old terminal", old.PTY().PID, old.AgentPID())
	if _, err := os.Stat("/sys/fs/cgroup" + cg); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the cgroup of the old terminal is still there: %v", err)
	}
	if !terminal.Alive(terminal.NewSystem(), fresh.PTY()) {
		t.Error("ending the old terminal ended the new one")
	}
}

var denied = regexp.MustCompile(`^(EACCES|EPERM)$`)

// CA-TERM-11 (real accounts): another process of the agent account, in
// another terminal, cannot read the environment or the memory of a mestre-pty,
// attach to it with ptrace or take a descriptor of it (PR_SET_DUMPABLE 0), nor
// reach the sockets of the engine, while the same attempts on a dumpable
// process of the account work: the control of the test.
func TestLinuxAnotherAgentProcessCannotTouchTheMestrePTY(t *testing.T) {
	t.Parallel()
	r := newRig(t, realaccount.Instance)
	victim := r.ready(t, newID("lv"), "sleeper")
	report := filepath.Join(r.shared, "probe")
	prober := r.start(t, newID("lp"), "probe", report,
		strconv.Itoa(victim.PTY().PID), strconv.Itoa(victim.AgentPID()),
		launcherSocket(realaccount.Instance), victim.Record().SocketPath, ptySocketDir(realaccount.Instance))
	observe(ctxFor(t), t, prober).waitFor(t, "DONE")
	b, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var rep map[string]string
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("report %q: %v", b, err)
	}

	for _, k := range []string{"environ of the mestre-pty", "memory of the mestre-pty", "pidfd_getfd of the mestre-pty"} {
		if !denied.MatchString(rep[k]) {
			t.Errorf("%s: %q, want it denied", k, rep[k])
		}
	}
	if rep["environ of the control"] != "ok" {
		t.Fatalf("the agent could not read the environment of a process of its own account (%q): the test proves nothing", rep["environ of the control"])
	}
	if rep["ptrace of the control"] == "ok" {
		if !denied.MatchString(rep["ptrace of the mestre-pty"]) {
			t.Errorf("ptrace of the mestre-pty: %q, want it denied (PR_SET_DUMPABLE), since ptrace of a dumpable process of the account works", rep["ptrace of the mestre-pty"])
		}
	} else {
		t.Logf("ptrace of a dumpable process of the account is denied too (%s: kernel.yama.ptrace_scope): its denial for the mestre-pty proves nothing here", rep["ptrace of the control"])
	}
	if rep["pidfd_getfd of the control"] != "ok" {
		t.Logf("pidfd_getfd of a dumpable process of the account is denied too (%s)", rep["pidfd_getfd of the control"])
	}
	// The engine's sockets and folders: the agent account does not even
	// cross the folder.
	for _, k := range []string{"socket of the launcher", "socket of the mestre-pty", "folder of the sockets"} {
		if !denied.MatchString(rep[k]) {
			t.Errorf("%s: %q, want it denied to the agent account", k, rep[k])
		}
	}
	// And the mestre-pty went on, untouched.
	if !terminal.Alive(terminal.NewSystem(), victim.PTY()) {
		t.Error("the mestre-pty is gone")
	}
}

// CA-TERM-07 (real accounts): the launcher accepts the engine by its account
// and by its cgroup. This run is of the same account in another unit (the tool
// runs it apart): the request is refused, recorded, and no mestre-pty, socket
// or cgroup is made.
func TestLinuxTheLauncherRefusesTheEngineAccountOutsideItsUnit(t *testing.T) {
	t.Parallel()
	own, err := terminal.ProcCgroup(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(own, "/"+realaccount.LinuxEngineUnit) {
		t.Skip("this is the run as the engine; the refusal is tried by the run in another unit")
	}
	if err := os.MkdirAll(ptySocketDir(realaccount.Instance), 0o700); err != nil {
		t.Fatal(err)
	}
	id := newID("lo")
	sock, err := terminal.SocketPath(ptySocketDir(realaccount.Instance), id)
	if err != nil {
		t.Fatal(err)
	}
	client := launcher.NewClient(launcherSocket(realaccount.Instance))
	_, err = client.Spawn(ctxFor(t), terminal.SpawnSpec{ID: id, Version: realaccount.PTYVersion, SocketPath: sock, Secret: bytes.Repeat([]byte{7}, 32)})
	var refused *launcher.Error
	if !errors.As(err, &refused) || refused.Code != launcher.CodeRefused {
		t.Fatalf("Spawn from %s = %v, want the launcher to refuse it", own, err)
	}
	if _, err := os.Stat(sock); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the socket of a terminal that was refused is there: %v", err)
	}
	// And its kill, by id or by identity.
	if err := client.KillCgroup(ctxFor(t), id); !errors.As(err, &refused) {
		t.Errorf("kill from %s = %v, want a refusal", own, err)
	}
}
