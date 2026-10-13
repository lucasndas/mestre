//go:build full

package localchan_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/testkit"
)

// CA-LOCAL-07, on Linux and on Windows: the engine and the CLI are real, the
// fake agent in a real terminal calls `mestre list` through the socket and
// gets the list, and the engine registered the request with the agent_id it
// verified: on Linux by the process tree (this machine's terminals have no
// delegated cgroup here; the cgroup is CA-LOCAL-11), on Windows by the Job
// Object, asked of the mestre-pty.
func TestAFakeAgentCallsListThroughTheSocket(t *testing.T) {
	t.Parallel()
	e := newFullEngine(t, engineOptions{mode: localchan.ModeTree})
	const agent = "char-7"
	work := filepath.Join(e.root, "work")
	cfg := filepath.Join(e.root, "cfg")
	script := scriptFile(t, e.root, builds.mestre+" list")
	testkit.WriteFiles(t, e.root, map[string]string{
		"work/.git/HEAD":        "ref: refs/heads/main\n",
		"cfg/.credentials.json": "{}",
		"cfg/.claude.json":      fmt.Sprintf(`{"numStartups": 3, "projects": {%q: {"hasTrustDialogAccepted": true}}}`, work),
	})
	env := e.env(agent, "HOME="+e.root, "CLAUDE_CONFIG_DIR="+cfg, "FAKEAGENT_SCRIPT="+script)
	term := e.spawn("t7", agent, builds.fakeagent, []string{"--provider", "claude"}, env, work)
	out := e.watch("t7")
	out.waitFor(t, ">")

	if err := term.Input(e.ctx, []byte("go\r")); err != nil {
		t.Fatal(err)
	}
	got := e.nextCall("the fake agent runs `mestre list`")
	if !got.Caller.Identified || got.Caller.AgentID != agent || got.Caller.TerminalID != "t7" {
		t.Fatalf("the engine registered %+v", got.Caller)
	}
	out.waitFor(t, "the list") // the answer reached the agent, on its screen
}

// CA-LOCAL-13 and CA-CLI-CONV-03 have the "outside process" case in common:
// a process that has the id of a character but is in none of its terminals
// is refused with 3 and not_identified, whichever system it runs on. The
// processes inside the terminal are CA-LOCAL-11 (Linux) and CA-LOCAL-13
// (Windows); this runs on both.
//
// CA-CLI-CONV-03
func TestAnOutsideProcessWithTheIdOfACharacterIsRefused(t *testing.T) {
	t.Parallel()
	e := newFullEngine(t, engineOptions{mode: localchan.ModeTree})
	const agent = "char-3"
	base := filepath.Join(e.root, "calls")
	for _, m := range []string{"direct"} {
		if err := os.MkdirAll(filepath.Join(base, m), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	e.spawn("t3", agent, self, []string{helperCaller, builds.mestre, base, "direct"}, e.env(agent), e.root)
	out := e.watch("t3")
	out.waitFor(t, "DONE")
	if c := e.nextCall("the process inside the terminal"); !c.Caller.Identified || c.Caller.TerminalID != "t3" {
		t.Fatalf("inside: %+v", c.Caller)
	}

	exit, stdout := e.outside(agent)
	var v struct {
		OK    bool
		Error struct{ Code string }
	}
	if err := json.Unmarshal([]byte(stdout), &v); err != nil {
		t.Fatalf("stdout %q: %v", stdout, err)
	}
	if exit != 3 || v.OK || v.Error.Code != "not_identified" {
		t.Fatalf("outside: exit %d, %s", exit, stdout)
	}
	if a := e.nextRefusal("the outside process"); a.AgentID != agent {
		t.Fatalf("audit = %+v", a)
	}
	e.noMoreCalls()
}

// Listen applies the access to the socket it creates, replaces the socket
// an engine that died left, and refuses to take the one of a running
// engine. The mode and owner on Linux are TestLinuxAccessSetsTheMode; the
// ACL on Windows is internal/winsvc's.
func TestListenAppliesTheAccess(t *testing.T) {
	t.Parallel()
	root, err := os.MkdirTemp("", "ml")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	path := filepath.Join(root, "run", "cli.sock")
	acc := &recordingAccess{}

	ln, err := localchan.Listen(path, acc)
	if err != nil {
		t.Fatal(err)
	}
	if got := acc.applied(); len(got) != 1 || got[0] != path {
		t.Fatalf("access applied to %v", got)
	}
	c, err := localchan.UnixDialer(path)(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	if _, err := localchan.Listen(path, acc); err == nil {
		t.Fatal("took the socket of a running engine")
	}
	_ = ln.Close()

	// A socket file left by an engine that died is replaced.
	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = stale.Close()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("test bug: no stale socket: %v", err)
	}
	ln, err = localchan.Listen(path, acc)
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close()

	failing := localchan.Access(failingAccess{})
	if _, err := localchan.Listen(path, failing); err == nil {
		t.Fatal("an access that failed was ignored")
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the socket of a failed Listen is still there: %v", err)
	}
}

type recordingAccess struct {
	mu    sync.Mutex
	paths []string
}

func (r *recordingAccess) Apply(p string) error {
	r.mu.Lock()
	r.paths = append(r.paths, p)
	r.mu.Unlock()
	return nil
}

func (r *recordingAccess) applied() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.paths...)
}

type failingAccess struct{}

func (failingAccess) Apply(string) error { return errors.New("no way") }
