package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/cli"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/testkit/localrig"
	"github.com/lucasndas/mestre/pkg/clock"
)

// result is what a run of the CLI left behind.
type result struct {
	code           int
	stdout, stderr string
}

// proc is a process of the test that runs `mestre`: the name of the host it
// dials from, and its environment.
type proc struct {
	rig  *localrig.Rig
	host string
	env  map[string]string
	// dials counts the connections the CLI opened.
	dials atomic.Int32
	// commands is the definition of the commands (nil: the default).
	commands *cli.Registry
	// files is the file system the CLI reads (nil: none), stdin its input.
	files map[string][]byte
	stdin string
}

// newProc registers a process in the terminal of a character (or none) and
// gives it the environment the engine gives a character.
func newProc(r *localrig.Rig, host, terminal, agent string) *proc {
	p := &proc{rig: r, host: host, env: map[string]string{}}
	if agent != "" {
		p.env[localchan.EnvAgentID] = agent
	}
	r.Process(host, terminal)
	return p
}

func (p *proc) run(ctx context.Context, args ...string) result {
	var stdout, stderr bytes.Buffer
	code := cli.Run(ctx, cli.Env{
		Getenv: func(k string) string { return p.env[k] },
		Stdout: &stdout, Stderr: &stderr, Clock: p.rig.Clock, Cwd: "/work", Version: "1.2.3",
		Dial: func(string) localchan.Dialer {
			d := p.rig.Dialer(p.host)
			return func(ctx context.Context) (net.Conn, error) {
				p.dials.Add(1)
				return d(ctx)
			}
		},
		FileExists: func(string) bool { return true },
		Commands:   p.commands,
		Stdin:      strings.NewReader(p.stdin),
		ReadFile: func(path string, max int64) ([]byte, error) {
			b, ok := p.files[path]
			if !ok {
				return nil, fmt.Errorf("open %s: %w", path, fs.ErrNotExist)
			}
			return b[:min(int64(len(b)), max)], nil
		},
		FileSize: func(path string) (int64, error) {
			b, ok := p.files[path]
			if !ok {
				return 0, fmt.Errorf("stat %s: %w", path, fs.ErrNotExist)
			}
			return int64(len(b)), nil
		},
	}, args)
	return result{code, stdout.String(), stderr.String()}
}

// bubble runs f in a testing/synctest bubble with a started rig.
func bubble(t *testing.T, f func(t *testing.T, r *localrig.Rig)) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		r := localrig.New(clock.Real{}, localrig.Options{})
		r.Start(t)
		f(t, r)
	})
}

func respondWith(r *localrig.Rig, cmd string, resp localchan.Response) {
	r.Forwarder.Handle(cmd, localchan.HandlerFunc(func(context.Context, *localchan.Call) localchan.Response { return resp }))
}

// CA-LOCAL-01
func TestTheExitCodeIsTheOneOfTheResponse(t *testing.T) {
	t.Parallel()
	nine := 9
	cases := []struct {
		name string
		resp localchan.Response
		want int
	}{
		{"ok without exit", localchan.OK(map[string]any{"text": "fine"}), 0},
		{"ok with exit 9", localchan.OK(map[string]any{"queued": true}).WithExit(nine), 9},
		{"internal", localchan.Fail(localchan.CodeInternal, "bug", ""), 1},
		{"usage", localchan.Fail(localchan.CodeUsage, "bad flag", ""), 2},
		{"denied", localchan.Fail(localchan.CodeDenied, "no", ""), 3},
		{"not_found", localchan.Fail(localchan.CodeNotFound, "none", ""), 4},
		{"unavailable", localchan.Fail(localchan.CodeUnavailable, "away", ""), 5},
		{"timeout", localchan.Fail(localchan.CodeTimeout, "slow", ""), 6},
		{"conflict", localchan.Fail(localchan.CodeConflict, "busy", ""), 7},
		{"limit", localchan.Fail(localchan.CodeLimit, "full", ""), 8},
		{"pending", localchan.Fail(localchan.CodePending, "wait", ""), 9},
		{"a code of a family", localchan.Fail("not_visible", "who?", "see mestre list"), 4},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			bubble(t, func(t *testing.T, r *localrig.Rig) {
				r.Character("tA", "alice")
				p := newProc(r, "alice-proc", "tA", "alice")
				respondWith(r, "list", tt.resp)
				if got := p.run(t.Context(), "list"); got.code != tt.want {
					t.Fatalf("exit = %d, want %d (stdout %q, stderr %q)", got.code, tt.want, got.stdout, got.stderr)
				}
			})
		})
	}
}

