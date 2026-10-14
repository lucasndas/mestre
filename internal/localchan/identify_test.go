package localchan_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/testkit/localrig"
)

// idRig is a ProcIdentifier over a fake /proc, as the engine builds one.
type idRig struct {
	proc  *localrig.FakeProc
	terms *localrig.FakeTerminals
	id    *localchan.ProcIdentifier
}

func newIDRig(mode localchan.Mode) *idRig {
	r := &idRig{proc: &localrig.FakeProc{}, terms: &localrig.FakeTerminals{}}
	r.id = &localchan.ProcIdentifier{Proc: r.proc, Terminals: r.terms, Mode: mode, CgroupBase: "/mestre.service"}
	return r
}

func (r *idRig) identify(p localchan.Peer, agent string) (localchan.Identity, error) {
	return r.id.Identify(context.Background(), p, agent)
}

func wantRefused(t *testing.T, err error, reason string) {
	t.Helper()
	var ie *localchan.IdentifyError
	if !errors.As(err, &ie) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if !strings.Contains(ie.Error(), reason) {
		t.Fatalf("refused for %q, want %q", ie.Error(), reason)
	}
}

// CA-LOCAL-08: a /proc where a caller is in .../term-A, the terminal of
// another character, and another in .../term-B, the one of agent_id.
func TestTheCgroupSaysWhichTerminalCalls(t *testing.T) {
	t.Parallel()
	r := newIDRig(localchan.ModeCgroup)
	r.terms.Add("A", "alice", 0)
	r.terms.Add("B", "bob", 0)
	r.proc.SetCgroup(100, "/mestre.service/term-A")
	r.proc.SetCgroup(200, "/mestre.service/term-B")
	r.proc.SetCgroup(300, "/mestre.service/term-ghost")
	r.proc.SetCgroup(400, "/user.slice/app.scope")
	r.proc.SetCgroup(500, "/elsewhere/term-B")
	peer := func(pid int) localchan.Peer { return localchan.Peer{PID: pid, Pidfd: localrig.NewPidfd(pid)} }

	if _, err := r.identify(peer(100), "bob"); err == nil {
		t.Fatal("a process of alice's terminal was accepted as bob")
	} else {
		wantRefused(t, err, "another character")
	}
	id, err := r.identify(peer(200), "bob")
	if err != nil || id.TerminalID != "B" || id.AgentID != "bob" || id.PID != 200 {
		t.Fatalf("identity = %+v, %v", id, err)
	}
	for _, tt := range []struct {
		name string
		pid  int
		why  string
	}{
		{"a terminal nobody registered", 300, "not registered"},
		{"no terminal cgroup", 400, "not the one of a terminal"},
		{"a term- cgroup outside the launcher's", 500, "is not under"},
		{"unknown process", 999, "read the cgroup"},
	} {
		_, err := r.identify(peer(tt.pid), "bob")
		wantRefused(t, err, tt.why)
		t.Log(tt.name, "->", err)
	}
	if _, err := r.identify(peer(200), ""); !errors.Is(err, localchan.ErrNoAgent) {
		t.Fatalf("a request with no agent_id: %v", err)
	}
}

