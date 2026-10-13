package terminal

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"syscall"
	"testing"
)

// permSystem is a System whose KillTree fails the way the engine's does on
// Linux without CAP_KILL: with EPERM on the process of another account.
type permSystem struct {
	err   error
	calls int
}

func (permSystem) Identify(int) (string, error)        { return "", ErrNoProcess }
func (permSystem) PeerPID(net.Conn) (int, error)       { return 0, errors.New("no") }
func (permSystem) WaitExit(context.Context, int) error { return nil }
func (s *permSystem) KillTree(context.Context, ProcID, []int) error {
	s.calls++
	return s.err
}

// launcherSpawner is a Spawner that is also a TreeKiller.
type launcherSpawner struct {
	got  []killCall
	err  error
	cgrp int
}

type killCall struct {
	id       string
	p        ProcID
	sessions []int
}

func (*launcherSpawner) Spawn(context.Context, SpawnSpec) (Spawned, error) { return Spawned{}, nil }
func (l *launcherSpawner) KillCgroup(context.Context, string) error        { l.cgrp++; return nil }
func (l *launcherSpawner) KillTree(_ context.Context, id string, p ProcID, sessions []int) error {
	l.got = append(l.got, killCall{id, p, sessions})
	return l.err
}

// plainSpawner has no launcher to ask.
type plainSpawner struct{ launcherSpawner }

func (plainSpawner) KillTree() {} // a different signature: not a TreeKiller

func newKillManager(sys System, sp Spawner) *Manager {
	return &Manager{cfg: ManagerConfig{System: sys, Spawner: sp}, log: slog.New(slog.DiscardHandler)}
}

// Without a delegated cgroup the engine (account `mestre`) is refused when it
// signals the processes of `mestre-agente`: the launcher does it, by the same
// identity.
func TestKillTreeAsksTheLauncherWhenTheEngineIsRefused(t *testing.T) {
	t.Parallel()
	p := ProcID{PID: 4242, Start: "boot:99"}
	eperm := &wrapped{syscall.EPERM}

	t.Run("refused, then the launcher ends it", func(t *testing.T) {
		t.Parallel()
		sys, sp := &permSystem{err: eperm}, &launcherSpawner{}
		if err := newKillManager(sys, sp).killTree(t.Context(), "t1", p, []int{4300}); err != nil {
			t.Fatal(err)
		}
		if len(sp.got) != 1 || sp.got[0].id != "t1" || sp.got[0].p != p || len(sp.got[0].sessions) != 1 || sp.got[0].sessions[0] != 4300 {
			t.Fatalf("the launcher got %+v", sp.got)
		}
	})
	t.Run("the engine's own right is enough", func(t *testing.T) {
		t.Parallel()
		sys, sp := &permSystem{}, &launcherSpawner{}
		if err := newKillManager(sys, sp).killTree(t.Context(), "t1", p, nil); err != nil {
			t.Fatal(err)
		}
		if len(sp.got) != 0 {
			t.Errorf("the launcher was asked: %+v", sp.got)
		}
	})
	t.Run("another error is not for the launcher", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("session 7 still has processes")
		sys, sp := &permSystem{err: boom}, &launcherSpawner{}
		if err := newKillManager(sys, sp).killTree(t.Context(), "t1", p, nil); !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
		if len(sp.got) != 0 {
			t.Errorf("the launcher was asked: %+v", sp.got)
		}
	})
	t.Run("the launcher fails too", func(t *testing.T) {
		t.Parallel()
		nope := errors.New("the launcher is down")
		sys, sp := &permSystem{err: eperm}, &launcherSpawner{err: nope}
		err := newKillManager(sys, sp).killTree(t.Context(), "t1", p, nil)
		if !errors.Is(err, nope) || !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("err = %v, want both reasons", err)
		}
	})
	t.Run("no launcher to ask", func(t *testing.T) {
		t.Parallel()
		sys := &permSystem{err: eperm}
		if err := newKillManager(sys, &plainSpawner{}).killTree(t.Context(), "t1", p, nil); !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("err = %v", err)
		}
	})
}

// wrapped is an error that wraps an errno, as the Linux System's do.
type wrapped struct{ error }

func (w *wrapped) Unwrap() error { return w.error }