// CA-CLI-CONV-01: with --json a failure is a single object on stdout, an
// empty stderr and the exit code of the table.
func TestAFailureWithJSONIsOneObjectOnStdout(t *testing.T) {
	t.Parallel()
	bubble(t, func(t *testing.T, r *localrig.Rig) {
		r.Character("tA", "alice")
		p := newProc(r, "alice-proc", "tA", "alice")
		respondWith(r, "list", localchan.Fail("not_connected", "Revisor is not connected to you", "ask the Master of the table"))
		got := p.run(t.Context(), "list", "--json")
		if got.code != 3 || got.stderr != "" {
			t.Fatalf("exit %d, stderr %q", got.code, got.stderr)
		}
		var v struct {
			OK    bool `json:"ok"`
			Error struct{ Code, Message, Hint string }
		}
		dec := json.NewDecoder(strings.NewReader(got.stdout))
		if err := dec.Decode(&v); err != nil || dec.More() {
			t.Fatalf("stdout is not one object: %q (%v)", got.stdout, err)
		}
		if v.OK || v.Error.Code != "not_connected" || v.Error.Hint == "" {
			t.Fatalf("object = %+v", v)
		}
		// Text mode: two lines on stderr, nothing on stdout.
		text := p.run(t.Context(), "list")
		if text.code != 3 || text.stdout != "" || !strings.HasPrefix(text.stderr, "error: ") || !strings.Contains(text.stderr, "\nhint: ask the Master") {
			t.Fatalf("text: %+v", text)
		}
	})
}

// CA-LOCAL-02: three partials, then the answer: stdout has the answer only.
func TestOnlyTheFinalAnswerIsPrinted(t *testing.T) {
	t.Parallel()
	bubble(t, func(t *testing.T, r *localrig.Rig) {
		r.Character("tA", "alice")
		p := newProc(r, "alice-proc", "tA", "alice")
		r.Forwarder.Handle("ask", localchan.HandlerFunc(func(_ context.Context, c *localchan.Call) localchan.Response {
			for _, s := range []string{"queued", "delivered", "answering"} {
				c.Partial(map[string]any{"status": s})
			}
			return localchan.OK(map[string]any{"status": "answered", "response": "ship it"})
		}))
		got := p.run(t.Context(), "ask", "Revisor", "revise o PR")
		if got.code != 0 || got.stdout != "ship it\n" || got.stderr != "" {
			t.Fatalf("result = %+v", got)
		}
		// The partials are shown only with --debug, on stderr.
		dbg := p.run(t.Context(), "ask", "Revisor", "revise o PR", "--debug")
		if dbg.stdout != "ship it\n" {
			t.Fatalf("stdout with --debug = %q", dbg.stdout)
		}
	})
}

// CA-LOCAL-03: an ask with --timeout 1m and no answer: after 1 min and 5 s
// the CLI leaves with 6, and the request is still pending in the engine.
func TestATimedOutAskLeavesTheRequestPending(t *testing.T) {
	t.Parallel()
	bubble(t, func(t *testing.T, r *localrig.Rig) {
		r.Character("tA", "alice")
		p := newProc(r, "alice-proc", "tA", "alice")
		started := make(chan struct{})
		var finished atomic.Bool
		release := make(chan struct{})
		r.Forwarder.Handle("ask", localchan.HandlerFunc(func(ctx context.Context, c *localchan.Call) localchan.Response {
			c.Partial(map[string]any{"status": "queued"})
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
			}
			finished.Store(true)
			return localchan.OK(map[string]any{"response": "late"})
		}))
		begin := clock.Real{}.Now() // inside the bubble: virtual time
		res := make(chan result, 1)
		go func() { res <- p.run(t.Context(), "ask", "Revisor", "revise", "--timeout", "1m") }()
		<-started
		got := <-res
		if got.code != 6 || !strings.Contains(got.stderr, "pending") {
			t.Fatalf("result = %+v", got)
		}
		if elapsed := clock.Since(clock.Real{}, begin); elapsed < 65*time.Second || elapsed > 66*time.Second {
			t.Fatalf("the CLI waited %v, want 1 min and 5 s", elapsed)
		}
		if finished.Load() {
			t.Fatal("the request was cancelled with the connection")
		}
		close(release)
	})
}

