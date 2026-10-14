//go:build linux

package hook_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/lucasndas/mestre/internal/hook"
)

// nextTick returns once the change time the system gives new files has moved
// on (the kernel's clock for files is coarse): files made after it arrive
// after the ones made before. No sleep: it makes files until one differs.
func nextTick(t *testing.T) {
	t.Helper()
	probe := t.TempDir()
	ctime := func(i int) syscall.Timespec {
		p := filepath.Join(probe, string(rune('a'+i%26))+string(rune('a'+i/26%26)))
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		return st.Sys().(*syscall.Stat_t).Ctim
	}
	first := ctime(0)
	for i := 1; i < 100_000; i++ {
		if ctime(i) != first {
			return
		}
	}
	t.Fatal("the change time of the files never moved")
}

// CA-HOOK-08
// Forged names do not push the real decisions out of the queue: the limit
// drops the oldest by when the file arrived, not by the time in its name. An
// agent that stamps its files with a time in the future (the name is the one
// thing it controls, and the folder is open to every agent of the machine)
// used to sort them last and so keep them while the real, older decisions
// went.
func TestForgedTimestampsDoNotPushTheRealDecisionsOutOfTheQueue(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const forged = 9_000_000_000_000_000_000 // far past any real time in ns
	for n := range 3 {
		queueFile(t, dir, forged+n, "d", decisionRecord(900+n))
	}
	nextTick(t)
	queueFile(t, dir, 1, "d", decisionRecord(1))
	queueFile(t, dir, 2, "d", decisionRecord(2))
	var got []string
	n, err := hook.Drain(dir, 3, farFuture, slog.New(slog.DiscardHandler), func(r hook.QueueRecord) { got = append(got, r.Event.TurnID) })
	if err != nil || n != 3 {
		t.Fatalf("drained %d %v (%v); want 3 (the limit)", n, got, err)
	}
	if !strings.Contains(strings.Join(got, ","), "1,2") || got[len(got)-2] != "1" || got[len(got)-1] != "2" {
		t.Fatalf("kept %v; want the two real decisions (the newest by arrival) and the forged one that arrived last before them", got)
	}
}
