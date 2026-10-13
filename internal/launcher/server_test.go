package launcher_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/launcher"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/internal/terminal/ptywire"
	"github.com/lucasndas/mestre/pkg/clock"
)

// fakeBackend records what the server asks and creates nothing.
type fakeBackend struct {
	mu     sync.Mutex
	starts []launcher.StartRequest
	kills  []string
	err    error
}

func (f *fakeBackend) Start(_ context.Context, r launcher.StartRequest) (launcher.Started, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, r)
	if f.err != nil {
		return launcher.Started{}, f.err
	}
	return launcher.Started{PID: 4242, Start: "t0", CgroupDir: "/cg/term-" + r.ID}, nil
}

func (f *fakeBackend) Kill(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kills = append(f.kills, id)
	return f.err
}

func (f *fakeBackend) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.starts) + len(f.kills)
}

// rig is a server over in-memory connections, with fakes for everything.
type rig struct {
	srv     *launcher.Server
	be      *fakeBackend
	bin     string
	sockets string
	logs    *bytes.Buffer
	mu      sync.Mutex
	events  []launcher.Event
	allow   bool
}

func ptyName() string {
	if runtime.GOOS == "windows" {
		return "mestre-pty.exe"
	}
	return "mestre-pty"
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{be: &fakeBackend{}, bin: t.TempDir(), sockets: filepath.Join(string(filepath.Separator)+"run", "mestre", "pty"), logs: &bytes.Buffer{}, allow: true}
	if err := os.MkdirAll(filepath.Join(r.bin, "1.0.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.bin, "1.0.0", ptyName()), []byte("not a program"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv, err := launcher.NewServer(launcher.Config{
		Backend: r.be, BinDir: r.bin, SocketDir: r.sockets, SecretInline: true,
		Check: func(net.Conn) (string, error) {
			if !r.allow {
				return "pid=1 uid=7", errors.New("not the engine")
			}
			return "pid=1 uid=0", nil
		},
		Audit: func(e launcher.Event) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.events = append(r.events, e)
		},
		Logger: slog.New(slog.NewJSONHandler(r.logs, nil)),
		Clock:  clock.NewFake(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatal(err)
	}
	r.srv = srv
	return r
}

func (r *rig) recorded() []launcher.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]launcher.Event(nil), r.events...)
}