// CA-LOCAL-09: no pidfd, and a pidfd that stops pointing to the PID after
// /proc was read, are refused even with the right cgroup.
func TestWithoutAPidfdThatStaysTheSameNobodyIsIdentified(t *testing.T) {
	t.Parallel()
	setup := func() *idRig {
		r := newIDRig(localchan.ModeCgroup)
		r.terms.Add("B", "bob", 0)
		r.proc.SetCgroup(200, "/mestre.service/term-B")
		r.proc.SetIdentity(200, "boot:200")
		return r
	}
	t.Run("no pidfd: the caller was reaped before the accept", func(t *testing.T) {
		t.Parallel()
		_, err := setup().identify(localchan.Peer{PID: 200, Start: "boot:200"}, "bob")
		wantRefused(t, err, "no pidfd")
	})
	t.Run("the pidfd stops pointing to the PID after /proc was read", func(t *testing.T) {
		t.Parallel()
		r := setup()
		fd := localrig.NewPidfd(200)
		r.proc.OnCgroup = func(int) { fd.Reap() } // the caller is collected right after the read
		_, err := r.identify(localchan.Peer{PID: 200, Start: "boot:200", Pidfd: fd}, "bob")
		wantRefused(t, err, "reaped")
	})
	t.Run("a pidfd of another PID", func(t *testing.T) {
		t.Parallel()
		_, err := setup().identify(localchan.Peer{PID: 200, Start: "boot:200", Pidfd: localrig.NewPidfd(201)}, "bob")
		wantRefused(t, err, "points to PID 201")
	})
	t.Run("a pidfd that holds is accepted", func(t *testing.T) {
		t.Parallel()
		if _, err := setup().identify(localchan.Peer{PID: 200, Start: "boot:200", Pidfd: localrig.NewPidfd(200)}, "bob"); err != nil {
			t.Fatal(err)
		}
	})
	// A kernel before 6.5 has no SO_PEERPIDFD: the start identity is all there is.
	t.Run("a kernel without SO_PEERPIDFD keeps to the start identity", func(t *testing.T) {
		t.Parallel()
		r := setup()
		if _, err := r.identify(localchan.Peer{PID: 200, Start: "boot:200", PidfdUnsupported: true}, "bob"); err != nil {
			t.Fatal(err)
		}
		r.proc.SetIdentity(200, "boot:999") // the PID now belongs to another process
		_, err := r.identify(localchan.Peer{PID: 200, Start: "boot:200", PidfdUnsupported: true}, "bob")
		wantRefused(t, err, "another process")
	})
}

// CA-LOCAL-10: without a delegated cgroup the engine climbs the tree. A
// parent that started after its child is a reused PID: the climb starts
// again from the caller, up to 3 times.
func TestTheClimbRestartsWhenAParentIsOlderThanItsChild(t *testing.T) {
	t.Parallel()
	setup := func() *idRig {
		r := newIDRig(localchan.ModeTree)
		r.terms.Add("B", "bob", 300) // the mestre-pty of terminal B is PID 300
		r.proc.SetStat(500, localchan.ProcStat{PPID: 400, Start: 1000})
		r.proc.SetStat(300, localchan.ProcStat{PPID: 1, Start: 800})
		return r
	}
	peer := localchan.Peer{PID: 500, Pidfd: localrig.NewPidfd(500)}

	t.Run("a consistent tree", func(t *testing.T) {
		t.Parallel()
		r := setup()
		r.proc.SetStat(400, localchan.ProcStat{PPID: 300, Start: 900})
		id, err := r.identify(peer, "bob")
		if err != nil || id.TerminalID != "B" {
			t.Fatalf("identity = %+v, %v", id, err)
		}
		if _, err := r.identify(peer, "alice"); err == nil {
			t.Fatal("the terminal of bob was taken for alice's")
		}
	})
	t.Run("the race is gone on the second attempt", func(t *testing.T) {
		t.Parallel()
		r := setup()
		// First read: the "parent" 400 started after the child 500, the PID
		// was reused; second read: the real parent.
		r.proc.SetStat(400, localchan.ProcStat{PPID: 300, Start: 1100}, localchan.ProcStat{PPID: 300, Start: 900})
		id, err := r.identify(peer, "bob")
		if err != nil || id.TerminalID != "B" {
			t.Fatalf("identity = %+v, %v", id, err)
		}
	})
	t.Run("the race goes on for 3 attempts", func(t *testing.T) {
		t.Parallel()
		r := setup()
		r.proc.SetStat(400, localchan.ProcStat{PPID: 300, Start: 1100})
		_, err := r.identify(peer, "bob")
		wantRefused(t, err, "3 times in a row")
	})
	t.Run("a parent that vanished counts as the race", func(t *testing.T) {
		t.Parallel()
		r := setup() // 400 has no stat: gone
		_, err := r.identify(peer, "bob")
		wantRefused(t, err, "3 times in a row")
	})
	t.Run("an orphan adopted by init has no terminal", func(t *testing.T) {
		t.Parallel()
		r := setup()
		r.proc.SetStat(500, localchan.ProcStat{PPID: 1, Start: 1000})
		_, err := r.identify(peer, "bob")
		wantRefused(t, err, "orphan")
	})
	t.Run("a caller that is gone", func(t *testing.T) {
		t.Parallel()
		_, err := setup().identify(localchan.Peer{PID: 777, Pidfd: localrig.NewPidfd(777)}, "bob")
		wantRefused(t, err, "read the caller")
	})
}

