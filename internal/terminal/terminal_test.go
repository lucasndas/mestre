package terminal_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/internal/terminal/ptyd"
	"github.com/lucasndas/mestre/internal/terminal/ptywire"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/internal/testkit/fakepty"
	"github.com/lucasndas/mestre/pkg/clock"
)

// rig is a mestre-pty (session and server) on the fake PTY and the fake
// network, with the engine side connecting to it: no process, no socket.
type rig struct {
	clk    *clock.Fake
	net    *fakenet.Network
	runner *fakepty.Runner
	sess   *ptyd.Session
	srv    *ptyd.Server
	opts   rigOptions
	secret []byte
	log    *syncBuffer
}

type rigOptions struct {
	bufferSize, unackedMax int
	proto                  int
	noJob                  bool
	hooks                  ptyd.HookRunner
}

type syncBuffer struct {
	mu      sync.Mutex
	b       bytes.Buffer
	changed chan struct{}
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.changed != nil {
		close(s.changed)
		s.changed = nil
	}
	return s.b.Write(p)
}

// waitFor blocks until the text holds sub, without polling.
func (s *syncBuffer) waitFor(t *testing.T, sub string) string {
	t.Helper()
	limit := clock.Real{}.NewTimer(time.Minute) // a hang is a failure, not a 10-minute timeout
	defer limit.Stop()
	for {
		s.mu.Lock()
		text := s.b.String()
		if s.changed == nil {
			s.changed = make(chan struct{})
		}
		ch := s.changed
		s.mu.Unlock()
		if strings.Contains(text, sub) {
			return text
		}
		select {
		case <-ch:
		case <-t.Context().Done():
			t.Fatalf("output never held %q:\n%s", sub, text)
		case <-limit.C():
			t.Fatalf("output did not hold %q in a minute:\n%s", sub, text)
		}
	}
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newRig(t *testing.T, o rigOptions) *rig {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))
	r := &rig{clk: clk, net: fakenet.New(clk), runner: &fakepty.Runner{Clock: clk, NoJob: o.noJob}, log: &syncBuffer{}, opts: o, secret: bytes.Repeat([]byte{7}, ptywire.SecretSize)}
	log := slog.New(slog.NewTextHandler(r.log, nil))
	r.sess = ptyd.NewSession(ptyd.SessionConfig{
		Runner: r.runner, Clock: clk, BufferSize: o.bufferSize, UnackedMax: o.unackedMax, Hooks: o.hooks,
		OnEvent: func(m ptywire.Msg) { r.srv.Broadcast(m) },
	})
	r.srv = ptyd.NewServer(ptyd.ServerConfig{
		Secret: r.secret, Session: r.sess, Clock: clk, Version: "1.0.0", PID: 4242, ProtoVersion: o.proto, Logger: log,
	})
	ln, err := r.net.Listen("pty:1")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = r.srv.Serve(t.Context(), ln) }()
	t.Cleanup(r.sess.Close)
	return r
}

// hooks is the fake shell of the rig, when it has one.
func (r *rig) hooks() *fakeHooks { h, _ := r.opts.hooks.(*fakeHooks); return h }

func (r *rig) dial(t *testing.T) *terminal.Client {
	t.Helper()
	nc, err := r.net.Dial(t.Context(), "engine", "pty:1")
	if err != nil {
		t.Fatal(err)
	}
	c, err := terminal.NewClient(nc, r.secret, r.clk, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func (r *rig) start(t *testing.T, c *terminal.Client) *fakepty.Agent {
	t.Helper()
	if _, err := c.Start(t.Context(), ptywire.Msg{Path: "agent"}); err != nil {
		t.Fatal(err)
	}
	return r.runner.Last()
}

// printed makes the fake agent print p and waits until the mestre-pty's
// buffer holds it.
func (r *rig) printed(t *testing.T, a *fakepty.Agent, p []byte) {
	t.Helper()
	want := r.sess.Buffer().End() + uint64(len(p))
	if _, err := a.Process().Write(p); err != nil {
		t.Fatal(err)
	}
	// Read End once per round: waiting from a second, later reading of it
	// could wait for bytes past want that never come.
	for end := r.sess.Buffer().End(); end < want; end = r.sess.Buffer().End() {
		if _, err := r.sess.Buffer().Next(t.Context(), end, 1); err != nil {
			t.Fatal(err)
		}
	}
}

func pattern(from, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + (from+i)%26)
	}
	return b
}

