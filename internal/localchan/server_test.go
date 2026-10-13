package localchan_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/testkit/localrig"
	"github.com/lucasndas/mestre/pkg/clock"
)

// bubble runs f in a testing/synctest bubble with a started rig: the engine
// on the fake network, the real clock whose time only moves when every
// goroutine is blocked (docs/qualidade/dubles.md).
func bubble(t *testing.T, o localrig.Options, f func(t *testing.T, r *localrig.Rig)) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		r := localrig.New(clock.Real{}, o)
		r.Start(t)
		f(t, r)
	})
}

// rawCall plays a CLI by hand: it sends one line and reads until the final
// response (or the end of the stream), returning every response read.
func rawCall(t *testing.T, r *localrig.Rig, host, line string) []localchan.Response {
	t.Helper()
	c, err := r.Dialer(host)(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte(line + "\n")); err != nil {
		t.Fatal(err)
	}
	return readAll(t, c)
}

func readAll(t *testing.T, c net.Conn) []localchan.Response {
	t.Helper()
	var out []localchan.Response
	sc := bufio.NewScanner(c)
	sc.Buffer(nil, localchan.MaxLine+1024)
	for sc.Scan() {
		var resp localchan.Response
		if err := json.Unmarshal(sc.Bytes(), &resp); err != nil {
			t.Fatalf("a line that is not a response: %q", sc.Text())
		}
		out = append(out, resp)
		if !resp.Partial {
			break
		}
	}
	return out
}

func final(t *testing.T, rs []localchan.Response) localchan.Response {
	t.Helper()
	if len(rs) == 0 || rs[len(rs)-1].Partial {
		t.Fatalf("no final response in %+v", rs)
	}
	return rs[len(rs)-1]
}

// agentRig has a character "alice" with the terminal "tA" and a process in it.
func agentRig(r *localrig.Rig) (host string) {
	r.Character("tA", "alice")
	r.Process("alice-proc", "tA")
	return "alice-proc"
}

