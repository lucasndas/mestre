//go:build full && windows

package launcher_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/mestre-ia/mestre/internal/launcher"
	"github.com/mestre-ia/mestre/internal/terminal"
	"github.com/mestre-ia/mestre/internal/terminal/ptywire"
	"github.com/mestre-ia/mestre/internal/winsvc"
)

// osEnv is the Windows side of the fixture. The launcher runs inside the test
// as an ordinary process (the CI has no service), with the account of the
// test as the engine, and as the "agent account" too (winsvc.SelfAccount):
// the whole path of start-pty runs, with the token of the launcher in place
// of the one a LogonUser would give. The test process plays the engine
// service: the PID the "service manager" reports is its own.
type osEnv struct {
	cfg   winsvc.LauncherConfig
	ended chan string // terminals whose session the launcher released, in order
	// released has the terminals whose Session.Release was called.
	released chan string
	stop     context.CancelFunc
	served   chan struct{}
	// noBreakaway is why a mestre-pty cannot be created outside the Jobs of
	// the test process (empty when it can).
	noBreakaway string
}

const (
	helperSpawn = "helper-spawn" // <pipe> <id> <socket>: asks the launcher for a terminal
	helperExit  = "helper-exit"
)

func osHelper(args []string) (bool, int) {
	switch args[0] {
	case helperExit:
		return true, 0
	case helperSpawn:
		if len(args) != 4 {
			return true, 2
		}
		spec := terminal.SpawnSpec{ID: args[2], Version: ptyVersion, SocketPath: args[3], Secret: make([]byte, ptywire.SecretSize)}
		_, err := winsvc.NewLauncherClientAt(args[1]).Spawn(context.Background(), spec)
		if err != nil {
			fmt.Printf("error: %v\n", err)
			return true, 0
		}
		fmt.Println("started")
		return true, 0
	}
	return false, 0
}

// The test process joins a Job of its own that allows breakaway, so that
// everything it starts is in it, and a mestre-pty that is not in it proves
// the launcher took it out. If an enclosing Job forbids breakaway altogether,
// no process can leave, and the tests that start one are skipped.
var testJob = sync.OnceValues(func() (windows.Handle, string) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Sprintf("cannot create a Job Object: %v", err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return 0, fmt.Sprintf("cannot configure the Job Object: %v", err)
	}
	if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		return 0, fmt.Sprintf("the test process cannot join a Job Object: %v", err)
	}
	// Can a child of this process leave its Jobs at all?
	probe := exec.Command(os.Args[0], helperExit)
	probe.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_BREAKAWAY_FROM_JOB}
	if err := probe.Run(); err != nil {
		return job, fmt.Sprintf("a Job that encloses the tests forbids breakaway (%v): the launcher cannot create a process outside it; the service of the installation is in no Job", err)
	}
	return job, ""
})

