package launcher_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"

	"github.com/mestre-ia/mestre/internal/launcher"
	"github.com/mestre-ia/mestre/internal/terminal"
	"github.com/mestre-ia/mestre/internal/terminal/ptywire"
)

// clientFor connects a client to the rig's server over in-memory
// connections; the secret travels inline, as on Windows.
func clientFor(t *testing.T, r *rig) *launcher.Client {
	t.Helper()
	return &launcher.Client{
		Dial: func(ctx context.Context) (net.Conn, error) {
			c1, c2 := net.Pipe()
			go r.srv.ServeConn(ctx, c2)
			return c1, nil
		},
		Pack: launcher.PackInline,
	}
}

func (r *rig) spec(id string) terminal.SpawnSpec {
	return terminal.SpawnSpec{
		ID: id, Version: "1.0.0", Binary: "/ignored/by/the/launcher", SocketPath: r.socket(id),
		Secret: bytes.Repeat([]byte{7}, ptywire.SecretSize),
	}
}

// CA-CODEX-WIN-02
//
// The logon SID the launcher read from the token it made reaches the engine
// with the process (TestClientSpawnAndKill checks the value).
func TestClientSpawnAndKill(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	var sp terminal.Spawner = clientFor(t, r)
	got, err := sp.Spawn(t.Context(), r.spec("t1"))
	if err != nil {
		t.Fatal(err)
	}
	if got.PID != 4242 || got.Start != "t0" || got.CgroupDir != "/cg/term-t1" || got.LogonSID != "S-1-5-5-0-4242" {
		t.Errorf("Spawned = %+v", got)
	}
	if len(r.be.starts) != 1 || r.be.starts[0].Binary != filepath.Join(r.bin, "1.0.0", ptyName()) {
		t.Errorf("the launcher must start its own binary, not the one in the spec: %+v", r.be.starts)
	}
	if err := sp.KillCgroup(t.Context(), "t1"); err != nil {
		t.Fatal(err)
	}
	if len(r.be.kills) != 1 || r.be.kills[0] != "t1" {
		t.Errorf("kills = %v", r.be.kills)
	}
}

func TestClientErrors(t *testing.T) {
	t.Parallel()
	t.Run("refusal", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		c := clientFor(t, r)
		spec := r.spec("t1")
		spec.Version = "9.9.9" // not installed
		_, err := c.Spawn(t.Context(), spec)
		if !errors.Is(err, launcher.ErrRefused) {
			t.Fatalf("err = %v, want a refusal", err)
		}
	})
	t.Run("failure is not a refusal", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		r.be.err = errors.New("boom")
		_, err := clientFor(t, r).Spawn(t.Context(), r.spec("t1"))
		var le *launcher.Error
		if err == nil || errors.Is(err, launcher.ErrRefused) || !errors.As(err, &le) || le.Code != launcher.CodeFailed {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("invalid arguments never reach the launcher", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		c := clientFor(t, r)
		bad := r.spec("t1")
		bad.ID = "a/b"
		if _, err := c.Spawn(t.Context(), bad); err == nil {
			t.Error("bad id accepted")
		}
		bad = r.spec("t1")
		bad.Version = ""
		if _, err := c.Spawn(t.Context(), bad); err == nil {
			t.Error("no version accepted")
		}
		if err := c.KillCgroup(t.Context(), "../x"); err == nil {
			t.Error("bad id accepted by kill")
		}
		if r.be.calls() != 0 || len(r.recorded()) != 0 {
			t.Errorf("the launcher heard of it: %d calls, %v", r.be.calls(), r.recorded())
		}
	})
	t.Run("launcher unreachable", func(t *testing.T) {
		t.Parallel()
		c := &launcher.Client{
			Dial: func(context.Context) (net.Conn, error) { return nil, errors.New("no such file") },
			Pack: launcher.PackInline,
		}
		_, err := c.Spawn(t.Context(), terminal.SpawnSpec{ID: "t1", Version: "1.0.0", Secret: make([]byte, 32)})
		if err == nil {
			t.Fatal("no error")
		}
	})
	t.Run("context ended", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := clientFor(t, r).Spawn(ctx, r.spec("t1")); !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a launcher that never answers", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		c := &launcher.Client{
			Dial: func(context.Context) (net.Conn, error) {
				c1, c2 := net.Pipe()
				go func() { // reads the request, then says nothing
					_, _, _ = launcher.NewConn(c2).ReadFrame()
					cancel()
				}()
				return c1, nil
			},
			Pack: launcher.PackInline,
		}
		_, err := c.Spawn(ctx, terminal.SpawnSpec{ID: "t1", Version: "1.0.0", Secret: make([]byte, 32)})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestClientNoKill(t *testing.T) {
	t.Parallel()
	c := &launcher.Client{NoKill: true}
	if err := c.KillCgroup(t.Context(), "t1"); err != nil {
		t.Errorf("KillCgroup = %v, want nothing to do", err)
	}
}