func list(t *testing.T, r *localrig.Rig, host, id string) localchan.Response {
	t.Helper()
	cl := localchan.NewClient(localchan.ClientConfig{Clock: r.Clock, Dial: r.Dialer(host)})
	resp, err := cl.Call(t.Context(), localchan.Request{ID: id, AgentID: "alice", Cmd: "list"}, localchan.CallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// CA-LOCAL-05
func TestAnIdRunsOnce(t *testing.T) {
	t.Parallel()
	bubble(t, localrig.Options{}, func(t *testing.T, r *localrig.Rig) {
		host := agentRig(r)
		var runs atomic.Int32
		r.Forwarder.Handle("list", localchan.HandlerFunc(func(_ context.Context, c *localchan.Call) localchan.Response {
			return localchan.OK(map[string]any{"run": runs.Add(1), "agent": c.Caller.AgentID})
		}))
		first := list(t, r, host, "01J0000000000000000000AAAA")
		second := list(t, r, host, "01J0000000000000000000AAAA")
		if !first.OK || !second.OK {
			t.Fatalf("responses: %+v / %+v", first, second)
		}
		if string(first.Data) != string(second.Data) {
			t.Fatalf("the second got %s, want the stored %s", second.Data, first.Data)
		}
		if runs.Load() != 1 {
			t.Fatalf("the command ran %d times", runs.Load())
		}
		other := list(t, r, host, "01J0000000000000000000BBBB")
		if runs.Load() != 2 || string(other.Data) == string(first.Data) {
			t.Fatalf("another id must run again: %s (runs %d)", other.Data, runs.Load())
		}
	})
}

func TestAnIdIsNotSharedBetweenCharacters(t *testing.T) {
	t.Parallel()
	bubble(t, localrig.Options{}, func(t *testing.T, r *localrig.Rig) {
		r.Character("tA", "alice")
		r.Character("tB", "bob")
		r.Process("alice-proc", "tA")
		r.Process("bob-proc", "tB")
		var runs atomic.Int32
		r.Forwarder.Handle("list", localchan.HandlerFunc(func(_ context.Context, c *localchan.Call) localchan.Response {
			return localchan.OK(map[string]any{"run": runs.Add(1), "agent": c.Caller.AgentID})
		}))
		a := final(t, rawCall(t, r, "alice-proc", `{"v":1,"id":"same","agent_id":"alice","cmd":"list","args":[],"flags":{},"cwd":""}`))
		b := final(t, rawCall(t, r, "bob-proc", `{"v":1,"id":"same","agent_id":"bob","cmd":"list","args":[],"flags":{},"cwd":""}`))
		if runs.Load() != 2 || string(a.Data) == string(b.Data) {
			t.Fatalf("bob got %s after alice's %s (runs %d): the stored response of one character must not reach another", b.Data, a.Data, runs.Load())
		}
	})
}

func TestAnIdWithAnotherCommandIsABug(t *testing.T) {
	t.Parallel()
	bubble(t, localrig.Options{}, func(t *testing.T, r *localrig.Rig) {
		host := agentRig(r)
		r.Forwarder.Handle("list", localchan.HandlerFunc(func(context.Context, *localchan.Call) localchan.Response {
			return localchan.OK(nil)
		}))
		list(t, r, host, "dup")
		got := final(t, rawCall(t, r, host, `{"v":1,"id":"dup","agent_id":"alice","cmd":"ask","args":[],"flags":{},"cwd":""}`))
		if got.OK || got.Error.Code != localchan.CodeBadRequest {
			t.Fatalf("response = %+v", got)
		}
	})
}

// CA-LOCAL-06
func TestAnUnknownVersionIsAProtocolMismatch(t *testing.T) {
	t.Parallel()
	bubble(t, localrig.Options{}, func(t *testing.T, r *localrig.Rig) {
		host := agentRig(r)
		got := final(t, rawCall(t, r, host, `{"v":99,"id":"x1","agent_id":"alice","cmd":"list","args":[],"flags":{},"cwd":"","future":true}`))
		if got.OK || got.Error.Code != localchan.CodeProtocolMismatch || got.ExitCode() != localchan.ExitUnavailable {
			t.Fatalf("response = %+v", got)
		}
		if got.V != localchan.Version || got.ID != "x1" {
			t.Fatalf("answered in v%d for id %q", got.V, got.ID)
		}
	})
}

func TestBadRequests(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, line, code string
		exit             int
	}{
		{"not json", `not json`, localchan.CodeBadRequest, localchan.ExitInternal},
		{"not an object", `[1,2]`, localchan.CodeBadRequest, localchan.ExitInternal},
		{"no id", `{"v":1,"cmd":"list"}`, localchan.CodeBadRequest, localchan.ExitInternal},
		{"no cmd", `{"v":1,"id":"a"}`, localchan.CodeBadRequest, localchan.ExitInternal},
		{"wrong type", `{"v":1,"id":"a","cmd":"list","args":"x"}`, localchan.CodeBadRequest, localchan.ExitInternal},
		{"too large", `{"v":1,"id":"a","cmd":"list","cwd":"` + strings.Repeat("x", localchan.MaxLine) + `"}`, localchan.CodeTooLarge, localchan.ExitLimit},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			bubble(t, localrig.Options{}, func(t *testing.T, r *localrig.Rig) {
				host := agentRig(r)
				got := final(t, rawCall(t, r, host, tt.line))
				if got.OK || got.Error.Code != tt.code || got.ExitCode() != tt.exit {
					t.Fatalf("response = %+v", got)
				}
			})
		})
	}
}

func TestALineOfExactlyTheLimitIsAccepted(t *testing.T) {
	t.Parallel()
	bubble(t, localrig.Options{}, func(t *testing.T, r *localrig.Rig) {
		host := agentRig(r)
		r.Forwarder.Handle("list", localchan.HandlerFunc(func(context.Context, *localchan.Call) localchan.Response {
			return localchan.OK("big")
		}))
		pre := `{"v":1,"id":"a","agent_id":"alice","cmd":"list","cwd":"`
		line := pre + strings.Repeat("x", localchan.MaxLine-len(pre)-2) + `"}`
		if len(line) != localchan.MaxLine {
			t.Fatalf("test bug: %d bytes", len(line))
		}
		if got := final(t, rawCall(t, r, host, line)); !got.OK {
			t.Fatalf("response = %+v", got)
		}
	})
}

