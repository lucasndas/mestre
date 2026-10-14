package localchan_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/testkit/localrig"
	"github.com/lucasndas/mestre/pkg/clock"
)

// A process that floods the engine with connections does not take it from
// the others: past MaxConnsPerPeer the extra connections of that process are
// refused with `limit` before anything is read, the other processes are
// served, and once its connections close it is served again; past MaxConns
// a new connection is closed at once.
func TestTheConnectionsOfOneProcessAreBounded(t *testing.T) {
	t.Parallel()
	bubble(t, localrig.Options{Config: localchan.Config{MaxConnsPerPeer: 2, MaxConns: 4}}, func(t *testing.T, r *localrig.Rig) {
		r.Process("flood", "")
		r.Process("other", "")
		debug := `{"v":1,"id":"%s","agent_id":"","cmd":"debug","args":[],"flags":{},"cwd":""}`
		hold := func(host string) net.Conn {
			c, err := r.Dialer(host)(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			return c
		}
		a, b := hold("flood"), hold("flood")
		synctest.Wait()
		if got := final(t, rawCall(t, r, "flood", fmt.Sprintf(debug, "f3"))); got.OK || got.Error.Code != localchan.CodeLimit {
			t.Fatalf("the third connection of the process: %+v; want limit", got)
		}
		if got := final(t, rawCall(t, r, "other", fmt.Sprintf(debug, "o1"))); !got.OK {
			t.Fatalf("another process: %+v", got)
		}
		_ = a.Close()
		synctest.Wait()
		if got := final(t, rawCall(t, r, "flood", fmt.Sprintf(debug, "f4"))); !got.OK {
			t.Fatalf("the process again, below the limit: %+v", got)
		}
		// The whole server: b, two of other and one more are 4; the next is
		// closed with nothing read or answered.
		o1, o2, o3 := hold("other"), hold("other"), hold("flood")
		synctest.Wait()
		c := hold("other")
		if n, err := c.Read(make([]byte, 1)); n != 0 || err != io.EOF {
			t.Fatalf("a connection over the limit of the server: read %d, %v; want it closed", n, err)
		}
		for _, x := range []net.Conn{b, o1, o2, o3, c} {
			_ = x.Close()
		}
	})
}

// hookOK answers every hook call, as the engine of the hook dispatcher does.
var hookOK = map[string]localchan.Handler{localchan.CmdHook: localchan.HandlerFunc(func(context.Context, *localchan.Call) localchan.Response {
	return localchan.OK(map[string]any{"verdict": "allow"})
})}

const hookLine = `{"v":1,"id":"%s","agent_id":"bob","cmd":"hook","args":[{}],"flags":{},"cwd":""}`

// CA-LOCAL-14
// One agent cannot fill the engine for the others: with the defaults, 32
// processes of one terminal holding 16 idle connections each (the probe of
// the audit: 512 connections renewed under the read timeout) keep at most
// the cap of their terminal, and the hook call of another character is
// served.
func TestOneTerminalCannotStarveAnotherTerminalsHookCalls(t *testing.T) {
	t.Parallel()
	bubble(t, localrig.Options{Config: localchan.Config{Local: hookOK}}, func(t *testing.T, r *localrig.Rig) {
		r.Character("tB", "bob")
		r.Process("bob-proc", "tB")
		var held []net.Conn
		for p := range 32 {
			h := fmt.Sprintf("flood%d", p)
			r.Process(h, "tA")
			for range 16 {
				c, err := r.Dialer(h)(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				held = append(held, c)
			}
		}
		synctest.Wait()
		// The ones over the cap of the terminal were answered at once, the
		// others wait for a request that never comes.
		refused := 0
		for _, c := range held {
			if answered(c) {
				refused++
			}
		}
		if want := 32*16 - localchan.DefaultMaxConnsPerGroup; refused != want {
			t.Fatalf("%d of the flooding connections refused; want %d (all over the cap of %d per terminal)", refused, want, localchan.DefaultMaxConnsPerGroup)
		}
		if got := final(t, rawCall(t, r, "bob-proc", fmt.Sprintf(hookLine, "b1"))); !got.OK {
			t.Fatalf("the hook call of another terminal with the flood held: %+v", got.Error)
		}
		for _, c := range held {
			_ = c.Close()
		}
	})
}

// answered reports whether the engine already wrote to c, without waiting
// for more.
func answered(c net.Conn) bool {
	_ = c.SetReadDeadline(clock.Real{}.Now().Add(time.Nanosecond))
	var b [1]byte
	n, _ := c.Read(b[:])
	return n > 0
}

// The cap of a terminal answers `limit` before anything is read, and the
// processes of other terminals are not counted in it; the cap of a user does
// the same across terminals when the system tells the user.
func TestTheConnectionsOfOneTerminalAndOneUserAreBounded(t *testing.T) {
	t.Parallel()
	bubble(t, localrig.Options{Config: localchan.Config{MaxConnsPerPeer: 2, MaxConnsPerGroup: 3, MaxConnsPerUID: 5, Local: hookOK}}, func(t *testing.T, r *localrig.Rig) {
		r.Character("tB", "bob")
		r.Process("bob-proc", "tB")
		r.Process("a1", "tA")
		r.Process("a2", "tA")
		for _, h := range []string{"u1", "u2", "u3", "u4"} {
			r.ProcessOf(h, "tU"+h, "1001")
		}
		hold := func(host string) net.Conn {
			c, err := r.Dialer(host)(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			return c
		}
		a, b, c := hold("a1"), hold("a1"), hold("a2")
		synctest.Wait()
		if got := final(t, rawCall(t, r, "a2", fmt.Sprintf(hookLine, "a3"))); got.OK || got.Error.Code != localchan.CodeLimit {
			t.Fatalf("the fourth connection of the terminal: %+v; want limit", got)
		}
		if got := final(t, rawCall(t, r, "bob-proc", fmt.Sprintf(hookLine, "b1"))); !got.OK {
			t.Fatalf("another terminal: %+v", got.Error)
		}
		// Three processes of one user, in three terminals, 2+2+1
		// connections: the sixth of the user is over its cap.
		var users []net.Conn
		for _, h := range []string{"u1", "u1", "u2", "u2", "u3"} {
			users = append(users, hold(h))
		}
		synctest.Wait()
		if got := final(t, rawCall(t, r, "u4", fmt.Sprintf(hookLine, "u6"))); got.OK || got.Error.Code != localchan.CodeLimit {
			t.Fatalf("the sixth connection of the user: %+v; want limit", got)
		}
		_ = a.Close()
		synctest.Wait()
		if got := final(t, rawCall(t, r, "a2", fmt.Sprintf(hookLine, "a4"))); got.Error != nil && got.Error.Code == localchan.CodeLimit {
			t.Fatalf("the terminal below its cap again: %+v", got.Error)
		}
		for _, x := range append(users, b, c) {
			_ = x.Close()
		}
	})
}

// The last connections of the engine are for the hook calls: past
// MaxConns-ReservedForHook another call is refused with `limit` while a hook
// call is served.
func TestTheLastConnectionsAreForHookCalls(t *testing.T) {
	t.Parallel()
	bubble(t, localrig.Options{Config: localchan.Config{MaxConns: 6, ReservedForHook: 2, Local: hookOK}}, func(t *testing.T, r *localrig.Rig) {
		r.Character("tB", "bob")
		r.Process("bob-proc", "tB")
		var held []net.Conn
		for i := range 4 {
			h := fmt.Sprintf("flood%d", i)
			r.Process(h, fmt.Sprintf("tF%d", i))
			c, err := r.Dialer(h)(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			held = append(held, c)
		}
		synctest.Wait()
		if got := final(t, rawCall(t, r, "bob-proc", `{"v":1,"id":"d1","agent_id":"bob","cmd":"debug","args":[],"flags":{},"cwd":""}`)); got.OK || got.Error.Code != localchan.CodeLimit {
			t.Fatalf("a call that is not a hook with the engine nearly full: %+v; want limit", got)
		}
		if got := final(t, rawCall(t, r, "bob-proc", fmt.Sprintf(hookLine, "h1"))); !got.OK {
			t.Fatalf("a hook call with the engine nearly full: %+v", got.Error)
		}
		for _, c := range held {
			_ = c.Close()
		}
	})
}

// A connection that sends nothing is closed after the read timeout, so the
// idle connections of a flood are held for seconds, not for a minute.
func TestAnIdleConnectionIsClosedAfterTheShortReadTimeout(t *testing.T) {
	t.Parallel()
	bubble(t, localrig.Options{}, func(t *testing.T, r *localrig.Rig) {
		r.Process("idle", "")
		c, err := r.Dialer("idle")(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if localchan.DefaultReadTimeout > 5*time.Second {
			t.Fatalf("the default read timeout is %v; want a few seconds", localchan.DefaultReadTimeout)
		}
		if err := clock.Sleep(t.Context(), clock.Real{}, localchan.DefaultReadTimeout+time.Second); err != nil {
			t.Fatal(err)
		}
		if n, err := c.Read(make([]byte, 1)); n != 0 || err != io.EOF {
			t.Fatalf("an idle connection after the read timeout: read %d, %v; want it closed", n, err)
		}
	})
}

// A client whose connection the engine closes at once (it is full) does not
// reconnect in a tight loop: it waits, longer each time, with jitter, and
// its time limit still ends the call.
func TestTheClientBacksOffWhenTheEngineKeepsClosingItsConnection(t *testing.T) {
	t.Parallel()
	bubble(t, localrig.Options{Config: localchan.Config{MaxConns: 2, ReservedForHook: -1, ReadTimeout: time.Hour}}, func(t *testing.T, r *localrig.Rig) {
		r.Process("bob-proc", "")
		r.Process("flood", "")
		a, _ := r.Dialer("flood")(t.Context())
		b, _ := r.Dialer("flood")(t.Context())
		synctest.Wait()
		dials := 0
		d := r.Dialer("bob-proc")
		cl := localchan.NewClient(localchan.ClientConfig{ConnectWindow: -1, Dial: func(ctx context.Context) (net.Conn, error) { dials++; return d(ctx) }})
		resp, err := cl.Call(t.Context(), localchan.Request{Cmd: "debug"}, localchan.CallOptions{Timeout: time.Second})
		if err != nil || resp.Error == nil || resp.Error.Code != localchan.CodeTimeout {
			t.Fatalf("the call of a full engine: %+v, %v; want the time limit", resp, err)
		}
		// 1 s + the grace, with waits of 100 ms doubling up to 2 s, each
		// between half of it and all of it: a handful of attempts, not
		// thousands.
		if dials < 2 || dials > 12 {
			t.Fatalf("%d connection attempts in %v; want a few, with a growing wait between them", dials, time.Second+localchan.TimeoutGrace)
		}
		_ = a.Close()
		_ = b.Close()
	})
}

// The group a peer counts in is its terminal's cgroup, whatever is below it.
func TestTheGroupOfAPeerIsItsTerminal(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"/mestre-launcher.service/term-A":         "/mestre-launcher.service/term-A",
		"/mestre-launcher.service/term-A/child/x": "/mestre-launcher.service/term-A",
		"/user.slice/session.scope":               "/user.slice/session.scope",
		"/":                                       "/",
	} {
		if got := localchan.TerminalGroup(in); got != want {
			t.Errorf("TerminalGroup(%q) = %q; want %q", in, got, want)
		}
	}
}
