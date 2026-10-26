//go:build full

package terminal_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/terminal"
	"github.com/mestre-ia/mestre/internal/terminal/ptywire"
	"github.com/mestre-ia/mestre/internal/testkit/delegated"
)

// readPIDs reads the PIDs the tree helper wrote, one per line.
func readPIDs(t *testing.T, path string) []int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var pids []int
	for _, f := range strings.Fields(string(b)) {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatal(err)
		}
		pids = append(pids, n)
	}
	return pids
}

// CA-TERM-05 (the parts with a delegated cgroup run again in a systemd scope)
func TestStopLeavesNothingOfTheTree(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		id     string // cgroups are named after it and the subtests share the base
		escape bool   // also a grandchild that left the session (setsid -f)
	}{
		{"a grandchild that ignores SIGTERM", "t5a", false},
		{"and one that left the session", "t5b", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.escape {
				if !canLeaveSession {
					t.Skip("a process cannot leave the Job Object of the terminal on Windows: nothing to test")
				}
				// A grandchild that left the session only dies with the
				// cgroup of the terminal: this process has no delegated one,
				// so the test runs again in a systemd scope that has.
				if _, err := terminal.DelegatedCgroup(); err != nil {
					delegated.Rerun(t, envBuilds+"="+builds.dir)
					return
				}
			}
			h := newHarness(t, harnessOptions{})
			pidsFile := filepath.Join(h.cfg.Root, "pids")
			flag := "0"
			if tt.escape {
				flag = "1"
			}
			term := h.create(h.selfSpec(tt.id, helperTree, pidsFile, flag))
			rec := h.record(tt.id)
			out, _ := h.observe(rec)
			out.waitFor(t, "READY")
			grandchildren := readPIDs(t, pidsFile)
			ptyPID, agentPID := term.PTY().PID, term.AgentPID()

			// The agent and its grandchildren ignore SIGTERM, so the
			// termination reaches step 3.
			step, err := term.Stop(h.ctx, terminal.StopPlan{TerminateWait: 300 * time.Millisecond})

			if err != nil {
				t.Fatalf("Stop: %v", err)
			}
			if runtime.GOOS == "linux" && step != terminal.StepForced {
				t.Fatalf("ended at step %v, want the forced step", step)
			}
			h.gone(ptyPID, "mestre-pty")
			h.gone(agentPID, "agent")
			for _, pid := range grandchildren {
				h.gone(pid, "grandchild")
			}
			if h.cfg.CgroupBase != "" {
				if _, err := os.Stat(filepath.Join(h.cfg.CgroupBase, "term-"+tt.id)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("the cgroup of the terminal still exists: %v", err)
				}
			}
			if _, err := os.Stat(rec.SocketPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("the socket is still there: %v", err)
			}
			if got := h.record(tt.id).Status; got != terminal.StatusStopped {
				t.Fatalf("status = %q, want stopped", got)
			}
		})
	}
}

// The agent ends on its own and leaves a process behind (nohup, a job in the
// background): the engine sweeps the terminal, marks its row interrupted,
// removes the socket and tells whoever keeps the terminal by id (OnEnded). The
// process left behind would otherwise go on in the session or the cgroup of
// the terminal and still identify as the character on the local channel.
//
// CA-TERM-13
func TestWhatTheAgentLeavesBehindEndsWithIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	const id = "t5c"
	ended := make(chan struct{})
	endedSignals.Store(id, ended)
	pidsFile := filepath.Join(h.cfg.Root, "pids")
	term := h.create(h.selfSpec(id, helperLeaver, pidsFile))
	rec := h.record(id)
	ptyPID := term.PTY().PID

	select {
	case <-ended:
	case <-h.ctx.Done():
		t.Fatal("the engine never reported the end of the agent")
	}
	for _, pid := range readPIDs(t, pidsFile) {
		h.gone(pid, "the process the agent left behind")
	}
	h.gone(ptyPID, "mestre-pty")
	if got := h.record(id).Status; got != terminal.StatusInterrupted {
		t.Errorf("status = %q, want interrupted", got)
	}
	if _, err := os.Stat(rec.SocketPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the socket is still there: %v", err)
	}
}