// quiet returns n bytes that depend on their offset, in a range the
// emulator ignores (C0 controls 0x01 to 0x06), so a megabyte of them costs
// no scrolling.
func quiet(from, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(1 + (uint32(from+i)*2654435761>>13)%6)
	}
	return b
}

// CA-TERM-01
func TestReplayAfterThe1MiBBufferWrappedMarksTheLostChunk(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	c := r.dial(t)
	a := r.start(t, c)
	const total = 3 << 19 // 1.5 MiB
	r.printed(t, a, quiet(0, total))
	// The engine's log holds all of it (the ack), so only the 1 MiB ring is kept.
	if err := c.Ack(t.Context(), total); err != nil {
		t.Fatal(err)
	}

	got, err := c.Replay(t.Context(), 0)

	if err != nil {
		t.Fatal(err)
	}
	if got.Lost != 1<<19 || got.Offset != 1<<19 || len(got.Data) != 1<<20 {
		t.Fatalf("replay = offset %d, lost %d, %d bytes; want offset 0.5 MiB, lost 0.5 MiB, 1 MiB", got.Offset, got.Lost, len(got.Data))
	}
	if !bytes.Equal(got.Data, quiet(1<<19, 1<<20)) {
		t.Fatal("replayed bytes differ from the output")
	}
}