// CA-LOCAL-06: an engine that answers protocol_mismatch makes the CLI leave with 5.
func TestAProtocolMismatchIsExit5(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := localrig.New(clock.Real{}, localrig.Options{})
		p := newProc(r, "alice-proc", "", "alice")
		// An engine of another version: it answers every request with the mismatch.
		ln, err := r.Net.Listen(localrig.Addr)
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		go func() {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
			buf := make([]byte, 4096)
			n, _ := c.Read(buf)
			var req localchan.Request
			_ = json.Unmarshal(buf[:n], &req)
			_, _ = c.Write([]byte(`{"v":1,"id":"` + req.ID + `","ok":false,"error":{"code":"protocol_mismatch","message":"update mestre","details":{}}}` + "\n"))
		}()
		got := p.run(t.Context(), "list")
		if got.code != 5 || !strings.Contains(got.stderr, "update mestre") {
			t.Fatalf("result = %+v", got)
		}
	})
}

// CA-CLI-CONV-02: a process without MESTRE_AGENT_ID is refused with 3 and
// not_identified, and mestre help works.
func TestWithoutAnAgentIdOnlyHelpWorks(t *testing.T) {
	t.Parallel()
	bubble(t, func(t *testing.T, r *localrig.Rig) {
		respondWith(r, "list", localchan.OK("never"))
		p := newProc(r, "a-person", "", "")
		got := p.run(t.Context(), "list", "--json")
		var v struct {
			OK    bool
			Error struct{ Code string }
		}
		if err := json.Unmarshal([]byte(got.stdout), &v); err != nil || got.code != 3 || v.OK || v.Error.Code != "not_identified" {
			t.Fatalf("list: %+v (%v)", got, err)
		}
		help := p.run(t.Context(), "help")
		if help.code != 0 || !strings.Contains(help.stdout, "mestre ask") || !strings.Contains(help.stdout, "list") {
			t.Fatalf("help: %+v", help)
		}
	})
}

func TestHelpWithoutAnEngineShowsEverythingAndSaysSo(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := localrig.New(clock.Real{}, localrig.Options{}) // not started
		p := newProc(r, "a-person", "", "")
		got := p.run(t.Context(), "help")
		if got.code != 0 || !strings.Contains(got.stdout, "mestre ask") || !strings.Contains(got.stderr, "not filtered") {
			t.Fatalf("help: %+v", got)
		}
	})
}

func TestUsageErrorsAreExit2(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := localrig.New(clock.Real{}, localrig.Options{})
		p := newProc(r, "alice-proc", "", "alice")
		for _, args := range [][]string{
			{}, {"nonsense"}, {"list", "extra"}, {"ask", "A", "B", "C"}, {"ask", "A", "B", "--timeout", "soon"}, {"ask", "A", "B", "--nope"},
			{"ask", "A", "B", "--timeout"},
		} {
			if got := p.run(t.Context(), args...); got.code != 2 || got.stdout != "" || !strings.HasPrefix(got.stderr, "error: ") {
				t.Errorf("mestre %v = %+v, want exit 2", args, got)
			}
		}
		if got := p.run(t.Context(), "ask", "--help"); got.code != 0 || !strings.Contains(got.stdout, "--timeout") {
			t.Errorf("ask --help = %+v", got)
		}
		if p.dials.Load() != 0 {
			t.Errorf("a bad command line reached the engine")
		}
	})
}

// CA-LOCAL-12, first half: an engine that refuses connections for 20 s of
// fake time, then comes back: the call goes through.
func TestTheCLIWaitsForAnEngineThatIsRestarting(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := localrig.New(clock.Real{}, localrig.Options{})
		r.Character("tA", "alice")
		p := newProc(r, "alice-proc", "tA", "alice")
		respondWith(r, "list", localchan.OK(map[string]any{"text": "the list"}))
		res := make(chan result, 1)
		begin := clock.Real{}.Now()
		go func() { res <- p.run(t.Context(), "list") }()
		if err := clock.Sleep(t.Context(), clock.Real{}, 20*time.Second); err != nil {
			t.Fatal(err)
		}
		r.Start(t) // the engine is back
		got := <-res
		if got.code != 0 || got.stdout != "the list\n" {
			t.Fatalf("result = %+v", got)
		}
		if p.dials.Load() < 6 {
			t.Fatalf("%d attempts in 20 s: the wait should grow, not stay long", p.dials.Load())
		}
		if p.dials.Load() > 20 {
			t.Fatalf("%d attempts in 20 s: the wait should grow", p.dials.Load())
		}
		if took := clock.Since(clock.Real{}, begin); took > 23*time.Second {
			t.Fatalf("the call took %v after an engine back at 20 s", took)
		}
	})
}