// CA-TERM-06
func TestLogMasksTheTokenAndTheBufferKeepsTheBytes(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	const token = "ghp_0123456789abcdefghijklmnopqrstuvwxyz"
	term := h.create(h.selfSpec("t6", helperPrinter, "deploy with "+token))
	out, _ := h.observe(h.record("t6"))
	out.waitFor(t, token)

	if err := term.FlushLog(h.ctx); err != nil {
		t.Fatal(err)
	}

	log, err := os.ReadFile(filepath.Join(h.cfg.Logs, "t6.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "deploy with [segredo]") || strings.Contains(string(log), token) {
		t.Fatalf("log = %q, want the token replaced by [segredo]", log)
	}
	got, err := term.Replay(h.ctx, 0)
	if err != nil || !strings.Contains(string(got.Data), token) {
		t.Fatalf("buffer = %q, %v, want the bytes as they came out", got.Data, err)
	}
}

// CA-TERM-08, on Linux and on Windows: the engine is a process of its own
// here, killed with SIGKILL (TerminateProcess), and the new engine is the
// Manager of the test. On the CI the mestre-pty runs as the account of the
// test, without a launcher.
func TestEngineKilledAndStartedAgainFindsTheAgentWhereItWas(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	spec := h.agentSpec("t8")
	req, err := json.Marshal(map[string]any{"Config": h.cfg, "Spec": spec})
	if err != nil {
		t.Fatal(err)
	}
	reqFile := filepath.Join(h.cfg.Root, "engine.json")
	if err := os.WriteFile(reqFile, req, 0o600); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	engine := exec.Command(self, helperEngine, reqFile)
	stdout, err := engine.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := engine.StdinPipe() // held open: the engine ends only by being killed
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	// Its output goes to a file: a process that holds the pipes of the test
	// keeps `go test` waiting for it.
	engineLog, err := os.Create(filepath.Join(h.cfg.Root, "engine.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer engineLog.Close()
	engine.Stderr = engineLog
	if err := engine.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Process.Kill() })
	var ptyPID, agentPID int
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("the engine did not report: %v", err)
	}
	f := strings.Fields(line)
	if len(f) != 3 || f[0] != "READY" {
		t.Fatalf("engine said %q", line)
	}
	ptyPID, _ = strconv.Atoi(f[1])
	agentPID, _ = strconv.Atoi(f[2])

	rec := h.record("t8")
	out, c := h.observe(rec)
	out.waitFor(t, ">")
	before, err := c.Screen(h.ctx, 0)
	if err != nil {
		t.Fatal(err)
	}

	// The engine dies without any warning.
	if err := engine.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = engine.Wait()
	if !terminal.Alive(h.sys, rec.PTY) {
		t.Fatal("the mestre-pty died with the engine")
	}
	if _, err := h.sys.Identify(agentPID); err != nil {
		t.Fatalf("the agent died with the engine: %v", err)
	}

	h.open() // the new engine
	rep, err := h.mgr.Reconcile(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Reconnected) != 1 || len(rep.Interrupted) != 0 || len(rep.Killed) != 0 {
		t.Fatalf("report = %+v, want just the reconnected terminal", rep)
	}
	term := rep.Reconnected[0]
	h.track(term)

	st, err := term.Status(h.ctx)
	if err != nil || st.AgentPID != agentPID || term.PTY().PID != ptyPID {
		t.Fatalf("agent PID %d (was %d), mestre-pty %d (was %d), %v: want the same processes", st.AgentPID, agentPID, term.PTY().PID, ptyPID, err)
	}
	after, err := term.Screen(h.ctx, 0)
	if err != nil || after.Text != before.Text || after.Col != before.CurCol || after.Row != before.CurRow {
		t.Fatalf("screen after = %q at %d,%d, before = %q at %d,%d (%v)", after.Text, after.Col, after.Row, before.Text, before.CurCol, before.CurRow, err)
	}
	if got := h.record("t8").Status; got == terminal.StatusInterrupted {
		t.Fatalf("the terminal passed through %q", got)
	}
	// A keystroke after the return reaches the agent.
	if err := term.Input(h.ctx, []byte("/fake-dump\r")); err != nil {
		t.Fatal(err)
	}
	out.waitFor(t, `"provider":"claude"`)
	if err := term.FlushLog(h.ctx); err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile(filepath.Join(h.cfg.Logs, "t8.log"))
	if err != nil || !strings.Contains(string(log), `"provider":"claude"`) {
		t.Fatalf("the new engine's log lacks the output after the return: %q, %v", log, err)
	}
}

