package launcher_test

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"slices"
	"testing"

	"github.com/lucasndas/mestre/internal/launcher"
	"github.com/lucasndas/mestre/internal/terminal"
)

// A kill that names the mestre-pty by PID and start identity, and the sessions
// of its agent, reaches the backend as such, and is recorded: the fallback of
// a terminal without a cgroup, where the engine cannot signal the agents'
// processes itself.
func TestServerKillsByIdentity(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	resp := r.do(t, mustJSON(t, launcher.Request{Op: launcher.OpKill, ID: "t1", PID: 4242, Start: "boot:99", Sessions: []int{4300, 4400}}))
	if !resp.OK {
		t.Fatalf("kill = %+v", resp)
	}
	want := procKill{"t1", terminal.ProcID{PID: 4242, Start: "boot:99"}, []int{4300, 4400}}
	if len(r.be.procs) != 1 || r.be.procs[0].id != want.id || r.be.procs[0].p != want.p || !slices.Equal(r.be.procs[0].sessions, want.sessions) {
		t.Fatalf("backend got %+v, want %+v", r.be.procs, want)
	}
	if len(r.be.kills) != 0 {
		t.Errorf("the cgroup kill ran too: %v", r.be.kills)
	}
	if ev := r.recorded(); len(ev) != 1 || ev[0].Op != launcher.OpKill || ev[0].Outcome != launcher.OutcomeOK {
		t.Errorf("events = %+v", ev)
	}
}

func TestServerRefusesWrongKillsByIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		req  launcher.Request
	}{
		{"a PID without its start identity", launcher.Request{Op: launcher.OpKill, ID: "t1", PID: 4242}},
		{"a start identity without a PID", launcher.Request{Op: launcher.OpKill, ID: "t1", Start: "boot:99"}},
		{"sessions without a PID", launcher.Request{Op: launcher.OpKill, ID: "t1", Sessions: []int{4300}}},
		{"init", launcher.Request{Op: launcher.OpKill, ID: "t1", PID: 1, Start: "boot:1"}},
		{"session 1", launcher.Request{Op: launcher.OpKill, ID: "t1", PID: 4242, Start: "boot:99", Sessions: []int{1}}},
		{"a negative session", launcher.Request{Op: launcher.OpKill, ID: "t1", PID: 4242, Start: "boot:99", Sessions: []int{-5}}},
		{"too many sessions", launcher.Request{Op: launcher.OpKill, ID: "t1", PID: 4242, Start: "boot:99", Sessions: slices.Repeat([]int{4300}, 17)}},
		{"a start-pty that names a process", launcher.Request{Op: launcher.OpStartPTY, ID: "t1", Version: "1.0.0", PID: 4242, Start: "boot:99"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			if resp := r.do(t, mustJSON(t, tc.req)); resp.OK || resp.Code != launcher.CodeRefused {
				t.Fatalf("answer = %+v, want a refusal", resp)
			}
			if r.be.calls() != 0 {
				t.Errorf("the backend was called %d times", r.be.calls())
			}
		})
	}
}

// plainBackend hides everything but Backend, as the Windows one does.
type plainBackend struct{ launcher.Backend }

// A launcher whose backend does not end processes by identity (Windows)
// refuses the request and records it.
func TestServerRefusesKillsByIdentityWithoutABackendThatDoesThem(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	srv, err := launcher.NewServer(launcher.Config{
		Backend: plainBackend{r.be}, BinDir: r.bin, SocketDir: r.sockets, SecretInline: true,
		Check:  func(net.Conn) (string, error) { return "pid=1", nil },
		Audit:  func(e launcher.Event) { r.mu.Lock(); r.events = append(r.events, e); r.mu.Unlock() },
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	r.srv = srv
	resp := r.do(t, mustJSON(t, launcher.Request{Op: launcher.OpKill, ID: "t1", PID: 4242, Start: "boot:99"}))
	if resp.OK || resp.Code != launcher.CodeRefused {
		t.Fatalf("answer = %+v", resp)
	}
	if ev := r.recorded(); len(ev) != 1 || ev[0].Outcome != launcher.OutcomeRefused {
		t.Errorf("events = %+v", ev)
	}
}

func TestClientKillTree(t *testing.T) {
	t.Parallel()
	t.Run("asks by identity", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		var tk terminal.TreeKiller = clientFor(t, r)
		if err := tk.KillTree(t.Context(), "t1", terminal.ProcID{PID: 4242, Start: "boot:99"}, []int{4300}); err != nil {
			t.Fatal(err)
		}
		if len(r.be.procs) != 1 || r.be.procs[0].p.PID != 4242 || !slices.Equal(r.be.procs[0].sessions, []int{4300}) {
			t.Errorf("backend got %+v", r.be.procs)
		}
	})
	t.Run("never on a PID alone", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		c := clientFor(t, r)
		for _, p := range []terminal.ProcID{{PID: 4242}, {Start: "boot:99"}, {}} {
			if err := c.KillTree(t.Context(), "t1", p, nil); err == nil {
				t.Errorf("KillTree(%+v) was sent", p)
			}
		}
		if r.be.calls() != 0 {
			t.Errorf("the backend was called %d times", r.be.calls())
		}
	})
	t.Run("a launcher that only starts says so", func(t *testing.T) {
		t.Parallel()
		c := &launcher.Client{NoKill: true}
		if err := c.KillTree(t.Context(), "t1", terminal.ProcID{PID: 4242, Start: "boot:99"}, nil); !errors.Is(err, launcher.ErrNoKill) {
			t.Errorf("KillTree = %v", err)
		}
	})
	t.Run("a bad terminal id", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		if err := clientFor(t, r).KillTree(context.Background(), "../x", terminal.ProcID{PID: 4242, Start: "boot:99"}, nil); err == nil {
			t.Error("a terminal id with a path was sent")
		}
	})
}