func newEnv(t *testing.T) *env {
	t.Helper()
	root := shortDir(t)
	self, err := winsvc.CurrentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	e := &env{root: root, sockets: filepath.Join(root, "p"), audit: &auditLog{}, sys: terminal.NewSystem()}
	_, e.os.noBreakaway = testJob()
	e.os.ended = make(chan string, 16)
	released := make(chan string, 16)
	e.os.released = released
	policy := winsvc.ClientPolicy{SID: self, ServicePID: func() (uint32, error) { return uint32(os.Getpid()), nil }}
	e.os.cfg = winsvc.LauncherConfig{
		BinDir: builds.bin, SocketDir: e.sockets, StateDir: filepath.Join(root, "state"),
		Engine: policy, Audit: e.audit.record,
		Account: winsvc.SelfAccount{Released: func(id string) { released <- id }},
		Ended:   func(id string) { e.os.ended <- id },
	}
	e.os.cfg.Pipe = winsvc.PipeName("test-" + filepath.Base(root))
	e.started = e.listen(t, e.os.cfg)
	e.client = winsvc.NewLauncherClientAt(e.os.cfg.Pipe)
	pipe := e.os.cfg.Pipe
	e.dial = func(ctx context.Context) (net.Conn, error) { return winsvc.DialPipe(ctx, pipe) }

	// A launcher whose engine is another account: SYSTEM.
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	other := e.os.cfg
	other.Pipe = winsvc.PipeName("test-" + filepath.Base(root) + "-other")
	other.StateDir = filepath.Join(root, "state-other")
	other.Engine = winsvc.ClientPolicy{SID: system, ServicePID: policy.ServicePID}
	otherAudit := &auditLog{}
	other.Audit = otherAudit.record
	otherStarted := e.listen(t, other)
	otherClient := winsvc.NewLauncherClientAt(other.Pipe)
	e.strangers = append(e.strangers, stranger{
		name:    "another account",
		audit:   otherAudit,
		started: otherStarted,
		try: func(ctx context.Context, spec terminal.SpawnSpec) error {
			_, err := otherClient.Spawn(ctx, spec)
			return err
		},
	})
	// The same account, but not the process the service manager reports: a
	// process of the agents' account is not an engine either.
	e.strangers = append(e.strangers, stranger{
		name:    "another process of the same account",
		audit:   e.audit,
		started: e.started,
		try: func(ctx context.Context, spec terminal.SpawnSpec) error {
			var out, errb bytes.Buffer
			cmd := exec.CommandContext(ctx, os.Args[0], helperSpawn, pipe, spec.ID, spec.SocketPath)
			cmd.Stdout, cmd.Stderr = &out, &errb
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("helper: %w: %s", err, errb.String())
			}
			if strings.HasPrefix(out.String(), "started") {
				return nil
			}
			return errors.New(strings.TrimSpace(out.String()))
		},
	})
	return e
}

// listen starts a launcher with cfg in the test process. It runs until the
// test ends.
func (e *env) listen(t *testing.T, cfg winsvc.LauncherConfig) func() int {
	t.Helper()
	l, err := winsvc.NewLauncher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	if cfg.Pipe == e.os.cfg.Pipe {
		e.os.stop, e.os.served = cancel, served
	}
	t.Cleanup(func() { cancel(); <-served })
	go func() { defer close(served); _ = l.Serve(ctx) }()
	return l.Started
}

func (e *env) needStart(t *testing.T) {
	t.Helper()
	if e.os.noBreakaway != "" {
		t.Skip(e.os.noBreakaway)
	}
}

