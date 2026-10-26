package hook_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/pkg/clock"
)

func decisionRecord(n int) hook.QueueRecord {
	return hook.QueueRecord{V: hook.QueueVersion, Kind: hook.QueuedDecision, Event: providers.Event{
		Version: providers.EventVersion, Event: providers.EventPreTool, AgentID: agent, TurnID: fmt.Sprint(n),
		Tool: &providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: "npm test"}},
		Decision: &hook.QueuedVerdict{Verdict: classes.Allow}}
}

// What an agent can leave in the queue never stops the drain: a folder with
// the name of a record (with a link inside, not followed when it goes), a
// FIFO and a hard link of a file outside (Unix) are removed, the files
// outside stay, and the valid records around them are read, every time.
func TestWhatAnAgentLeavesInTheQueueNeverStopsTheDrain(t *testing.T) {
	t.Parallel()
	dir, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "segredo")
	if err := os.WriteFile(secret, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(dir, fmt.Sprintf("%020d-%016x-d.json", 0, 0))
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(outside, filepath.Join(folder, "link")); err != nil {
			t.Fatal(err)
		}
		if err := mkfifo(filepath.Join(dir, fmt.Sprintf("%020d-%016x-i.json", 1, 1))); err != nil {
			t.Fatal(err)
		}
	}
	hard := filepath.Join(dir, fmt.Sprintf("%020d-%016x-i.json", 3, 3))
	if err := os.Link(secret, hard); err != nil {
		t.Fatal(err)
	}
	queueFile(t, dir, 2, "i", info(2))
	for round := range 2 {
		var got []string
		n, err := hook.Drain(dir, hook.MaxQueued, farFuture, slog.New(slog.DiscardHandler), func(r hook.QueueRecord) {
			got = append(got, r.Event.TurnID)
		})
		if round == 0 && (err != nil || n != 1 || strings.Join(got, ",") != "2") {
			t.Fatalf("drained %d %v (%v); want the one valid record", n, got, err)
		}
		if left, _ := os.ReadDir(dir); err != nil || len(left) != 0 {
			t.Fatalf("round %d: left %v (%v)", round, left, err)
		}
		queueFile(t, dir, 4, "i", info(4))
	}
	if _, err := os.Stat(secret); err != nil {
		t.Fatalf("the file outside the queue went: %v", err)
	}
}

// The limit holds for decisions too: a queue of more decisions than the
// limit keeps the newest ones.
func TestTheLimitOfTheQueueHoldsForDecisionsToo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for n := 1; n <= 5; n++ {
		queueFile(t, dir, n, "d", decisionRecord(n))
	}
	var got []string
	n, err := hook.Drain(dir, 3, farFuture, slog.New(slog.DiscardHandler), func(r hook.QueueRecord) { got = append(got, r.Event.TurnID) })
	if err != nil || n != 3 || strings.Join(got, ",") != "3,4,5" {
		t.Fatalf("drained %d %v (%v); want the 3 newest decisions", n, got, err)
	}
}

// syncBuffer is a log the drain writes while the test reads.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// The degraded decisions the dispatcher took alone go to the log of the
// engine when it reads the queue, with the verdict and the character the
// record claims.
func TestTheQueuedDecisionsGoToTheLog(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	queueFile(t, dir, 1, "d", decisionRecord(1))
	queueFile(t, dir, 2, "i", info(2))
	synctest.Test(t, func(t *testing.T) {
		var log syncBuffer
		e := &hook.Engine{Clock: clock.Real{}, Logger: slog.New(slog.NewTextHandler(&log, nil))}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { e.DrainLoop(ctx, dir, nil); close(done) }()
		synctest.Wait()
		cancel()
		<-done
		out := log.String()
		if !strings.Contains(out, "a decision the dispatcher took without the engine") || !strings.Contains(out, "verdict=allow") ||
			!strings.Contains(out, "claimed_agent="+agent) || !strings.Contains(out, "tool=Bash") {
			t.Fatalf("log %q; want the queued decision", out)
		}
	})
}
