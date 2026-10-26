//go:build full

package localchan_test

import (
	"bufio"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/terminal"
)

// The residual M3/M4 of the M3 audit, round 3: what the caps of the server
// count by is read of the real peer at accept time. A live caller has its
// user (the UID on Linux, the SID of its token on Windows); a caller that
// had already exited when the engine read it is Gone, and the server
// refuses it before it counts (TestAGoneCallerIsRefusedBeforeTheCaps), not
// left to the cap of its user alone.
func TestThePeerHasItsUserAndAnExitedOneIsGone(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "mlp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "p.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	peers := localchan.NewPeerSource(terminal.NewSystem())

	live, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = live.Close() }()
	c, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	p, err := peers.Peer(c)
	t.Cleanup(p.Close) // a peer that failed to be read may still hold a pidfd
	want := strconv.Itoa(os.Getuid())
	if runtime.GOOS == "windows" {
		u, uerr := user.Current()
		if uerr != nil {
			t.Fatal(uerr)
		}
		want = u.Uid // the SID
	}
	if err != nil || p.Gone || p.PID != os.Getpid() || p.UID != want {
		t.Fatalf("live peer = %+v, %v; want PID %d, user %s, not gone", p, err, os.Getpid(), want)
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The helper connects and the engine accepts while it lives (Windows
	// drops a pending connection whose client is gone); it then exits and
	// is reaped before the engine reads the peer.
	cmd := exec.Command(self, helperDial, sock)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || strings.TrimSpace(line) != "CONNECTED" {
		_ = cmd.Process.Kill()
		t.Fatalf("helper: %q, %v", line, err)
	}
	gone, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gone.Close() }()
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper: %v", err)
	}
	g, err := peers.Peer(gone)
	t.Cleanup(g.Close)
	if !g.Gone {
		t.Fatalf("the peer of a process that exited and was reaped = %+v, %v; want gone", g, err)
	}
}