// CA-TERM-09
func TestReconciliationOfTheThreeCases(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	ctx := h.ctx
	alive := h.create(h.agentSpec("alive"))
	noPID := h.create(h.agentSpec("nopid"))
	dead := h.create(h.agentSpec("dead"))
	reused := h.create(h.agentSpec("reused"))
	orphan := h.create(h.agentSpec("orphan"))
	// The engine goes away (the processes of the terminals stay); what
	// happens to them from here on happens with no engine to notice.
	for _, tm := range []*terminal.Terminal{alive, noPID, dead, reused, orphan} {
		tm.Detach()
	}

	// A fourth live one whose row has no PID yet: the engine fell between
	// starting the mestre-pty and recording it.
	exec1 := func(q string, args ...any) {
		t.Helper()
		if _, err := h.db.Writer().ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec1(`UPDATE terminals SET pty_pid = NULL, pty_started = NULL WHERE id = 'nopid'`)
	// One whose mestre-pty died.
	if err := h.sys.KillTree(ctx, dead.PTY(), []int{dead.AgentPID()}); err != nil {
		t.Fatal(err)
	}
	// One whose PID now belongs to an unrelated process, started at another time.
	if err := h.sys.KillTree(ctx, reused.PTY(), []int{reused.AgentPID()}); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	innocent := exec.Command(self, helperSleeper)
	if err := innocent.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = innocent.Process.Kill(); _ = innocent.Wait() }()
	exec1(`UPDATE terminals SET pty_pid = ?, pty_started = 'another-start-time' WHERE id = 'reused'`, innocent.Process.Pid)
	// And a live mestre-pty with no row at all (a restored backup).
	orphanPTY, orphanAgent := orphan.PTY().PID, orphan.AgentPID()
	exec1(`DELETE FROM terminals WHERE id = 'orphan'`)

	// The engine restarts.
	h.open()
	rep, err := h.mgr.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var reconnected []string
	for _, tm := range rep.Reconnected {
		h.track(tm)
		reconnected = append(reconnected, tm.ID())
		if scr, err := tm.Screen(ctx, 0); err != nil || !strings.HasSuffix(scr.Text, ">") {
			t.Errorf("terminal %s: screen %q, %v", tm.ID(), scr.Text, err)
		}
	}
	if strings.Join(sorted(reconnected), ",") != "alive,nopid" {
		t.Fatalf("reconnected = %v, want alive and nopid", reconnected)
	}
	if strings.Join(sorted(rep.Interrupted), ",") != "dead,reused" {
		t.Fatalf("interrupted = %v, want dead and reused", rep.Interrupted)
	}
	if strings.Join(rep.Killed, ",") != "orphan" {
		t.Fatalf("killed = %v, want orphan", rep.Killed)
	}
	h.gone(orphanPTY, "the mestre-pty without a row")
	h.gone(orphanAgent, "the agent without a row")
	if _, err := h.sys.Identify(innocent.Process.Pid); err != nil {
		t.Fatalf("the process that reused the PID was touched: %v", err)
	}
	if rec := h.record("nopid"); rec.PTY.PID == 0 || rec.PTY.Start == "" {
		t.Fatalf("the PID of the row that had none was not recorded: %+v", rec.PTY)
	}
	for _, id := range []string{"dead", "reused"} {
		if got := h.record(id).Status; got != terminal.StatusInterrupted {
			t.Fatalf("%s status = %q, want interrupted", id, got)
		}
	}
	if _, err := os.Stat(filepath.Join(h.cfg.Sockets, "orphan.sock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the orphan socket is still there: %v", err)
	}
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// CA-TERM-10: the terminal is born in the "previous version" of the
// mestre-pty (protocol 1, version 0.9.0: no ping, no history on request).
func TestEngineOfANewVersionServesATerminalOfThePreviousOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{version: versionPrevious})
	ctx := h.ctx
	old := h.create(h.agentSpec("old"))
	out, _ := h.observe(h.record("old"))
	out.waitFor(t, ">")
	if old.PTYVersion() != versionPrevious || h.record("old").PTYVersion != versionPrevious {
		t.Fatalf("born with %q / %q, want %s", old.PTYVersion(), h.record("old").PTYVersion, versionPrevious)
	}
	if err := old.Ping(ctx); !errors.Is(err, terminal.ErrUnsupported) {
		t.Fatalf("Ping on the previous version = %v, want ErrUnsupported", err)
	}
	old.Detach()

	// The engine is updated: it creates terminals of the current version.
	h.cfg.Version = versionCurrent
	h.open()
	rep, err := h.mgr.Reconcile(ctx)
	if err != nil || len(rep.Reconnected) != 1 {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	old = rep.Reconnected[0]
	h.track(old)
	scr, err := old.Screen(ctx, 5)
	if err != nil || !strings.HasSuffix(scr.Text, ">") || scr.History != "" {
		t.Fatalf("screen = %q, history %q, %v", scr.Text, scr.History, err)
	}
	if err := old.Input(ctx, []byte("/fake-dump\r")); err != nil {
		t.Fatal(err)
	}
	out.waitFor(t, `"provider":"claude"`)
	// Ctrl-C twice is how the fake agent exits (the graceful step).
	step, err := old.Stop(ctx, terminal.StopPlan{Graceful: func(ctx context.Context, t *terminal.Terminal) error {
		return t.Input(ctx, []byte("\x03\x03"))
	}})
	if err != nil || step != terminal.StepGraceful {
		t.Fatalf("Stop = %v, %v, want the graceful step", step, err)
	}

	fresh := h.create(h.agentSpec("fresh"))
	if fresh.PTYVersion() != versionCurrent || h.record("fresh").PTYVersion != versionCurrent {
		t.Fatalf("the new terminal was born with %q, want %s", fresh.PTYVersion(), versionCurrent)
	}
	if err := fresh.Ping(ctx); err != nil {
		t.Fatalf("Ping on the current version: %v", err)
	}
}