// CA-LOCAL-08 through the server: the first process is in the terminal of
// another character and is refused; the second is in the one of agent_id.
func TestACallerOfAnotherTerminalIsRefused(t *testing.T) {
	t.Parallel()
	bubble(t, localrig.Options{}, func(t *testing.T, r *localrig.Rig) {
		r.Character("tA", "alice")
		r.Character("tB", "bob")
		r.Process("in-A", "tA")
		r.Process("in-B", "tB")
		var seen localchan.Caller
		r.Forwarder.Handle("list", localchan.HandlerFunc(func(_ context.Context, c *localchan.Call) localchan.Response {
			seen = c.Caller
			return localchan.OK("listed")
		}))
		line := `{"v":1,"id":"` + "%s" + `","agent_id":"bob","cmd":"list","args":[],"flags":{},"cwd":""}`
		refused := final(t, rawCall(t, r, "in-A", strings.Replace(line, "%s", "r1", 1)))
		if refused.OK || refused.Error.Code != localchan.CodeNotIdentified || refused.ExitCode() != localchan.ExitDenied {
			t.Fatalf("a process of alice's terminal calling as bob: %+v", refused)
		}
		accepted := final(t, rawCall(t, r, "in-B", strings.Replace(line, "%s", "r2", 1)))
		if !accepted.OK {
			t.Fatalf("a process of bob's terminal calling as bob: %+v", accepted)
		}
		if !seen.Identified || seen.AgentID != "bob" || seen.TerminalID != "tB" {
			t.Fatalf("the handler saw %+v", seen)
		}
		var refusals int
		for _, a := range r.Audits() {
			if a.Code == localchan.CodeNotIdentified && a.AgentID == "bob" && a.Reason != "" {
				refusals++
			}
		}
		if refusals != 1 {
			t.Fatalf("audit trail: %+v", r.Audits())
		}
	})
}

func TestWhatNeedsNoIdentity(t *testing.T) {
	t.Parallel()
	bubble(t, localrig.Options{Config: localchan.Config{
		Instance: "principal", EngineVersion: "9.9.9",
		Local: map[string]localchan.Handler{
			"hook": localchan.HandlerFunc(func(context.Context, *localchan.Call) localchan.Response { return localchan.OK("hooked") }),
		},
	}}, func(t *testing.T, r *localrig.Rig) {
		r.Character("tA", "alice")
		r.Process("shell", "")
		r.Process("alice-proc", "tA")
		r.Forwarder.Handle("list", localchan.HandlerFunc(func(context.Context, *localchan.Call) localchan.Response {
			return localchan.OK("listed")
		}))
		req := func(id, agent, cmd string) string {
			return `{"v":1,"id":"` + id + `","agent_id":"` + agent + `","cmd":"` + cmd + `","args":[],"flags":{},"cwd":""}`
		}
		// Without an id: only help, debug and hook.
		for cmd, wantOK := range map[string]bool{"help": true, "debug": true, "hook": true, "list": false, "note write": false} {
			got := final(t, rawCall(t, r, "shell", req("n-"+cmd, "", cmd)))
			if got.OK != wantOK || (!wantOK && got.Error.Code != localchan.CodeNotIdentified) {
				t.Errorf("%s without an id: %+v", cmd, got)
			}
		}
		// An id that does not match the process: help and debug still answer
		// and say they do not know the caller; hook and the rest are refused.
		dbg := final(t, rawCall(t, r, "shell", req("d1", "alice", "debug")))
		var data struct {
			Engine struct {
				Version  string
				Protocol int
			}
			Caller struct{ Identified bool }
		}
		if err := dbg.DecodeData(&data); err != nil || !dbg.OK {
			t.Fatalf("debug: %+v (%v)", dbg, err)
		}
		if data.Caller.Identified || data.Engine.Version != "9.9.9" || data.Engine.Protocol != localchan.Version {
			t.Fatalf("debug data: %s", dbg.Data)
		}
		if got := final(t, rawCall(t, r, "shell", req("h1", "alice", "hook"))); got.OK {
			t.Fatalf("hook with a forged id was served: %+v", got)
		}
		// The right process: debug says it is identified.
		dbg = final(t, rawCall(t, r, "alice-proc", req("d2", "alice", "debug")))
		_ = dbg.DecodeData(&data)
		if !data.Caller.Identified {
			t.Fatalf("debug of the right process: %s", dbg.Data)
		}
	})
}

