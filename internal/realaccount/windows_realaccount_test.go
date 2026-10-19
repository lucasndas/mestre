//go:build realaccount && windows

package realaccount_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/lucasndas/mestre/internal/launcher"
	"github.com/lucasndas/mestre/internal/realaccount"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/store/machine"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/internal/terminal/ptywire"
	"github.com/lucasndas/mestre/internal/vault"
	"github.com/lucasndas/mestre/internal/winsvc"
	"github.com/lucasndas/mestre/internal/winsvc/dpapi"
	"github.com/lucasndas/mestre/internal/winsvc/logonsweep"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The Windows side, part two: the tests. They run as the engine service
// (NT SERVICE\Mestre$ci, no administrator, no SeDebugPrivilege), against the
// mestre-launcher service (LocalSystem) that makes a batch logon of the agent
// account for each terminal.

// A rig is the engine side of the launcher: a terminal manager that asks the
// launcher for the mestre-pty of each terminal.
type rig struct {
	mgr    *terminal.Manager
	client *launcher.Client
	root   string // the state of this rig, the engine's alone
	shared string // where the agents leave their reports
}

func newRig(t *testing.T) *rig {
	t.Helper()
	engineDir := filepath.Join(stage(), "ci")
	root, err := os.MkdirTemp(engineDir, "t")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	shared, err := os.MkdirTemp(filepath.Join(stage(), "work"), "s")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shared) })
	db, err := machine.Open(context.Background(), filepath.Join(root, "maquina.db"), store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	client := winsvc.NewLauncherClient(realaccount.Instance)
	mgr, err := terminal.NewManager(terminal.ManagerConfig{
		DB: db, System: terminal.NewSystem(), Spawner: client, MachineID: "m1",
		SocketDir: filepath.Join(engineDir, "pty"), LogDir: filepath.Join(root, "logs"),
		PTYBinary:  func(v string) (string, error) { return filepath.Join(stage(), "bin", v, "mestre-pty.exe"), nil },
		PTYVersion: realaccount.PTYVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &rig{mgr: mgr, client: client, root: root, shared: shared}
}

// start makes a terminal whose agent is the helper mode args of this binary,
// with the environment of the profile the launcher loaded (no Env: the
// mestre-pty hands its own), and ends it with the test, whatever happened.
func (r *rig) start(t *testing.T, id string, args ...string) *terminal.Terminal {
	t.Helper()
	term, err := r.mgr.Create(ctxFor(t), terminal.CreateSpec{
		ID: id, CharacterID: "c-" + id, Dir: r.root,
		Path: filepath.Join(stage(), "realaccount.test.exe"), Args: append([]string{helperAgent}, args...),
		WorkDir: filepath.Join(stage(), "work"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := clock.WithTimeout(context.Background(), clock.Real{}, 30*time.Second)
		defer cancel()
		_, _ = term.Stop(ctx, terminal.StopPlan{TerminateWait: 300 * time.Millisecond})
		terminatePTY(term)
	})
	return term
}

// terminatePTY ends the mestre-pty, which the engine has the right to.
func terminatePTY(term *terminal.Terminal) {
	if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(term.PTY().PID)); err == nil {
		_ = windows.TerminateProcess(h, 1)
		_ = windows.CloseHandle(h)
	}
}

// ready starts a terminal and waits for its agent to say it is.
func (r *rig) ready(t *testing.T, id string, args ...string) *terminal.Terminal {
	t.Helper()
	term := r.start(t, id, args...)
	observe(ctxFor(t), t, term).waitFor(t, "READY")
	return term
}

// report starts a terminal whose agent writes a report and says DONE, and
// returns the report.
func (r *rig) report(t *testing.T, id string, args ...string) (*terminal.Terminal, map[string]string) {
	t.Helper()
	path := filepath.Join(r.shared, id+".json")
	term := r.start(t, id, append([]string{args[0], path}, args[1:]...)...)
	observe(ctxFor(t), t, term).waitFor(t, "DONE")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rep map[string]string
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("report %q: %v", b, err)
	}
	return term, rep
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

// expectAccess checks the outcome of opening a process for each right: want
// maps a right to "ok" or "denied"; the rights it does not name are all
// denied.
func expectAccess(t *testing.T, what string, got func(name string) string, allowed ...string) {
	t.Helper()
	for _, a := range processAccess {
		want := "denied"
		for _, ok := range allowed {
			if a.name == ok {
				want = "ok"
			}
		}
		if g := got(a.name); g != want {
			t.Errorf("%s: %s = %q, want %q", what, a.name, g, want)
		}
	}
}

// engineMatrix is what the engine may do to a process of a terminal.
func engineMatrix(t *testing.T, what string, pid int, allowed ...string) {
	t.Helper()
	expectAccess(t, what+" (PID "+strconv.Itoa(pid)+") by the engine", func(name string) string {
		for _, a := range processAccess {
			if a.name == name {
				return openResult(pid, a.mask)
			}
		}
		return "?"
	}, allowed...)
}

// Part of CA-TERM-07 (real accounts): the mestre-pty the launcher service starts runs
// as the agent account, in a batch logon session of its own that is not the
// engine's nor another terminal's, outside the Job Object of the engine, with
// the profile the launcher loaded; the engine may query, wait and end it and
// nothing else, and the agent only query it.
func TestWindowsTheMestrePTYRunsAsTheAgentAccountInALogonOfItsOwn(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	me, err := winsvc.CurrentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	if want, _, _, err := windows.LookupSID("", `NT SERVICE\`+winsvc.ServiceName(realaccount.Instance)); err != nil || !me.Equals(want) {
		t.Fatalf("the tests run as %s, not as the virtual account of the engine service (%v)", me, err)
	}
	mine, err := logonsweep.SessionOfSelf()
	if err != nil {
		t.Fatal(err)
	}
	agent := agentSID(t).String()

	// Two terminals, each telling who it is: the agent and, through the right
	// the owner has to query it, its mestre-pty.
	a, ra := r.report(t, newID("wm"), "whoami")
	_, rb := r.report(t, newID("wm"), "whoami")
	batch := strconv.Itoa(logonsweep.LogonTypeBatch)
	for _, p := range []struct {
		what string
		rep  map[string]string
		pfx  string
	}{{"agent of A", ra, ""}, {"mestre-pty of A", ra, "mestre-pty "}, {"agent of B", rb, ""}, {"mestre-pty of B", rb, "mestre-pty "}} {
		if got := p.rep[p.pfx+"user"]; got != agent {
			t.Errorf("%s runs as %q, want the agent account %s", p.what, got, agent)
		}
		if got := p.rep[p.pfx+"logon type"]; got != batch {
			t.Errorf("%s: logon type %q, want a batch logon (%s)", p.what, got, batch)
		}
		if got := p.rep[p.pfx+"logon session"]; got == "" || got == fmt.Sprintf("%#x", uint64(mine.ID)) {
			t.Errorf("%s: logon session %q, the engine's is %#x", p.what, got, uint64(mine.ID))
		}
	}
	if ra["logon session"] != ra["mestre-pty logon session"] || rb["logon session"] != rb["mestre-pty logon session"] {
		t.Errorf("the agent and its mestre-pty are in different logon sessions: %q/%q and %q/%q: one session per terminal",
			ra["logon session"], ra["mestre-pty logon session"], rb["logon session"], rb["mestre-pty logon session"])
	}
	if ra["logon session"] == rb["logon session"] {
		t.Errorf("two terminals share the logon session %s", ra["logon session"])
	}

	// Outside the Job of the engine, which the mestre-pty would be in if the
	// launcher had not created it with CREATE_BREAKAWAY_FROM_JOB.
	if engineJob == 0 {
		t.Log("the engine service could not join a Job Object of its own: the check of the Job is skipped")
	} else {
		if in, err := winsvc.IsProcessInJob(windows.CurrentProcess(), engineJob); err != nil || !in {
			t.Fatalf("the engine is in its Job = %v, %v; the check below proves nothing", in, err)
		}
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(a.PTY().PID))
		if err != nil {
			t.Fatal(err)
		}
		in, err := winsvc.IsProcessInJob(h, engineJob)
		_ = windows.CloseHandle(h)
		if err != nil || in {
			t.Errorf("the mestre-pty is in the Job of the engine = %v, %v: the engine could end it, and its Job would hold it", in, err)
		}
	}

	// The descriptors: the engine queries, waits and ends the mestre-pty; the
	// processes of the agent it queries and waits.
	engineMatrix(t, "mestre-pty", a.PTY().PID, "query", "wait", "terminate")
	engineMatrix(t, "agent", a.AgentPID(), "query", "wait")

	// LoadUserProfile and CreateEnvironmentBlock: the agent sees its own
	// profile and the user hive.
	if want := `\` + realaccount.WindowsAgentUser; !strings.HasSuffix(strings.ToLower(ra["USERPROFILE"]), want) {
		t.Errorf("USERPROFILE of the agent = %q, want the profile of %s", ra["USERPROFILE"], realaccount.WindowsAgentUser)
	}
	for _, k := range []string{"HKCU", "profile folder"} {
		if ra[k] != "ok" {
			t.Errorf("%s: %q, want the profile loaded", k, ra[k])
		}
	}
}

// Part of CA-TERM-12 (real accounts): two terminals with distinct logons of the same
// account: a process of one cannot open the agent processes of the other, nor
// ask the mestre-pty of the other for more than to be queried; it has the run
// of its own terminal; the engine queries and waits and does no more.
func TestWindowsAProcessOfOneTerminalCannotOpenThoseOfAnother(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	pids := filepath.Join(r.shared, "pids")
	victim := r.ready(t, newID("wv"), "tree", pids, "0")
	grand := readPIDs(t, pids)
	if len(grand) != 1 {
		t.Fatalf("the agent reported %v, want one grandchild", grand)
	}
	prober, rep := r.report(t, newID("wp"), "access",
		"B pty="+strconv.Itoa(victim.PTY().PID), "B agent="+strconv.Itoa(victim.AgentPID()), "B child="+strconv.Itoa(grand[0]))
	for _, c := range []struct {
		name    string
		allowed []string
	}{
		{"B agent", nil}, // the default DACL of the token names its own logon SID, not the account
		{"B child", nil},
		{"B pty", []string{"query", "wait"}}, // the owner of the mestre-pty may query it, nothing else
		{"own mestre-pty", []string{"query", "wait"}},
		{"own child", []string{"query", "wait", "read memory", "write memory", "duplicate handles", "create thread", "terminate", "change the DACL"}},
	} {
		expectAccess(t, c.name+" by a process of the other terminal", func(a string) string { return rep[c.name+"/"+a] }, c.allowed...)
	}

	engineMatrix(t, "agent of terminal B", victim.AgentPID(), "query", "wait")
	engineMatrix(t, "grandchild of terminal B", grand[0], "query", "wait")
	engineMatrix(t, "agent of the other terminal", prober.AgentPID(), "query", "wait")
	engineMatrix(t, "mestre-pty of terminal B", victim.PTY().PID, "query", "wait", "terminate")
}

// The sockets and folders of the engine, as the agent account sees them from
// a terminal (docs/arquitetura/windows.md#acls-do-socket-da-cli-e-da-pasta-pty):
// the socket of another terminal and the folder of the sockets are closed to
// it, so are the folder of the engine and the pipe of the launcher; cli.sock
// is open, because the agents call the engine through it.
func TestWindowsTheSocketsAndFoldersOfTheEngineRefuseTheAgentAccount(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	victim := r.ready(t, newID("ws"), "sleeper")
	cli := filepath.Join(r.root, "cli.sock")
	ln, err := net.Listen("unix", cli)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	if err := (winsvc.CLIAccess{AgentAccount: realaccount.WindowsAgentUser}).Apply(cli); err != nil {
		t.Fatal(err)
	}

	_, rep := r.report(t, newID("wa"), "acls", victim.Record().SocketPath, filepath.Dir(victim.Record().SocketPath), cli, filepath.Join(stage(), "ci"))
	for k, want := range map[string]string{
		"socket of another terminal":            "denied",
		"list the folder of the sockets":        "denied",
		"delete the socket of another terminal": "denied",
		"cli.sock":                              "ok",
		"list the folder of the engine":         "denied",
		"pipe of the launcher":                  "denied",
	} {
		if rep[k] != want {
			t.Errorf("%s: %q, want %q", k, rep[k], want)
		}
	}
	// The engine reaches its own: the socket of the terminal (the manager
	// connected to it) and cli.sock.
	c, err := net.Dial("unix", cli)
	if err != nil {
		t.Errorf("the engine cannot connect to its own cli.sock: %v", err)
	} else {
		_ = c.Close()
	}
	if !terminal.Alive(terminal.NewSystem(), victim.PTY()) {
		t.Error("the mestre-pty of the terminal whose socket the agent tried to delete is gone")
	}
}

// wmiTerminal starts a terminal whose agent starts a process through WMI,
// which escapes the Job Object, and returns the connection that process holds
// to the engine (the end of the connection is the death of the process).
func (r *rig) wmiTerminal(t *testing.T) (term *terminal.Terminal, gone <-chan struct{}) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	term = r.start(t, newID("ww"), "wmi", ln.Addr().String())
	out := observe(ctxFor(t), t, term).waitFor(t, "READY")
	if !strings.Contains(out, "WMI 0") {
		t.Fatalf("WMI did not create the process:\n%s", out)
	}
	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			accepted <- c
		}
	}()
	var conn net.Conn
	select {
	case conn = <-accepted:
	case <-ctxFor(t).Done():
		t.Fatal("the process started through WMI never called the engine")
	}
	t.Cleanup(func() { _ = conn.Close() })
	rd := bufio.NewReader(conn)
	line, err := rd.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	pid, start, _ := strings.Cut(strings.TrimSpace(line), " ")
	n, err := strconv.Atoi(pid)
	if err != nil {
		t.Fatalf("the process started through WMI said %q", line)
	}
	// Control: it is outside the Job of the terminal, which is why the Job does
	// not end it.
	in, err := term.InJob(ctxFor(t), terminal.ProcID{PID: n, Start: start})
	if err != nil || in {
		t.Fatalf("the process started through WMI is in the Job of the terminal = %v, %v: the test proves nothing", in, err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = rd.ReadByte() // returns when the process ends and its connection drops
	}()
	return term, done
}

func waitGone(t *testing.T, what string, gone <-chan struct{}) {
	t.Helper()
	limit := clock.Real{}.NewTimer(time.Minute)
	defer limit.Stop()
	select {
	case <-gone:
	case <-limit.C():
		t.Fatalf("%s is still alive a minute after its terminal ended", what)
	}
}

// Part of CA-TERM-05 (real accounts, the logon-session sweep): a process created
// through WMI from inside a terminal is born outside the Job Object, in the
// logon session of the terminal, and the end of the terminal ends it: by the
// forced stop of the mestre-pty itself, by the stop of the engine, and by the
// launcher, when the mestre-pty is killed without sweeping.
func TestWindowsTheEndOfATerminalEndsWhatWMIStartedInIt(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		end  func(t *testing.T, term *terminal.Terminal)
	}{
		{"the forced stop of the mestre-pty", func(t *testing.T, term *terminal.Terminal) {
			if err := term.Client().Stop(ctxFor(t), ptywire.StopKill); err != nil {
				t.Fatal(err)
			}
		}},
		{"the stop of the engine", func(t *testing.T, term *terminal.Terminal) {
			if _, err := term.Stop(ctxFor(t), terminal.StopPlan{TerminateWait: 300 * time.Millisecond}); err != nil {
				t.Fatal(err)
			}
		}},
		{"the mestre-pty killed, which sweeps nothing", func(_ *testing.T, term *terminal.Terminal) { terminatePTY(term) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			term, gone := r.wmiTerminal(t)
			c.end(t, term)
			waitGone(t, "the process started through WMI", gone)
		})
	}
}

// Part of CA-TERM-07 (real accounts, the release): when the mestre-pty ends, the
// launcher unloads the profile. It runs alone (a serial test), because the user
// hive stays loaded while any terminal of the account lives. That the logon
// sessions are closed too is for tools/realaccount to check at the end: only an
// administrator can list the sessions of another account.
//
//nolint:paralleltest // the profile is shared by every terminal of the account: nothing else may be running
func TestWindowsTheProfileAndTheLogonSessionEndWithTheMestrePTY(t *testing.T) {
	sid := agentSID(t).String()
	loaded := func() bool {
		// The hive is the agent account's: the engine is refused it while it is
		// loaded, and does not find it once it is not.
		k, err := registry.OpenKey(registry.USERS, sid, registry.QUERY_VALUE)
		if err == nil {
			_ = k.Close()
		}
		return !errors.Is(err, registry.ErrNotExist)
	}
	if loaded() {
		t.Fatal("the profile of the agent account is loaded before any terminal: something else is running")
	}
	r := newRig(t)
	term, rep := r.report(t, newID("wr"), "whoami")
	id, err := strconv.ParseUint(strings.TrimPrefix(rep["logon session"], "0x"), 16, 64)
	if err != nil {
		t.Fatalf("logon session %q: %v", rep["logon session"], err)
	}
	if rep["logon session"] == "" || id == 0 {
		t.Fatal("the terminal did not say its logon session")
	}
	if !loaded() {
		t.Error("the profile is not loaded while the terminal runs")
	}

	terminatePTY(term)
	ctx := ctxFor(t)
	until(ctx, t, "the profile unloaded", func() bool { return !loaded() })
}

// CA-COFRE-08 (the part that runs in the CI): under the virtual service
// account of the engine, the vault key is protected by machine-scope DPAPI and
// a file for that account alone; the agent account cannot read the file; and
// the vault refuses it as soon as an ACL gives the agent account access, which
// the agent then can use: the refusal is what stands in the way.
func TestWindowsTheVaultKeyIsTheVirtualServiceAccountsAlone(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	dir, err := os.MkdirTemp(filepath.Join(stage(), "work"), "v") // the agent account crosses and lists it
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "cofre.key")
	ctx := ctxFor(t)
	me, err := dpapi.CurrentAccount()
	if err != nil {
		t.Fatal(err)
	}

	keys := vault.NewFileKeyStore(path)
	key := make([]byte, 64)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	if err := keys.Save(ctx, key); err != nil {
		t.Fatalf("the engine cannot create its key file: %v", err)
	}
	if got, err := keys.Load(ctx); err != nil || !bytes.Equal(got, key) {
		t.Fatalf("Load = %d bytes, %v; want the key back through DPAPI", len(got), err)
	}
	if err := dpapi.Verify(path, me); err != nil {
		t.Fatalf("the key file is not protected for %s alone: %v", me, err)
	}

	read := func(id string) string {
		_, rep := r.report(t, newID(id), "readfile", path)
		return rep["read"]
	}
	if got := read("wk"); got != "denied" {
		t.Errorf("the agent account reads the key file = %q, want access denied", got)
	}

	// An ACL that gives the agent account the file: the vault refuses it.
	if out, err := exec.CommandContext(ctx, "icacls", path, "/grant", realaccount.WindowsAgentUser+":R").CombinedOutput(); err != nil {
		t.Fatalf("icacls: %v\n%s", err, out)
	}
	_, err = keys.Load(ctx)
	var bad *vault.UnsafeKeyFileError
	if !errors.As(err, &bad) || bad.Check != "WIN-14" {
		t.Errorf("Load with the agent account in the ACL = %v, want an unsafe key file (WIN-14)", err)
	}
	if got := read("wk"); got != "ok" {
		t.Errorf("with the ACL granting it, the agent account reads the key file = %q: the refusal above protects nothing", got)
	}

	// The fix of the checklist puts it right.
	if err := dpapi.Restrict(path, me); err != nil {
		t.Fatal(err)
	}
	if got, err := keys.Load(ctx); err != nil || !bytes.Equal(got, key) {
		t.Errorf("Load after the fix = %d bytes, %v", len(got), err)
	}
	if got := read("wk"); got != "denied" {
		t.Errorf("after the fix the agent account reads the key file = %q, want access denied", got)
	}
}
