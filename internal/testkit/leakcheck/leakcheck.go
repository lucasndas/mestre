// Package leakcheck finds, and ends, the processes a full-suite test left
// alive. The mestre-pty is built to outlive the engine (the terminal survives
// the restart of the engine), so a rig that starts terminals must end them
// when the test is over: the pty and the fake agent under it would otherwise
// live on, orphans of init, until the machine is restarted
// (docs/qualidade/dubles.md#processos-que-o-teste-deixa).
//
// A process is the test's when its command line holds one of the needles: the
// folder of the test (the socket of a terminal lives in it) or the folder of
// the binaries the test binary built. The search reads /proc, so it is Linux
// only; on Windows the Job Object of the mestre-pty ends the tree and Find
// finds nothing.
package leakcheck

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

// Proc is a live process of the user.
type Proc struct {
	PID, PPID int
	Cmdline   string
}

func (p Proc) String() string { return fmt.Sprintf("%d (parent %d) %s", p.PID, p.PPID, p.Cmdline) }

// settle bounds the wait for a killed process to leave the table.
const settle = 10 * time.Second

// Find returns the live processes of the user, other than this one, whose
// command line holds a needle, with their descendants. A needle is a folder:
// it matches only as a whole path element (the folders of MkdirTemp differ
// only in the digits at the end, so /tmp/x12 must not match /tmp/x123). An
// empty needle is ignored (it would match everything).
func Find(needles ...string) []Proc { return find(needles) }

// Kill ends the processes of ps (SIGKILL) and waits until they are gone.
// It returns the ones that are still there.
func Kill(ps []Proc) []Proc {
	kill(ps)
	clk := clock.Real{}
	deadline := clk.NewTimer(settle)
	defer deadline.Stop()
	tick := clk.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		left := alive(ps)
		if len(left) == 0 {
			return nil
		}
		select {
		case <-tick.C():
		case <-deadline.C():
			return left
		}
	}
}

// Reap ends whatever is left of the test in the folders: the cleanup of a rig
// that starts terminals, run after its engines stopped. It returns what it
// had to end.
func Reap(dirs ...string) []Proc {
	var all []Proc
	// A process forked between the reading and the kill is found by the next
	// round.
	for range 5 {
		ps := Find(dirs...)
		if len(ps) == 0 {
			break
		}
		all = append(all, ps...)
		Kill(ps)
	}
	return all
}

func list(ps []Proc) string {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString("  " + p.String() + "\n")
	}
	return b.String()
}

// Main is the guard of a TestMain: after m.Run, it fails the binary (exit code
// 1) when a process that holds one of the needles (the folder of the binaries
// the test binary built, which no other test binary shares) is still alive,
// naming it, and ends it. Call it with the code m.Run returned.
func Main(w io.Writer, code int, needles ...string) int {
	ps := Find(needles...)
	if len(ps) == 0 {
		return code
	}
	Kill(ps)
	fmt.Fprintf(w, "FAIL: the tests left %d process(es) alive (a rig must end the terminals it starts):\n%s", len(ps), list(ps))
	return 1
}
