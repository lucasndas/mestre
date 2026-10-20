package terminal_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/internal/terminal/ptywire"
	"github.com/lucasndas/mestre/pkg/clock"
)

// What a mestre-pty says to the engine is what the agent of the terminal lets it
// say: the agent can take the mestre-pty over. Nothing in the replies is trusted
// to be the size, the order or the number it announces.

// peerWith is a mestre-pty that answers each message as answer says: it gets the
// connection and the request, and returns true to close the connection after it.
func peerWith(t *testing.T, answer func(conn *ptywire.Conn, req *ptywire.Msg) (closeAfter bool)) *terminal.Client {
	t.Helper()
	secret := bytes.Repeat([]byte{7}, ptywire.SecretSize)
	server, client := net.Pipe()
	go func() {
		defer server.Close()
		conn := ptywire.NewConn(server)
		if _, err := ptywire.ServerHandshake(conn, secret, ptywire.Info{Proto: ptywire.ProtoVersion, Version: "1.0.0", PID: 1}); err != nil {
			return
		}
		for {
			it, err := conn.Read()
			if err != nil {
				return
			}
			if it.Msg != nil && answer(conn, it.Msg) {
				return
			}
		}
	}()
	c, err := terminal.NewClient(client, secret, clock.NewFake(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// within bounds a call to 10 s: a client that does not fail where it should
// would wait for a reply that never comes, and the test for the end of the suite.
func within(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := clock.WithTimeout(t.Context(), clock.Real{}, 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func replayOf(ctx context.Context, c *terminal.Client) error {
	_, err := c.Replay(ctx, 0)
	return err
}

// header answers a replay with the header of Len n starting at offset.
func header(req *ptywire.Msg, offset uint64, n int) *ptywire.Msg {
	return &ptywire.Msg{T: ptywire.TypeReplay, ID: req.ID, Offset: offset, Len: n}
}

func frame(req *ptywire.Msg, offset uint64, n int) *ptywire.Frame {
	return &ptywire.Frame{Stream: req.ID, Offset: offset, Data: bytes.Repeat([]byte{'x'}, n)}
}

// CA-TERM-14
// The length that a replay announces comes from the mestre-pty: the engine never
// allocates more than the protocol allows (ptywire.MaxReplay) for it, a header
// answers a replay and only once, and the frames are the bytes the header
// announced, from the offset it said. A mestre-pty that does otherwise is a
// broken connection, and the call fails with an error that says it, instead of
// bringing the engine down (a make with that capacity panics in the goroutine
// that reads the connection) or of giving the caller bytes nobody announced.
func TestAMestrePtyThatBreaksTheReplayProtocolFailsTheConnectionWithAnErrorThatSaysIt(t *testing.T) {
	t.Parallel()
	over := func(n int) func(*ptywire.Conn, *ptywire.Msg) bool {
		return func(conn *ptywire.Conn, req *ptywire.Msg) bool { _ = conn.WriteMsg(header(req, 0, n)); return true }
	}
	for _, tc := range []struct {
		name   string
		call   func(context.Context, *terminal.Client) error
		answer func(*ptywire.Conn, *ptywire.Msg) bool
		says   []string
	}{
		{"one byte over the maximum", replayOf, over(ptywire.MaxReplay + 1), []string{"replay", strconv.Itoa(ptywire.MaxReplay + 1)}},
		{"a terabyte", replayOf, over(1 << 40), []string{"replay", strconv.Itoa(1 << 40)}},
		{"the largest int", replayOf, over(math.MaxInt), []string{"replay", strconv.Itoa(math.MaxInt)}},
		{"a header sent twice", replayOf, func(conn *ptywire.Conn, req *ptywire.Msg) bool {
			// each would allocate, and the first one is garbage for the next
			_ = conn.WriteMsg(header(req, 0, 8))
			_ = conn.WriteMsg(header(req, 0, 8))
			return true
		}, []string{"twice"}},
		{"the header of a replay as the answer to a status", func(ctx context.Context, c *terminal.Client) error {
			_, err := c.Status(ctx)
			return err
		}, func(conn *ptywire.Conn, req *ptywire.Msg) bool { _ = conn.WriteMsg(header(req, 0, 5)); return true },
			[]string{"status", "replay"}},
		{"more bytes than it announced", replayOf, func(conn *ptywire.Conn, req *ptywire.Msg) bool {
			_ = conn.WriteMsg(header(req, 0, 10))
			_ = conn.WriteFrame(frame(req, 0, 20))
			return true
		}, []string{"frame of the replay"}},
		{"a frame from another offset than the one announced", replayOf, func(conn *ptywire.Conn, req *ptywire.Msg) bool {
			_ = conn.WriteMsg(header(req, 100, 10))
			_ = conn.WriteFrame(frame(req, 0, 10))
			return true
		}, []string{"frame of the replay"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.call(within(t), peerWith(t, tc.answer))
			if !errors.Is(err, terminal.ErrClosed) {
				t.Fatalf("the call = %v; want the connection closed", err)
			}
			for _, want := range tc.says {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error %q does not say %q", err, want)
				}
			}
		})
	}

	// what the mestre-pty really sends is not refused: the maximum itself, and a
	// replay that starts where the buffer starts, not at zero, in several frames
	for name, tc := range map[string]struct {
		offset uint64
		n      int
	}{
		"exactly the maximum":                    {0, ptywire.MaxReplay},
		"from an offset in the middle, in parts": {5000, 2*ptywire.MaxFrame + 17},
		"less than one frame":                    {3, 11},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := peerWith(t, func(conn *ptywire.Conn, req *ptywire.Msg) bool {
				if req.T != ptywire.TypeReplay {
					return false
				}
				if conn.WriteMsg(header(req, tc.offset, tc.n)) != nil {
					return true
				}
				for sent := 0; sent < tc.n; sent += ptywire.MaxFrame {
					if conn.WriteFrame(frame(req, tc.offset+uint64(sent), min(ptywire.MaxFrame, tc.n-sent))) != nil {
						return true
					}
				}
				return false
			})
			got, err := c.Replay(within(t), 0)
			if err != nil || len(got.Data) != tc.n || got.Offset != tc.offset {
				t.Fatalf("Replay = %d bytes from offset %d, %v; want %d from %d", len(got.Data), got.Offset, err, tc.n, tc.offset)
			}
		})
	}
}

// CA-TERM-14
// When the connection fails for a violation, what the mestre-pty sent in the same
// breath, and is already in the buffer of the reader, is not handled: the exit of
// the agent it would announce, or the lines of a stream, are as untrustworthy as
// the one before them.
func TestNothingThatComesAfterAViolationOfTheProtocolIsHandled(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		firstLine  ptywire.Msg // the answer to the replay
		wantEvents int32
	}{
		{"after a header over the maximum", ptywire.Msg{T: ptywire.TypeReplay, Len: ptywire.MaxReplay + 1}, 0},
		// the control: the same two lines, the first one fine, and the exit is handled
		{"after a reply that is fine", ptywire.Msg{T: ptywire.TypeReplay}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				secret := bytes.Repeat([]byte{7}, ptywire.SecretSize)
				server, client := net.Pipe()
				go func() {
					defer server.Close()
					conn := ptywire.NewConn(server)
					if _, err := ptywire.ServerHandshake(conn, secret, ptywire.Info{Proto: ptywire.ProtoVersion, Version: "1.0.0", PID: 1}); err != nil {
						return
					}
					for {
						it, err := conn.Read()
						if err != nil {
							return
						}
						if it.Msg == nil || it.Msg.T != ptywire.TypeReplay {
							continue
						}
						// both lines in one write: they reach the buffer of the reader together
						first := tc.firstLine
						first.ID = it.Msg.ID
						code := 0
						one, _ := json.Marshal(&first)
						two, _ := json.Marshal(&ptywire.Msg{T: ptywire.TypeExit, ExitCode: &code})
						_, _ = server.Write(append(append(one, '\n'), append(two, '\n')...))
					}
				}()
				c, err := terminal.NewClient(client, secret, clock.NewFake(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)), 0)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				var events atomic.Int32
				c.SetEventHandler(func(ptywire.Msg) { events.Add(1) })

				_, _ = c.Replay(within(t), 0)
				synctest.Wait() // the reader has gone through what was in its buffer
				if got := events.Load(); got != tc.wantEvents {
					t.Errorf("%d events handled, want %d", got, tc.wantEvents)
				}
			})
		})
	}
}

// An exit announced with no code is not an exit with code 0: whoever decides on
// success (the verification) must be able to tell the two apart.
func TestAnExitWithNoCodeIsNotAnExitWithCodeZero(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		code   *int
		noCode bool
	}{{"no code", nil, true}, {"code zero", new(int), false}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := peerWith(t, func(conn *ptywire.Conn, req *ptywire.Msg) bool {
				_ = conn.WriteMsg(&ptywire.Msg{T: ptywire.TypeExit, ExitCode: tc.code})
				_ = conn.WriteMsg(&ptywire.Msg{T: ptywire.TypeReplay, ID: req.ID})
				return false
			})
			_, _ = c.Replay(within(t), 0)
			select {
			case <-c.Exited():
			case <-within(t).Done():
				t.Fatal("the exit was not seen")
			}
			if ex := c.Exit(); ex.NoCode != tc.noCode || ex.Code != 0 {
				t.Fatalf("exit = %+v, want NoCode %v", ex, tc.noCode)
			}
		})
	}
}