func TestWithoutAForwarderTheCitadelIsUnavailable(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := localrig.New(clock.Real{}, localrig.Options{Config: localchan.Config{Forwarder: localchan.UnavailableForwarder{}}})
		r.Start(t)
		host := agentRig(r)
		got := list(t, r, host, "u1")
		if got.OK || got.Error.Code != localchan.CodeCitadelUnavailable || got.ExitCode() != localchan.ExitUnavailable {
			t.Fatalf("response = %+v", got)
		}
	})
}

func TestAnUnknownCommandIsAUsageError(t *testing.T) {
	t.Parallel()
	bubble(t, localrig.Options{}, func(t *testing.T, r *localrig.Rig) {
		host := agentRig(r)
		got := list(t, r, host, "k1") // nothing registered
		if got.OK || got.Error.Code != localchan.CodeUsage || got.ExitCode() != localchan.ExitUsage {
			t.Fatalf("response = %+v", got)
		}
	})
}

func TestAHandlerThatPanicsAnswersInternal(t *testing.T) {
	t.Parallel()
	bubble(t, localrig.Options{}, func(t *testing.T, r *localrig.Rig) {
		host := agentRig(r)
		r.Forwarder.Handle("list", localchan.HandlerFunc(func(context.Context, *localchan.Call) localchan.Response {
			panic("boom")
		}))
		got := list(t, r, host, "p1")
		if got.OK || got.Error.Code != localchan.CodeInternal || got.ExitCode() != localchan.ExitInternal {
			t.Fatalf("response = %+v", got)
		}
	})
}

// gate lets a handler wait for the test.
type gate struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newGate() *gate { return &gate{started: make(chan struct{}), release: make(chan struct{})} }

func (g *gate) enter() { g.once.Do(func() { close(g.started) }) }