// CA-TERM-04
func TestTwoViewersSeeTheSameBytesAtTheFixedSize(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	c1, c2 := r.dial(t), r.dial(t)
	a := r.start(t, c1)

	// Viewer 2 asks to resize the terminal to its own width: there is no
	// such message, so nothing changes.
	nc, err := r.net.Dial(t.Context(), "browser", "pty:1")
	if err != nil {
		t.Fatal(err)
	}
	raw := ptywire.NewConn(nc)
	if _, err := ptywire.ClientHandshake(raw, r.secret); err != nil {
		t.Fatal(err)
	}
	if err := raw.WriteMsg(&ptywire.Msg{T: "resize", ID: 1, Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	it, err := raw.Read()
	if err != nil || it.Msg == nil || it.Msg.Code != ptywire.CodeUnknownMessage {
		t.Fatalf("resize answered with %+v, %v, want unknown_message", it, err)
	}
	_ = raw.Close()

	var wg sync.WaitGroup
	collect := func(c *terminal.Client, n int) *bytes.Buffer {
		var mu sync.Mutex
		var buf bytes.Buffer
		wg.Add(1)
		done := false
		if _, err := c.Follow(t.Context(), 0, func(f ptywire.Frame) {
			mu.Lock()
			defer mu.Unlock()
			buf.Write(f.Data)
			if !done && buf.Len() >= n {
				done = true
				wg.Done()
			}
		}); err != nil {
			t.Fatal(err)
		}
		return &buf
	}
	const n = 100_000
	b1, b2 := collect(c1, n), collect(c2, n)
	r.printed(t, a, pattern(0, n))
	wg.Wait()

	if !bytes.Equal(b1.Bytes(), b2.Bytes()) || !bytes.Equal(b1.Bytes(), pattern(0, n)) {
		t.Fatal("the two viewers did not receive the same bytes")
	}
	st, err := c1.Status(t.Context())
	if err != nil || st.Cols != 200 || st.Rows != 50 {
		t.Fatalf("size = %dx%d, %v, want 200x50", st.Cols, st.Rows, err)
	}
	if cols, rows := a.Pair().Size(); cols != 200 || rows != 50 {
		t.Fatalf("PTY size = %dx%d, want 200x50", cols, rows)
	}
}

func TestFollowAfterTheBufferWrappedStartsAtTheOldestByteKept(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{bufferSize: 64, unackedMax: 64})
	c := r.dial(t)
	a := r.start(t, c)
	r.printed(t, a, pattern(0, 200))
	got := make(chan ptywire.Frame, 4)

	fs, err := c.Follow(t.Context(), 0, func(f ptywire.Frame) { got <- f })

	if err != nil || fs.Offset != 136 || fs.Lost != 136 {
		t.Fatalf("Follow = %+v, %v, want offset 136, lost 136", fs, err)
	}
	if f := <-got; f.Offset != 136 || !bytes.Equal(f.Data, pattern(136, 64)) {
		t.Fatalf("first frame = offset %d, %d bytes", f.Offset, len(f.Data))
	}
	r.printed(t, a, []byte("tail"))
	if f := <-got; f.Offset != 200 || string(f.Data) != "tail" {
		t.Fatalf("live frame = offset %d, %q", f.Offset, f.Data)
	}
}

func TestInputReachesTheAgentAndTheScreenFollowsTheOutput(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	c := r.dial(t)
	a := r.start(t, c)
	r.printed(t, a, []byte("\x1b[1mready\x1b[0m\r\n> "))

	scr, err := c.Screen(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if scr.Text != "ready\n>" || scr.CurCol != 2 || scr.CurRow != 1 {
		t.Fatalf("screen = %q at %d,%d", scr.Text, scr.CurCol, scr.CurRow)
	}
	if err := c.Input(t.Context(), []byte("hello\r")); err != nil {
		t.Fatal(err)
	}
	// The agent reads what was typed.
	buf := make([]byte, 16)
	n, err := a.Process().Read(buf)
	if err != nil || string(buf[:n]) != "hello\r" {
		t.Fatalf("agent read %q, %v", buf[:n], err)
	}
}

func TestEveryConnectionHasToProveTheSecret(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		play func(t *testing.T, r *rig, raw *ptywire.Conn)
		want string
	}{
		{"engine with the wrong secret", func(t *testing.T, r *rig, raw *ptywire.Conn) {
			if _, err := ptywire.ClientHandshake(raw, bytes.Repeat([]byte{9}, 32)); !errors.Is(err, ptywire.ErrAuth) {
				t.Errorf("handshake = %v, want ErrAuth", err)
			}
		}, "reading auth"},
		{"input before any handshake", func(t *testing.T, r *rig, raw *ptywire.Conn) {
			_ = raw.WriteMsg(&ptywire.Msg{T: ptywire.TypeInput, ID: 1, Data: []byte("rm -rf\r")})
		}, "first message is not a hello"},
		{"hello then a forged proof", func(t *testing.T, r *rig, raw *ptywire.Conn) {
			_ = raw.WriteMsg(&ptywire.Msg{T: ptywire.TypeHello, Nonce: "AAAA"})
			if _, err := raw.Read(); err != nil {
				t.Error(err)
			}
			_ = raw.WriteMsg(&ptywire.Msg{T: ptywire.TypeAuth, Proof: "AAAA"})
		}, "wrong secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, rigOptions{})
			c := r.dial(t)
			a := r.start(t, c)
			nc, err := r.net.Dial(t.Context(), "intruder", "pty:1")
			if err != nil {
				t.Fatal(err)
			}
			raw := ptywire.NewConn(nc)

			tt.play(t, r, raw)
			_ = raw.Close()
			// A later, valid request on the real connection proves the
			// refused one is over and nothing it sent was applied.
			if _, err := c.Status(t.Context()); err != nil {
				t.Fatal(err)
			}

			r.log.waitFor(t, "handshake refused")
			r.log.waitFor(t, tt.want)
			if got := a.Pair().Input(); got != "" {
				t.Fatalf("the agent received %q", got)
			}
		})
	}
}