// checkStarted: the mestre-pty is not in the Job of the test, which the
// test process is in.
func (e *env) checkStarted(t *testing.T, _ context.Context, _ terminal.SpawnSpec, sp terminal.Spawned, _ *terminal.Client) {
	t.Helper()
	// The descriptor the launcher creates the mestre-pty with: the engine (the
	// account of the test here) may query and wait, and the owner (the agent
	// account) only query, so another agent can neither read its memory,
	// duplicate its handles, inject a thread nor change who may
	// (docs/arquitetura/terminal.md#socket-e-aperto-de-mão).
	t.Run("protected from the agents", func(t *testing.T) {
		t.Parallel()
		// The runner of the CI is an administrator with SeDebugPrivilege,
		// which opens any process whatever its DACL says; the agents have
		// no such privilege. Taking it off the token of the test process
		// (it is not used by any other test) makes the test one of them.
		removeDebugPrivilege(t)
		for _, tc := range []struct {
			name    string
			access  uint32
			allowed bool
		}{
			{"query", windows.PROCESS_QUERY_LIMITED_INFORMATION, true},
			{"wait", windows.SYNCHRONIZE, true},
			{"read its memory", windows.PROCESS_VM_READ, false},
			{"write its memory", windows.PROCESS_VM_WRITE | windows.PROCESS_VM_OPERATION, false},
			{"duplicate its handles", windows.PROCESS_DUP_HANDLE, false},
			{"inject a thread", windows.PROCESS_CREATE_THREAD, false},
			{"suspend it", windows.PROCESS_SUSPEND_RESUME, false},
			{"change its DACL", windows.WRITE_DAC, false},
			{"take its ownership", windows.WRITE_OWNER, false},
		} {
			h, err := windows.OpenProcess(tc.access, false, uint32(sp.PID))
			if err == nil {
				_ = windows.CloseHandle(h)
			}
			if tc.allowed && err != nil {
				t.Errorf("%s: %v, want it allowed", tc.name, err)
			}
			if !tc.allowed && !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
				t.Errorf("%s: %v, want access denied", tc.name, err)
			}
		}
	})

	t.Run("outside the Job of the test", func(t *testing.T) {
		t.Parallel()
		job, _ := testJob()
		if job == 0 {
			t.Skip("the test process could not join a Job Object of its own")
		}
		if in, err := winsvc.IsProcessInJob(windows.CurrentProcess(), job); err != nil || !in {
			t.Fatalf("the test process is in its Job = %v, %v; the check below proves nothing", in, err)
		}
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(sp.PID))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = windows.CloseHandle(h) }()
		in, err := winsvc.IsProcessInJob(h, job)
		if err != nil {
			t.Fatal(err)
		}
		if in {
			t.Error("the mestre-pty is in the Job of the process that asked for it: the engine could end it, and its Job would hold it")
		}
	})
}

// removeDebugPrivilege takes SeDebugPrivilege off the token of this process.
func removeDebugPrivilege(t *testing.T) {
	t.Helper()
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &tok); err != nil {
		t.Fatal(err)
	}
	defer tok.Close()
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr("SeDebugPrivilege"), &luid); err != nil {
		t.Fatal(err)
	}
	tp := windows.Tokenprivileges{PrivilegeCount: 1}
	tp.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_REMOVED}
	if err := windows.AdjustTokenPrivileges(tok, false, &tp, 0, nil, nil); err != nil {
		t.Fatal(err)
	}
}

// wait for the launcher to release the session of terminal id.
func (e *env) waitEnded(t *testing.T, ctx context.Context, id string) {
	t.Helper()
	for {
		select {
		case got := <-e.os.ended:
			if got == id {
				// The session is released before the launcher says it ended.
				for {
					select {
					case r := <-e.os.released:
						if r == id {
							return
						}
					default:
						t.Fatalf("the launcher said terminal %s ended without releasing its session", id)
					}
				}
			}
		case <-ctx.Done():
			t.Fatalf("the session of terminal %s was not released: %v", id, ctx.Err())
		}
	}
}

// osCases registers what only Windows has to check.
func osCases(t *testing.T) {
	t.Run("refuses kill: the Windows launcher only does start-pty", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		resp, err := e.raw(ctxFor(t), mustJSON(t, map[string]any{"op": "kill", "id": "t07-kill"}))
		if err != nil {
			t.Fatal(err)
		}
		if resp.OK || resp.Code != launcher.CodeRefused {
			t.Errorf("answer = %+v, want a refusal", resp)
		}
		if e.audit.count(launcher.OutcomeRefused) != 1 {
			t.Errorf("refusals recorded = %d, want 1", e.audit.count(launcher.OutcomeRefused))
		}
	})
	t.Run("releases the session when the mestre-pty ends", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		e.needStart(t)
		ctx := ctxFor(t)
		spec := e.spec("t07-release")
		sp, err := e.client.Spawn(ctx, spec)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = e.sys.KillTree(context.Background(), sp.ProcID, nil) })
		list, err := launcher.OpenPTYList(filepath.Join(e.os.cfg.StateDir, "launcher-ptys.json"))
		if err != nil || len(list.Entries()) != 1 || list.Entries()[0] != (launcher.PTYEntry{ID: spec.ID, PID: sp.PID, Start: sp.Start}) {
			t.Fatalf("list of mestre-pty = %v, %v; want the one just created", list.Entries(), err)
		}
		select {
		case id := <-e.os.ended:
			t.Fatalf("the session of %s was released while the mestre-pty lives", id)
		default:
		}
		if err := e.sys.KillTree(ctx, sp.ProcID, nil); err != nil {
			t.Fatal(err)
		}
		e.waitEnded(t, ctx, spec.ID)
		list, err = launcher.OpenPTYList(filepath.Join(e.os.cfg.StateDir, "launcher-ptys.json"))
		if err != nil || len(list.Entries()) != 0 {
			t.Errorf("list of mestre-pty after it ended = %v, %v", list.Entries(), err)
		}
	})
	t.Run("the pipe is closed to whoever the ACL does not name", func(t *testing.T) {
		t.Parallel()
		name := winsvc.PipeName("test-acl-" + filepath.Base(shortDir(t)))
		ln, err := winsvc.ListenPipe(name, "D:P(A;;GA;;;SY)")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		if _, err := winsvc.DialPipe(ctxFor(t), name); !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			t.Errorf("DialPipe = %v, want access denied", err)
		}
	})
}