// The same logic as CA-LOCAL-13, played with fakes: the real thing, with a
// terminal and its Job Object, is in the full suite.
func TestTheJobObjectSaysWhichTerminalCalls(t *testing.T) {
	t.Parallel()
	accepted := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	type world struct {
		id   *localchan.JobIdentifier
		jobs *localrig.FakeJobs
	}
	setup := func() world {
		w := world{jobs: &localrig.FakeJobs{}}
		terms := &localrig.FakeTerminals{}
		terms.Add("A", "alice", 0)
		terms.Add("B", "bob", 0)
		w.id = &localchan.JobIdentifier{Terminals: terms, Jobs: w.jobs}
		return w
	}
	p := localchan.Peer{PID: 4242, Start: "133000000000000000", StartedAt: accepted.Add(-time.Minute), AcceptedAt: accepted}
	ref := localchan.ProcRef{PID: 4242, Start: p.Start}

	w := setup()
	w.jobs.Put("B", ref, true)
	id, err := w.id.Identify(context.Background(), p, "bob")
	if err != nil || id.TerminalID != "B" {
		t.Fatalf("identity = %+v, %v", id, err)
	}
	_, err = w.id.Identify(context.Background(), p, "alice")
	wantRefused(t, err, "not in the Job Object")

	w = setup()
	w.jobs.Put("A", ref, true) // in the Job of another character's terminal
	_, err = w.id.Identify(context.Background(), p, "bob")
	wantRefused(t, err, "not in the Job Object")

	late := p
	late.StartedAt = accepted.Add(time.Second) // a PID reused after the accept
	w = setup()
	w.jobs.Put("B", ref, true)
	_, err = w.id.Identify(context.Background(), late, "bob")
	wantRefused(t, err, "started after the connection")

	w = setup()
	w.jobs.Fail = errors.New("mestre-pty does not answer")
	_, err = w.id.Identify(context.Background(), p, "bob")
	wantRefused(t, err, "could not be asked")

	_, err = w.id.Identify(context.Background(), p, "carol")
	wantRefused(t, err, "no live terminal")
}

func TestFiletimeToTime(t *testing.T) {
	t.Parallel()
	got, err := localchan.FiletimeToTime("133732944000000000") // 2024-10-13 12:00:00 UTC
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2024, 10, 13, 12, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, bad := range []string{"", "abc", "1000"} {
		if _, err := localchan.FiletimeToTime(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// peerFunc is a PeerSource from a function.
type peerFunc func() localchan.Peer

func (f peerFunc) Peer(net.Conn) (localchan.Peer, error) { return f(), nil }

// The residual M3/M4 of the M3 audit, round 3: without delegated cgroups
// every terminal is in the cgroup of the engine's service, so the group
// the caps count by is the terminal the climb finds at accept time, not
// that shared cgroup; a caller outside every terminal keeps its cgroup.
func TestWithoutCgroupsTheCapsCountByTheTerminalOfTheTree(t *testing.T) {
	t.Parallel()
	r := newIDRig(localchan.ModeTree)
	r.terms.Add("B", "bob", 300)
	r.terms.Add("C", "carol", 600)
	r.proc.SetStat(500, localchan.ProcStat{PPID: 300, Start: 1000})
	r.proc.SetStat(300, localchan.ProcStat{PPID: 1, Start: 800})
	r.proc.SetStat(700, localchan.ProcStat{PPID: 600, Start: 1000})
	r.proc.SetStat(600, localchan.ProcStat{PPID: 1, Start: 800})
	r.proc.SetStat(900, localchan.ProcStat{PPID: 1, Start: 1000}) // an orphan
	group := func(pid int) string {
		r.id.Peers = peerFunc(func() localchan.Peer { return localchan.Peer{PID: pid, Group: "/mestre.service"} })
		p, err := r.id.Accept(nil, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		return p.Group
	}
	if b, c := group(500), group(700); b != "tree/term-B" || c != "tree/term-C" {
		t.Fatalf("groups = %q, %q; want the terminal of each", b, c)
	}
	if o := group(900); o != "/mestre.service" {
		t.Fatalf("the orphan's group = %q; want its cgroup", o)
	}
}