func TestTheEngineRefusesAMestrePtyThatDoesNotKnowTheSecret(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	r.secret = bytes.Repeat([]byte{1}, 32) // the engine's idea of the secret
	nc, err := r.net.Dial(t.Context(), "engine", "pty:1")
	if err != nil {
		t.Fatal(err)
	}

	_, err = terminal.NewClient(nc, r.secret, r.clk, 0)

	if !errors.Is(err, ptywire.ErrAuth) {
		t.Fatalf("NewClient = %v, want ErrAuth", err)
	}
}

func TestAHandshakeThatNeverEndsIsClosedAfter5Seconds(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	nc, err := r.net.Dial(t.Context(), "silent", "pty:1")
	if err != nil {
		t.Fatal(err)
	}
	r.clk.BlockUntil(1) // the server armed its timer
	r.clk.Advance(ptywire.HandshakeTimeout)

	buf := make([]byte, 1)
	if _, err := nc.Read(buf); err == nil {
		t.Fatal("the connection is still open after the handshake timeout")
	}
}

func TestNewMessagesAndFieldsAreIgnoredByAnOlderPeer(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	c := r.dial(t)
	r.start(t, c)
	nc, err := r.net.Dial(t.Context(), "newer-engine", "pty:1")
	if err != nil {
		t.Fatal(err)
	}
	raw := ptywire.NewConn(nc)
	if _, err := ptywire.ClientHandshake(raw, r.secret); err != nil {
		t.Fatal(err)
	}

	// A message from the future, and a known one with a field from the future.
	if _, err := nc.Write([]byte(`{"t":"teleport","id":1,"where":"mars"}` + "\n" + `{"t":"status","id":2,"flux":42}` + "\n")); err != nil {
		t.Fatal(err)
	}

	it1, err1 := raw.Read()
	it2, err2 := raw.Read()
	if err1 != nil || err2 != nil || it1.Msg.Code != ptywire.CodeUnknownMessage || it1.Msg.ID != 1 ||
		it2.Msg.T != ptywire.TypeStatus || it2.Msg.ID != 2 {
		t.Fatalf("replies = %+v (%v), %+v (%v)", it1.Msg, err1, it2.Msg, err2)
	}
}

func TestAnOlderMestrePtyLacksPingAndHistory(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		proto int
		ping  bool
	}{{"previous version", 1, false}, {"current version", 0, true}} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, rigOptions{proto: tt.proto})
			c := r.dial(t)
			a := r.start(t, c)
			r.printed(t, a, []byte("one\r\ntwo\r\nthree\r\n"))

			_, pingErr := c.Ping(t.Context())
			scr, err := c.Screen(t.Context(), 10)

			if tt.ping && pingErr != nil || !tt.ping && !errors.Is(pingErr, terminal.ErrUnsupported) {
				t.Fatalf("Ping = %v, want supported=%v", pingErr, tt.ping)
			}
			if err != nil || (scr.History != "") != tt.ping || scr.Text != "one\ntwo\nthree" {
				t.Fatalf("screen = %q, history %q, %v", scr.Text, scr.History, err)
			}
		})
	}
}

// The "injob" message is how the engine identifies the caller of the CLI on
// Windows (docs/arquitetura/canal-local.md#identificação-e-versão): the
// mestre-pty says whether a process, with its start identity, is in the Job
// Object of the terminal.
func TestInJobSaysWhoIsInTheJobObject(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	c := r.dial(t)
	a := r.start(t, c)
	a.AddToJob(100, "133000000000000001")

	for _, tt := range []struct {
		name  string
		pid   int
		start string
		want  bool
	}{
		{"a process in the Job", 100, "133000000000000001", true},
		{"the same PID with another start identity (a reused PID)", 100, "133000000000000002", false},
		{"a process that is not in it", 101, "133000000000000001", false},
	} {
		in, err := c.InJob(t.Context(), tt.pid, tt.start)
		if err != nil || in != tt.want {
			t.Errorf("%s: InJob = %v, %v, want %v", tt.name, in, err, tt.want)
		}
	}
}