// CA-TERM-11
func TestOnlyTheEngineWithTheSecretGetsIn(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	ctx := h.ctx
	term := h.create(h.agentSpec("t11"))
	rec := h.record("t11")
	out, _ := h.observe(rec)
	out.waitFor(t, ">")
	before, err := term.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// A process that does not know the secret sends keystrokes at once.
	intruder, err := net.Dial("unix", rec.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	raw := ptywire.NewConn(intruder)
	_ = raw.WriteMsg(&ptywire.Msg{T: ptywire.TypeInput, ID: 1, Data: []byte("/fake-dump\r")})
	if it, err := raw.Read(); err == nil {
		t.Fatalf("the intruder got an answer: %+v", it)
	}
	// A false engine goes through the handshake with a proof made from the
	// wrong secret.
	fake, err := net.Dial("unix", rec.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	raw2 := ptywire.NewConn(fake)
	_ = raw2.WriteMsg(&ptywire.Msg{T: ptywire.TypeHello, Proto: ptywire.ProtoVersion, Nonce: "AAAAAAAAAAAAAAAAAAAAAA=="})
	if it, err := raw2.Read(); err != nil || it.Msg.T != ptywire.TypeHello {
		t.Fatalf("hello answer = %+v, %v", it, err)
	}
	_ = raw2.WriteMsg(&ptywire.Msg{T: ptywire.TypeAuth, Proof: "d3Jvbmcgc2VjcmV0"})
	if it, err := raw2.Read(); err == nil {
		t.Fatalf("the false engine got an answer: %+v", it)
	}

	// Both were refused and registered by the mestre-pty; the server closes
	// the connection only after writing the entry.
	ptyLog, err := os.ReadFile(filepath.Join(h.cfg.Logs, "t11.pty.log"))
	if err != nil || strings.Count(string(ptyLog), "terminal handshake refused") != 2 {
		t.Fatalf("mestre-pty log (%v):\n%s", err, ptyLog)
	}
	// Nothing reached the agent: it printed nothing and the screen is the same.
	after, err := term.Status(ctx)
	if err != nil || after.End != before.End {
		t.Fatalf("output offset %d -> %d (%v): the agent reacted to something", before.End, after.End, err)
	}
	if strings.Contains(out.String(), `"provider"`) {
		t.Fatal("the keystrokes of the intruder were typed")
	}

	// Linux: another process of the same account (this one) cannot read the
	// mestre-pty's environment, attach to it or take its descriptors.
	// Windows has the process security descriptor of the launcher instead,
	// which only a mestre-pty started by it has: that is checked in
	// internal/launcher (CA-TERM-07).
	checkHardened(t, term.PTY().PID)
}