// CA-LOCAL-04: a connection that closes (the CLI died) does not cancel the
// request; when the target answers, the result is where the engine puts it
// (here a stand-in for the inbox, which arrives with the waits, M4), and a
// resend of the same id finds the stored response.
func TestAClosedConnectionDoesNotCancelTheRequest(t *testing.T) {
	t.Parallel()
	bubble(t, localrig.Options{}, func(t *testing.T, r *localrig.Rig) {
		host := agentRig(r)
		g := newGate()
		inbox := make(chan string, 1)
		r.Forwarder.Handle("ask", localchan.HandlerFunc(func(ctx context.Context, c *localchan.Call) localchan.Response {
			c.Partial(map[string]any{"status": "queued"})
			g.enter()
			select {
			case <-g.release:
			case <-ctx.Done():
				return localchan.Fail(localchan.CodeInternal, "cancelled", "")
			}
			inbox <- "the answer, for " + c.Caller.AgentID
			return localchan.OK(map[string]any{"response": "42"})
		}))
		conn, err := r.Dialer(host)(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		_, _ = conn.Write([]byte(`{"v":1,"id":"ask1","agent_id":"alice","cmd":"ask","args":["Bob","?"],"flags":{},"cwd":""}` + "\n"))
		<-g.started
		_ = conn.Close() // the CLI dies
		synctest.Wait()
		select {
		case <-inbox:
			t.Fatal("the request ended before the target answered")
		default:
		}
		close(g.release) // the target answers
		if got := <-inbox; got != "the answer, for alice" {
			t.Fatalf("inbox = %q", got)
		}
		// A resend with the same id gets the stored response.
		resend := final(t, rawCall(t, r, host, `{"v":1,"id":"ask1","agent_id":"alice","cmd":"ask","args":["Bob","?"],"flags":{},"cwd":""}`))
		if !resend.OK || !strings.Contains(string(resend.Data), "42") {
			t.Fatalf("resend = %+v", resend)
		}
	})
}

// A resend while the request is still running attaches to it and never runs it again.
func TestAResendJoinsTheRequestInProgress(t *testing.T) {
	t.Parallel()
	bubble(t, localrig.Options{}, func(t *testing.T, r *localrig.Rig) {
		host := agentRig(r)
		g := newGate()
		var runs atomic.Int32
		r.Forwarder.Handle("ask", localchan.HandlerFunc(func(_ context.Context, c *localchan.Call) localchan.Response {
			runs.Add(1)
			c.Partial(map[string]any{"status": "queued"})
			g.enter()
			<-g.release
			return localchan.OK("done")
		}))
		line := `{"v":1,"id":"ask2","agent_id":"alice","cmd":"ask","args":[],"flags":{},"cwd":""}`
		first, err := r.Dialer(host)(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		_, _ = first.Write([]byte(line + "\n"))
		<-g.started
		_ = first.Close()
		second, err := r.Dialer(host)(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer second.Close()
		_, _ = second.Write([]byte(line + "\n"))
		synctest.Wait()
		close(g.release)
		rs := readAll(t, second)
		if got := final(t, rs); !got.OK || runs.Load() != 1 {
			t.Fatalf("response %+v after %d runs", got, runs.Load())
		}
		// It got the partial the first connection saw, too.
		if len(rs) < 2 || !rs[0].Partial || !strings.Contains(string(rs[0].Data), "queued") {
			t.Fatalf("responses = %+v", rs)
		}
	})
}

func TestTheEngineSendsASignOfLifeWhileARequestRuns(t *testing.T) {
	t.Parallel()
	bubble(t, localrig.Options{}, func(t *testing.T, r *localrig.Rig) {
		host := agentRig(r)
		g := newGate()
		r.Forwarder.Handle("ask", localchan.HandlerFunc(func(context.Context, *localchan.Call) localchan.Response {
			g.enter()
			<-g.release
			return localchan.OK("done")
		}))
		conn, err := r.Dialer(host)(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_, _ = conn.Write([]byte(`{"v":1,"id":"hb","agent_id":"alice","cmd":"ask","args":[],"flags":{},"cwd":""}` + "\n"))
		got := make(chan []localchan.Response, 1)
		go func() { got <- readAll(t, conn) }()
		<-g.started
		if err := clock.Sleep(t.Context(), clock.Real{}, 50*time.Second); err != nil {
			t.Fatal(err)
		}
		close(g.release)
		rs := <-got
		alive := 0
		for _, r := range rs {
			if r.Partial && strings.Contains(string(r.Data), "alive") {
				alive++
			}
		}
		if alive != 3 { // 15 s, 30 s and 45 s
			t.Fatalf("%d signs of life in 50 s, want 3: %+v", alive, rs)
		}
	})
}

// A server that shuts down ends the requests that are running.
func TestShuttingDownCancelsTheRequests(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := localrig.New(clock.Real{}, localrig.Options{})
		r.Start(t)
		host := agentRig(r)
		g := newGate()
		ended := make(chan struct{})
		r.Forwarder.Handle("ask", localchan.HandlerFunc(func(ctx context.Context, _ *localchan.Call) localchan.Response {
			g.enter()
			<-ctx.Done()
			close(ended)
			return localchan.Fail(localchan.CodeUnavailable, "shutting down", "")
		}))
		conn, err := r.Dialer(host)(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_, _ = conn.Write([]byte(`{"v":1,"id":"sd","agent_id":"alice","cmd":"ask","args":[],"flags":{},"cwd":""}` + "\n"))
		<-g.started
		r.Stop()
		<-ended
	})
}