func TestInJobNeedsAMestrePtyThatCanAnswer(t *testing.T) {
	t.Parallel()
	t.Run("a terminal with no Job Object (Linux)", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, rigOptions{noJob: true})
		c := r.dial(t)
		r.start(t, c)
		if _, err := c.InJob(t.Context(), 100, "x"); !errors.Is(err, terminal.ErrUnsupported) {
			t.Fatalf("InJob = %v, want ErrUnsupported", err)
		}
	})
	t.Run("a mestre-pty of protocol 2", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, rigOptions{proto: 2})
		c := r.dial(t)
		r.start(t, c)
		if _, err := c.InJob(t.Context(), 100, "x"); !errors.Is(err, terminal.ErrUnsupported) {
			t.Fatalf("InJob = %v, want ErrUnsupported", err)
		}
	})
	t.Run("before the agent started", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, rigOptions{})
		c := r.dial(t)
		var re *terminal.RemoteError
		if _, err := c.InJob(t.Context(), 100, "x"); !errors.As(err, &re) || re.Code != ptywire.CodeNotRunning {
			t.Fatalf("InJob = %v, want not_running", err)
		}
	})
}

func TestTheMestrePtyLeavesOnlyAfterTheEngineConfirmedTheWholeOutput(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	c := r.dial(t)
	a := r.start(t, c)
	r.printed(t, a, []byte("last words\r\n"))

	a.Exit(3)
	r.clk.BlockUntil(2) // the stability timer and the wait for the last output
	r.clk.Advance(ptyd.DefaultDrainAfterExit)
	<-c.Exited()

	if ex := c.Exit(); ex.Code != 3 || ex.End != uint64(len("last words\r\n")) {
		t.Fatalf("exit = %+v", ex)
	}
	select {
	case <-r.srv.Done():
		t.Fatal("the mestre-pty left before the engine confirmed the output")
	default:
	}
	st, err := c.Status(t.Context())
	if err != nil || st.State != ptywire.StateExited || st.ExitCode == nil || *st.ExitCode != 3 {
		t.Fatalf("status = %+v, %v", st, err)
	}
	if err := c.Ack(t.Context(), c.Exit().End); err != nil {
		t.Fatal(err)
	}
	<-r.srv.Done()
}

func TestStopSignalLeavesTheMestrePtyAndStopKillMakesItLeave(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	c := r.dial(t)
	a := r.start(t, c)

	if err := c.Stop(t.Context(), ptywire.StopSignal); err != nil {
		t.Fatal(err)
	}
	if sig, kill := a.Signals(); sig != 1 || kill != 0 {
		t.Fatalf("signals = %d, kills = %d", sig, kill)
	}
	select {
	case <-r.srv.Done():
		t.Fatal("the mestre-pty left on a plain signal")
	default:
	}

	if err := c.Stop(t.Context(), ptywire.StopKill); err != nil {
		t.Fatal(err)
	}
	<-r.srv.Done()
	if _, kill := a.Signals(); kill != 1 {
		t.Fatalf("kills = %d, want 1", kill)
	}
}

func TestStartRefusesASecondAgentAndReportsFailures(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	c := r.dial(t)
	r.start(t, c)

	_, err := c.Start(t.Context(), ptywire.Msg{Path: "again"})

	var re *terminal.RemoteError
	if !errors.As(err, &re) || re.Code != ptywire.CodeAlreadyStarted {
		t.Fatalf("second Start = %v, want already_started", err)
	}
	r2 := newRig(t, rigOptions{})
	r2.runner.StartErr = errors.New("no such file")
	_, err = r2.dial(t).Start(context.Background(), ptywire.Msg{Path: "x"})
	if !errors.As(err, &re) || re.Code != ptywire.CodeStartFailed || !strings.Contains(re.Text, "no such file") {
		t.Fatalf("failing Start = %v", err)
	}
}