// do sends one raw frame and returns the answer.
func (r *rig) do(t *testing.T, body []byte) launcher.Response {
	t.Helper()
	c1, c2 := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.srv.ServeConn(t.Context(), c2)
	}()
	conn := launcher.NewConn(c1)
	defer func() { _ = conn.Close(); <-done }()
	if r.allow { // a peer that is refused is not read: the answer comes first
		if err := conn.WriteFrame(body, nil); err != nil {
			t.Fatal(err)
		}
	}
	frame, _, err := conn.ReadFrame()
	if err != nil {
		t.Fatalf("read answer: %v", err)
	}
	var resp launcher.Response
	if err := json.Unmarshal(frame, &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func (r *rig) socket(id string) string {
	p, err := terminal.SocketPath(r.sockets, id)
	if err != nil {
		panic(err)
	}
	return p
}

func (r *rig) start(id string) launcher.Request {
	return launcher.Request{
		Op: launcher.OpStartPTY, ID: id, Version: "1.0.0", Socket: r.socket(id),
		Secret: bytes.Repeat([]byte{0xAB}, ptywire.SecretSize),
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestServerStartAndKill(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	resp := r.do(t, mustJSON(t, r.start("t1")))
	if !resp.OK || resp.PID != 4242 || resp.Start != "t0" || resp.CgroupDir != "/cg/term-t1" {
		t.Fatalf("start = %+v", resp)
	}
	if len(r.be.starts) != 1 {
		t.Fatalf("backend starts = %d", len(r.be.starts))
	}
	got := r.be.starts[0]
	if got.Binary != filepath.Join(r.bin, "1.0.0", ptyName()) || got.Socket != r.socket("t1") || len(got.Secret) != ptywire.SecretSize {
		t.Errorf("start request = %+v", got)
	}
	if resp := r.do(t, mustJSON(t, launcher.Request{Op: launcher.OpKill, ID: "t1"})); !resp.OK {
		t.Fatalf("kill = %+v", resp)
	}
	if len(r.be.kills) != 1 || r.be.kills[0] != "t1" {
		t.Errorf("backend kills = %v", r.be.kills)
	}
	ev := r.recorded()
	if len(ev) != 2 || ev[0].Outcome != launcher.OutcomeOK || ev[0].Op != launcher.OpStartPTY || ev[0].ID != "t1" ||
		ev[0].Peer != "pid=1 uid=0" || !ev[0].Time.Equal(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("events = %+v", ev)
	}
}

// The log and the record never carry the body of a request, which holds the
// secret of the terminal.
func TestServerNeverLogsTheSecret(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	req := r.start("t1")
	r.do(t, mustJSON(t, req))
	r.do(t, mustJSON(t, map[string]any{"op": "start-pty", "id": "t1", "secret": req.Secret, "user": "root"}))
	b64 := base64.StdEncoding.EncodeToString(req.Secret)
	out := r.logs.String()
	if out == "" {
		t.Fatal("nothing was logged")
	}
	for _, leak := range []string{b64, "root"} {
		if strings.Contains(out, leak) {
			t.Errorf("the log has %q:\n%s", leak, out)
		}
	}
	for _, e := range r.recorded() {
		if strings.Contains(e.Reason, b64) {
			t.Errorf("event %+v has the secret", e)
		}
	}
}

func TestServerRefusals(t *testing.T) {
	t.Parallel()
	secret := bytes.Repeat([]byte{1}, ptywire.SecretSize)
	cases := []struct {
		name string
		body func(r *rig) any
		raw  []byte
		deny bool // the peer is not the engine
	}{
		{name: "another peer", deny: true, body: func(r *rig) any { return r.start("t1") }},
		{name: "another account", body: func(*rig) any {
			return map[string]any{"op": "start-pty", "id": "t1", "version": "1.0.0", "user": "root"}
		}},
		{name: "another executable", body: func(*rig) any {
			return map[string]any{"op": "start-pty", "id": "t1", "version": "1.0.0", "path": "/bin/sh"}
		}},
		{name: "arguments", body: func(*rig) any {
			return map[string]any{"op": "start-pty", "id": "t1", "version": "1.0.0", "args": []string{"-c", "id"}}
		}},
		{name: "not start-pty", body: func(*rig) any { return map[string]any{"op": "exec", "id": "t1"} }},
		{name: "no op", body: func(*rig) any { return map[string]any{"id": "t1"} }},
		{name: "version is a path", body: func(r *rig) any {
			q := r.start("t1")
			q.Version = "../../bin/sh"
			return q
		}},
		{name: "version not installed", body: func(r *rig) any {
			q := r.start("t1")
			q.Version = "9.9.9"
			return q
		}},
		{name: "bad terminal id", body: func(r *rig) any {
			q := r.start("t1")
			q.ID = "../x"
			return q
		}},
		{name: "socket elsewhere", body: func(r *rig) any {
			q := r.start("t1")
			q.Socket = filepath.Join(r.sockets, "..", "other.sock")
			return q
		}},
		{name: "short secret", body: func(r *rig) any {
			q := r.start("t1")
			q.Secret = secret[:8]
			return q
		}},
		{name: "no secret", body: func(r *rig) any {
			q := r.start("t1")
			q.Secret = nil
			return q
		}},
		{name: "descriptors where the secret is inline", body: func(r *rig) any {
			q := r.start("t1")
			q.Files = []string{launcher.RoleListen, launcher.RoleSecret}
			return q
		}},
		{name: "kill with a version", body: func(*rig) any {
			return launcher.Request{Op: launcher.OpKill, ID: "t1", Version: "1.0.0"}
		}},
		{name: "data after the request", raw: []byte(`{"op":"kill","id":"t1"} {"op":"exec"}`)},
		{name: "not json", raw: []byte(`start-pty t1`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			r.allow = !tc.deny
			raw := tc.raw
			if raw == nil {
				raw = mustJSON(t, tc.body(r))
			}
			resp := r.do(t, raw)
			if resp.OK || resp.Code != launcher.CodeRefused {
				t.Fatalf("answer = %+v, want a refusal", resp)
			}
			if r.be.calls() != 0 {
				t.Errorf("the backend was called %d times: a process would have been created", r.be.calls())
			}
			ev := r.recorded()
			if len(ev) != 1 || ev[0].Outcome != launcher.OutcomeRefused || ev[0].Reason == "" {
				t.Errorf("events = %+v, want one refusal with its reason", ev)
			}
		})
	}
}

func TestServerBackendErrors(t *testing.T) {
	t.Parallel()
	t.Run("failure", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		r.be.err = errors.New("fork: resource temporarily unavailable")
		resp := r.do(t, mustJSON(t, r.start("t1")))
		if resp.OK || resp.Code != launcher.CodeFailed || !strings.Contains(resp.Error, "resource") {
			t.Errorf("answer = %+v", resp)
		}
		if ev := r.recorded(); len(ev) != 1 || ev[0].Outcome != launcher.OutcomeFailed {
			t.Errorf("events = %+v", ev)
		}
	})
	t.Run("refusal", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		r.be.err = launcher.Refused("not a listening socket")
		resp := r.do(t, mustJSON(t, r.start("t1")))
		if resp.OK || resp.Code != launcher.CodeRefused {
			t.Errorf("answer = %+v", resp)
		}
		if ev := r.recorded(); len(ev) != 1 || ev[0].Outcome != launcher.OutcomeRefused {
			t.Errorf("events = %+v", ev)
		}
	})
}

// A peer that connects and says nothing is dropped when the read timeout of
// the (fake) clock runs out.
func TestServerDropsASilentPeer(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	fake := clock.NewFake(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))
	srv, err := launcher.NewServer(launcher.Config{
		Backend: r.be, BinDir: r.bin, SocketDir: r.sockets, SecretInline: true, Clock: fake,
		Check: func(net.Conn) (string, error) { return "peer", nil }, ReadTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	c1, c2 := net.Pipe()
	defer c1.Close()
	done := make(chan struct{})
	go func() { defer close(done); srv.ServeConn(t.Context(), c2) }()
	fake.BlockUntil(1) // the server waits for the request, with its timer armed
	fake.Advance(10 * time.Second)
	<-done
	if r.be.calls() != 0 {
		t.Errorf("backend called")
	}
}

func TestNewServerNeedsAPeerCheck(t *testing.T) {
	t.Parallel()
	_, err := launcher.NewServer(launcher.Config{Backend: &fakeBackend{}, BinDir: "b", SocketDir: "s"})
	if err == nil {
		t.Fatal("a server without a peer check accepts anyone")
	}
}

func TestServeStopsWithItsContext(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	ctx, cancel := context.WithCancel(t.Context())
	ln := newChanListener()
	done := make(chan error, 1)
	go func() { done <- r.srv.Serve(ctx, ln) }()
	c1, c2 := net.Pipe()
	ln.conns <- c2
	conn := launcher.NewConn(c1)
	if err := conn.WriteFrame(mustJSON(t, r.start("t1")), nil); err != nil {
		t.Fatal(err)
	}
	frame, _, err := conn.ReadFrame()
	if err != nil || !strings.Contains(string(frame), `"ok":true`) {
		t.Fatalf("answer %s, %v", frame, err)
	}
	_ = conn.Close()
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Serve = %v", err)
	}
}

// chanListener hands out connections from a channel.
type chanListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newChanListener() *chanListener {
	return &chanListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) Close() error   { l.once.Do(func() { close(l.closed) }); return nil }
func (l *chanListener) Addr() net.Addr { return &net.UnixAddr{Name: "fake", Net: "unix"} }