// CA-LOCAL-12, second half: the engine out for more than 60 s: exit 5.
func TestTheCLIGivesUpOnAnEngineThatStaysOut(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := localrig.New(clock.Real{}, localrig.Options{}) // never started
		p := newProc(r, "alice-proc", "", "alice")
		begin := clock.Real{}.Now()
		got := p.run(t.Context(), "list", "--json")
		var v struct {
			OK    bool
			Error struct{ Code string }
		}
		if err := json.Unmarshal([]byte(got.stdout), &v); err != nil || got.code != 5 || v.Error.Code != "engine_unreachable" {
			t.Fatalf("result = %+v (%v)", got, err)
		}
		if took := clock.Since(clock.Real{}, begin); took < 60*time.Second || took > 61*time.Second {
			t.Fatalf("gave up after %v, want 60 s", took)
		}
	})
}

// CA-LOCAL-12, third part: the connection drops in the middle of an ask;
// the CLI resends the same id, and the engine runs the ask once.
func TestAnAskCutInTheMiddleIsResentAndRunsOnce(t *testing.T) {
	t.Parallel()
	bubble(t, func(t *testing.T, r *localrig.Rig) {
		r.Character("tA", "alice")
		p := newProc(r, "alice-proc", "tA", "alice")
		started := make(chan struct{})
		release := make(chan struct{})
		var runs, ids atomic.Int32
		var seen sync.Map
		r.Forwarder.Handle("ask", localchan.HandlerFunc(func(_ context.Context, c *localchan.Call) localchan.Response {
			if _, dup := seen.LoadOrStore(c.Request.ID, true); !dup {
				ids.Add(1)
			}
			if runs.Add(1) == 1 {
				close(started)
			}
			<-release
			return localchan.OK(map[string]any{"response": "once"})
		}))
		res := make(chan result, 1)
		go func() { res <- p.run(t.Context(), "ask", "Revisor", "revise") }()
		<-started
		r.Net.Cut(p.host, "engine") // the engine's side of the connection drops
		synctest.Wait()
		close(release)
		got := <-res
		if got.code != 0 || got.stdout != "once\n" {
			t.Fatalf("result = %+v", got)
		}
		if runs.Load() != 1 {
			t.Fatalf("the ask ran %d times", runs.Load())
		}
		if p.dials.Load() != 2 {
			t.Fatalf("%d connections: the CLI should have reconnected once", p.dials.Load())
		}
	})
}

// A silent engine (no sign of life for 45 s) is taken for dead; the CLI
// reconnects and resends, and the request still runs once.
func TestASilentEngineIsTakenForDead(t *testing.T) {
	t.Parallel()
	bubble(t, func(t *testing.T, r *localrig.Rig) {
		r.Character("tA", "alice")
		p := newProc(r, "alice-proc", "tA", "alice")
		started := make(chan struct{})
		release := make(chan struct{})
		var runs atomic.Int32
		r.Forwarder.Handle("ask", localchan.HandlerFunc(func(context.Context, *localchan.Call) localchan.Response {
			if runs.Add(1) == 1 {
				close(started)
			}
			<-release
			return localchan.OK(map[string]any{"response": "once"})
		}))
		res := make(chan result, 1)
		go func() { res <- p.run(t.Context(), "ask", "Revisor", "revise") }()
		<-started
		r.Net.HalfOpen(p.host, "engine") // nothing gets through, no end-of-stream either
		if err := clock.Sleep(t.Context(), clock.Real{}, 50*time.Second); err != nil {
			t.Fatal(err)
		}
		r.Net.Heal(p.host, "engine")
		close(release)
		got := <-res
		if got.code != 0 || got.stdout != "once\n" || runs.Load() != 1 {
			t.Fatalf("result = %+v after %d runs", got, runs.Load())
		}
		if p.dials.Load() < 2 {
			t.Fatalf("%d connections: the CLI should have dropped the silent one", p.dials.Load())
		}
	})
}