// checkRestart: the launcher that stops releases nothing, the next one takes
// back the live mestre-pty by PID and creation time (and drops the entry of
// a PID that is no longer it), and releases each when it ends.
func (e *env) checkRestart(t *testing.T) {
	t.Helper()
	e.needStart(t)
	ctx := ctxFor(t)
	spec := e.spec("t07-restart")
	sp, err := e.client.Spawn(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.sys.KillTree(context.Background(), sp.ProcID, nil) })

	e.os.stop()
	<-e.os.served
	select {
	case id := <-e.os.ended:
		t.Fatalf("the launcher released the session of %s when it stopped, but the mestre-pty lives", id)
	default:
	}
	if !terminal.Alive(e.sys, sp.ProcID) {
		t.Fatal("the mestre-pty did not survive the launcher")
	}
	// An entry whose PID is alive but is another process (this test's own,
	// with a creation time that is not the recorded one) must be dropped,
	// never taken back or touched.
	statePath := filepath.Join(e.os.cfg.StateDir, "launcher-ptys.json")
	list, err := launcher.OpenPTYList(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := list.Add(launcher.PTYEntry{ID: "t07-stale", PID: os.Getpid(), Start: "1"}); err != nil {
		t.Fatal(err)
	}

	l2, err := winsvc.NewLauncher(e.os.cfg)
	if err != nil {
		t.Fatal(err)
	}
	sctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	t.Cleanup(func() { cancel(); <-served })
	go func() { defer close(served); _ = l2.Serve(sctx) }()

	want := []launcher.PTYEntry{{ID: spec.ID, PID: sp.PID, Start: sp.Start}}
	if got := l2.Adopted(); len(got) != 1 || got[0] != want[0] {
		t.Fatalf("adopted = %v, want %v", got, want)
	}
	if list, _ := launcher.OpenPTYList(statePath); len(list.Entries()) != 1 || list.Entries()[0] != want[0] {
		t.Errorf("list after the restart = %v, want %v (the stale entry dropped)", list.Entries(), want)
	}
	// The new launcher serves, on the same pipe.
	spec2 := e.spec("t07-after")
	sp2, err := e.client.Spawn(ctx, spec2)
	if err != nil {
		t.Fatalf("the new launcher does not start terminals: %v", err)
	}
	t.Cleanup(func() { _ = e.sys.KillTree(context.Background(), sp2.ProcID, nil) })

	for _, p := range []struct {
		id string
		sp terminal.Spawned
	}{{spec.ID, sp}, {spec2.ID, sp2}} {
		if err := e.sys.KillTree(ctx, p.sp.ProcID, nil); err != nil {
			t.Fatal(err)
		}
		e.waitEnded(t, ctx, p.id)
	}
}
