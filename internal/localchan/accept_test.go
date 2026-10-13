package localchan_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/testkit/localrig"
	"github.com/lucasndas/mestre/pkg/clock"
)

// lateReader is a PeerSource that takes time to read the peer, as the real
// one does (SIO_AF_UNIX_GETPEERPID, then OpenProcess): the fake clock moves
// while it works, and the process "started" at the end of it.
type lateReader struct {
	clk       *clock.Fake
	startedIn func(accepted time.Time) time.Time
}

func (l lateReader) Peer(net.Conn) (localchan.Peer, error) {
	accepted := l.clk.Now()
	l.clk.Advance(time.Second)
	return localchan.Peer{PID: 4242, Start: "133000000000000000", StartedAt: l.startedIn(accepted)}, nil
}

// The accept time is taken by the server before the peer is read, so a
// process that started while the peer was being read (a PID reused after the
// accept) is refused. Taking the time inside the identifier, after the read,
// made the check never fire.
func TestAProcessStartedAfterTheAcceptIsNotIdentified(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		startedIn func(accepted time.Time) time.Time
		ok        bool
	}{
		{"before the accept", func(a time.Time) time.Time { return a.Add(-time.Minute) }, true},
		{"after the accept", func(a time.Time) time.Time { return a.Add(time.Millisecond) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			clk := clock.NewFake(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))
			terms := &localrig.FakeTerminals{}
			terms.Add("tA", "alice", 0)
			jobs := &localrig.FakeJobs{}
			jobs.Put("tA", localchan.ProcRef{PID: 4242, Start: "133000000000000000"}, true)
			r := localrig.New(clk, localrig.Options{Config: localchan.Config{Identifier: &localchan.JobIdentifier{
				Peers: lateReader{clk, tc.startedIn}, Terminals: terms, Jobs: jobs,
			}}})
			r.Forwarder.Handle("list", localchan.HandlerFunc(func(context.Context, *localchan.Call) localchan.Response {
				return localchan.OK(map[string]any{})
			}))
			r.Start(t)
			resp := final(t, rawCall(t, r, "whoever", `{"v":1,"id":"x","agent_id":"alice","cmd":"list","args":[],"flags":{},"cwd":""}`))
			if resp.OK != tc.ok {
				t.Fatalf("ok = %v, want %v (%+v)", resp.OK, tc.ok, resp.Error)
			}
			if !tc.ok && resp.Error.Code != localchan.CodeNotIdentified {
				t.Fatalf("code = %q", resp.Error.Code)
			}
		})
	}
}