// CA-CLI-CONV-09: with the engine stopped, mestre debug shows item 3 as
// fail, with a hint, and leaves with 5.
func TestDebugWithTheEngineStopped(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := localrig.New(clock.Real{}, localrig.Options{}) // not started
		p := newProc(r, "alice-proc", "", "alice")
		begin := clock.Real{}.Now()
		got := p.run(t.Context(), "debug")
		if got.code != 5 {
			t.Fatalf("exit = %d\n%s", got.code, got.stdout)
		}
		lines := strings.Split(strings.TrimSpace(got.stdout), "\n")
		if len(lines) < 6 || !strings.HasPrefix(lines[0], "1. version") || !strings.Contains(lines[2], "3. engine") || !strings.Contains(lines[2], "fail") ||
			!strings.Contains(lines[3], "hint:") {
			t.Fatalf("output:\n%s", got.stdout)
		}
		if clock.Since(clock.Real{}, begin) > time.Second {
			t.Fatalf("debug waited %v for the engine: it must answer, not wait", clock.Since(clock.Real{}, begin))
		}
		js := p.run(t.Context(), "debug", "--json")
		var v struct {
			OK    bool
			Error struct{ Code string }
			Data  struct {
				Checks []struct{ Name, Status string }
			}
		}
		if err := json.Unmarshal([]byte(js.stdout), &v); err != nil || v.OK || v.Error.Code != "engine_unreachable" || len(v.Data.Checks) != 5 || js.stderr != "" {
			t.Fatalf("json: %+v (%v)", js, err)
		}
	})
}

func TestDebugItemsOneToFive(t *testing.T) {
	t.Parallel()
	type check struct{ Name, Status, Detail string }
	run := func(t *testing.T, p *proc) (int, []check) {
		t.Helper()
		got := p.run(t.Context(), "debug", "--json")
		var v struct {
			Data struct{ Checks []check }
		}
		if err := json.Unmarshal([]byte(got.stdout), &v); err != nil {
			t.Fatalf("%q: %v", got.stdout, err)
		}
		return got.code, v.Data.Checks
	}
	want := func(t *testing.T, cs []check, statuses ...string) {
		t.Helper()
		if len(cs) != len(statuses) {
			t.Fatalf("checks = %+v", cs)
		}
		for i, c := range cs {
			if c.Status != statuses[i] {
				t.Fatalf("check %d (%s) is %s, want %s: %+v", i+1, c.Name, c.Status, statuses[i], cs)
			}
		}
	}
	t.Run("a character in its terminal", func(t *testing.T) {
		t.Parallel()
		bubble(t, func(t *testing.T, r *localrig.Rig) {
			r.Character("tA", "alice")
			p := newProc(r, "alice-proc", "tA", "alice")
			p.env[localchan.EnvEndpoint] = "unix:///run/mestre/principal/cli.sock"
			p.env[localchan.EnvCLI] = "/usr/bin/mestre"
			code, cs := run(t, p)
			want(t, cs, "ok", "ok", "ok", "ok", "skipped")
			if code != 0 || !strings.Contains(cs[0].Detail, "1.2.3") || !strings.Contains(cs[0].Detail, "v1") || !strings.Contains(cs[2].Detail, "answered in") {
				t.Fatalf("exit %d, checks %+v", code, cs)
			}
		})
	})
	t.Run("a person in a shell: item 4 is skipped", func(t *testing.T) {
		t.Parallel()
		bubble(t, func(t *testing.T, r *localrig.Rig) {
			p := newProc(r, "a-person", "", "")
			code, cs := run(t, p)
			want(t, cs, "ok", "ok", "ok", "skipped", "skipped")
			if code != 0 {
				t.Fatalf("exit = %d", code)
			}
		})
	})
	t.Run("the id of a character used outside its terminal", func(t *testing.T) {
		t.Parallel()
		bubble(t, func(t *testing.T, r *localrig.Rig) {
			r.Character("tA", "alice")
			p := newProc(r, "intruder", "", "alice")
			code, cs := run(t, p)
			want(t, cs, "ok", "ok", "ok", "fail", "skipped")
			if code != 3 {
				t.Fatalf("exit = %d, want 3", code)
			}
		})
	})
	t.Run("a malformed endpoint", func(t *testing.T) {
		t.Parallel()
		bubble(t, func(t *testing.T, r *localrig.Rig) {
			p := newProc(r, "alice-proc", "", "alice")
			p.env[localchan.EnvEndpoint] = "tcp://127.0.0.1:80"
			code, cs := run(t, p)
			if code != 2 || cs[1].Status != "fail" {
				t.Fatalf("exit %d, checks %+v", code, cs)
			}
		})
	})
}
