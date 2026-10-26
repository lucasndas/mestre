//go:build full

package clocksock_test

import (
	"bufio"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/testkit/clocksock"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// follower is a process that follows the clock: it answers each line with the
// given answers, in order, and leaves when it runs out of them.
func follower(t *testing.T, path string, answers ...string) {
	t.Helper()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	r := bufio.NewReader(c)
	go func() {
		defer c.Close()
		for _, a := range answers {
			if _, err := r.ReadString('\n'); err != nil {
				return
			}
			_, _ = c.Write([]byte(a + "\n"))
		}
	}()
}

func waitPeers(t *testing.T, s *clocksock.Server, n int) {
	t.Helper()
	for range 500 {
		if s.Peers() == n {
			return
		}
		if err := clock.Sleep(t.Context(), clock.Real{}, 10*time.Millisecond); err != nil { // real sockets: the peer registers by itself
			t.Fatal(err)
		}
	}
	t.Fatalf("%d peers, want %d", s.Peers(), n)
}

// The test goes on only when every process applied the time: a process that
// leaves or answers something else is an error of Advance, and is dropped.
// CA-DUBLES-12
func TestAdvanceReturnsTheErrorOfAProcessThatDoesNotApplyTheTime(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "clock.sock")
	start := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	s, err := clocksock.Start(path, start)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	follower(t, path, "ok x", "ok x", "ok x", "ok x") // the set, then three advances
	waitPeers(t, s, 1)
	if err := s.Advance(time.Hour); err != nil {
		t.Fatalf("one process that answers ok: %v", err)
	}
	if got := s.Now(); !got.Equal(start.Add(time.Hour)) {
		t.Fatalf("Now = %v", got)
	}

	follower(t, path, "ok x") // answers the set and leaves
	waitPeers(t, s, 2)
	err = s.Advance(time.Minute)
	if err == nil || !strings.Contains(err.Error(), "did not apply advance 1m0s") {
		t.Fatalf("a process that left: %v", err)
	}
	if s.Peers() != 1 {
		t.Fatalf("%d peers after the one that left, want 1", s.Peers())
	}

	follower(t, path, "ok x", "error no clock") // the set, then a refusal
	waitPeers(t, s, 2)
	if err := s.Advance(time.Minute); err == nil || !strings.Contains(err.Error(), "error no clock") {
		t.Fatalf("a process that refuses: %v", err)
	}
	if s.Peers() != 1 {
		t.Fatalf("%d peers after the one that refused, want 1", s.Peers())
	}
}
