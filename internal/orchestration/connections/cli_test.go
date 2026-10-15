package connections_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/lucasndas/mestre/internal/cli"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/connections"
	"github.com/lucasndas/mestre/internal/testkit/localrig"
	"github.com/lucasndas/mestre/pkg/clock"
)

// cliWorld is a world whose commands are served by the engine of the local
// channel: the CLI runs in the terminal of a character, the Gate checks the
// level, and the handlers of the connections answer.
type cliWorld struct {
	*world
	rig *localrig.Rig
	// asked counts the `ask` that got past the guard.
	asked atomic.Int32
}

type result struct {
	code           int
	stdout, stderr string
}

// cliBubble runs f in a synctest bubble with the engine over the world.
func cliBubble(t *testing.T, w *world, f func(t *testing.T, cw *cliWorld)) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		fwd := localchan.NewLocalForwarder()
		w.svc.Register(fwd)
		cw := &cliWorld{world: w}
		// `ask` is stage 10's: a handler that only says it ran, behind the guard.
		fwd.Handle("ask", w.svc.Guard(localchan.HandlerFunc(func(context.Context, *localchan.Call) localchan.Response {
			cw.asked.Add(1)
			return localchan.OK(map[string]any{"response": "oi"})
		}), 0))
		gate := cli.NewGate(cli.GateConfig{Next: fwd, Profiles: cli.ProfilesFunc(func(ctx context.Context, id string) cli.Profile {
			c, err := w.reg.Get(ctx, id)
			if err != nil {
				return cli.Profile{}
			}
			return cli.Profile{Role: map[characters.Role]cli.Role{
				characters.RoleCharacter: cli.RoleCharacter, characters.RoleTableMaster: cli.RoleTableMaster, characters.RoleGrandMaster: cli.RoleGrandMaster,
			}[c.Role]}
		})})
		cw.rig = localrig.New(clock.Real{}, localrig.Options{Config: localchan.Config{
			Forwarder: gate, Local: map[string]localchan.Handler{localchan.CmdHelp: gate.Help()},
		}})
		cw.rig.Start(t)
		f(t, cw)
	})
}

// run runs `mestre` as a process of the terminal of the character.
func (cw *cliWorld) run(t *testing.T, c characters.Character, args ...string) result {
	t.Helper()
	term, host := "t-"+c.ID, "proc-"+c.ID
	cw.rig.Character(term, c.ID)
	cw.rig.Process(host, term)
	var stdout, stderr bytes.Buffer
	code := cli.Run(t.Context(), cli.Env{
		Getenv: func(k string) string {
			if k == localchan.EnvAgentID {
				return c.ID
			}
			return ""
		},
		Stdout: &stdout, Stderr: &stderr, Clock: cw.rig.Clock, Cwd: "/work", Version: "1.0.0",
		Dial:       func(string) localchan.Dialer { return cw.rig.Dialer(host) },
		FileExists: func(string) bool { return true },
	}, args)
	return result{code, stdout.String(), stderr.String()}
}

type jsonOut struct {
	OK    bool           `json:"ok"`
	Data  map[string]any `json:"data"`
	Error struct{ Code, Message, Hint string }
}

func decode(t *testing.T, r result) jsonOut {
	t.Helper()
	var v jsonOut
	if err := json.NewDecoder(strings.NewReader(r.stdout)).Decode(&v); err != nil {
		t.Fatalf("stdout is not JSON: %q (%v)", r.stdout, err)
	}
	return v
}

// CA-CONX-01: a table with the Master, Dev and QA recruited by the Master:
// Dev's `mestre list` shows the Master, not QA and not the Grand Master.
func TestListShowsTheDevOnlyTheMaster(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	gm := w.gm()
	tm := w.master("3-login", gm)
	dev := w.dev("3-login", "Dev", by(tm))
	w.dev("3-login", "QA", by(tm))
	cliBubble(t, w, func(t *testing.T, cw *cliWorld) {
		got := cw.run(t, dev, "list", "--json")
		if got.code != 0 || got.stderr != "" {
			t.Fatalf("exit %d: %+v", got.code, got)
		}
		v := decode(t, got)
		rows, _ := v.Data["agents"].([]any)
		if len(rows) != 1 {
			t.Fatalf("Dev sees %v, want only the Master", rows)
		}
		row := rows[0].(map[string]any)
		if row["name"] != "Mestre" || row["machine"] != "linux" || row["class"] != "Dev" || row["status"] != "idle" {
			t.Errorf("the Master as listed: %v", row)
		}
		if self, _ := v.Data["self"].(map[string]any); self["name"] != "Dev" || self["table"] != "3-login" || self["machine"] != "linux" || self["id"] != dev.ID {
			t.Errorf("self = %v", v.Data["self"])
		}
		text := cw.run(t, dev, "list")
		for _, bad := range []string{"QA", "Grão-Mestre"} {
			if strings.Contains(text.stdout, bad) {
				t.Errorf("the text of list shows %q:\n%s", bad, text.stdout)
			}
		}
		if !strings.HasPrefix(text.stdout, "you: Dev (id "+dev.ID+", table 3-login, machine linux)") || !strings.Contains(text.stdout, "  Mestre  class Dev") {
			t.Errorf("the text of list:\n%s", text.stdout)
		}
	})
}

// A waiting or blocked character is listed with the reason of the wait.
func TestListShowsTheReasonOfTheWait(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	gm := w.gm()
	tm := w.master("3-login", gm)
	dev := w.dev("3-login", "Dev", by(tm))
	rev := w.dev("3-login", "Revisor", by(tm))
	if _, err := w.svc.Connect(w.ctx, connectionsRequest(tm, "Dev", "Revisor")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.reg.Transition(w.ctx, rev.ID, characters.Waiting, characters.Change{Reason: "approval #3", At: w.clk.Now()}); err != nil {
		t.Fatal(err)
	}
	cliBubble(t, w, func(t *testing.T, cw *cliWorld) {
		out := cw.run(t, dev, "list")
		if !strings.Contains(out.stdout, "  Revisor  class Dev  machine linux  aguardando aprovação") {
			t.Errorf("list:\n%s", out.stdout)
		}
	})
}

// CA-CONX-06: a common character is refused by the level; the Grand Master
// makes the same connection in their table, with the reason manual.
func TestConnectIsRefusedByTheLevelAndTheGrandMasterMakesItManual(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	gm := w.gm()
	tm := w.master("3-login", gm)
	dev := w.dev("3-login", "Dev", by(tm))
	qa := w.dev("3-login", "QA", by(tm))
	rev := w.dev("3-login", "Revisor", by(tm))
	cliBubble(t, w, func(t *testing.T, cw *cliWorld) {
		got := cw.run(t, dev, "connect", "QA", "Revisor", "--json")
		if v := decode(t, got); got.code != 3 || v.Error.Code != "level_denied" {
			t.Fatalf("a common character: exit %d %+v", got.code, v.Error)
		}
		if w.reasons(ch(qa), ch(rev)) != "" {
			t.Fatal("the refused command connected them")
		}
		got = cw.run(t, gm, "connect", "QA", "Revisor", "--json")
		if v := decode(t, got); got.code != 2 || v.Error.Code != "table_required" {
			t.Fatalf("the Grand Master without --table: exit %d %+v", got.code, v.Error)
		}
		got = cw.run(t, gm, "connect", "QA", "Revisor", "--table", "3-login", "--json")
		v := decode(t, got)
		if got.code != 0 || v.Data["already_connected"] != false {
			t.Fatalf("the Grand Master: exit %d %+v", got.code, v)
		}
		if w.reasons(ch(qa), ch(rev)) != "manual" {
			t.Errorf("reasons: %q, want manual", w.reasons(ch(qa), ch(rev)))
		}
		// The same again, by the table master of that table: nothing more.
		got = cw.run(t, tm, "connect", "Revisor", "QA", "--json")
		if v := decode(t, got); got.code != 0 || v.Data["already_connected"] != true {
			t.Errorf("repeated: exit %d %+v", got.code, v)
		}
	})
}

// CA-CONX-04 through the CLI: the table master of table A names a character of
// table B and is refused with a usage error (exit 2).
func TestConnectOfAnItemOfAnotherTableIsRefused(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	gm := w.gm()
	tmA, tmB := w.master("3-login", gm), w.master("4-api", gm)
	w.dev("3-login", "Dev", by(tmA))
	b := w.dev("4-api", "Gemeo", by(tmB))
	cliBubble(t, w, func(t *testing.T, cw *cliWorld) {
		got := cw.run(t, tmA, "connect", "Dev", "Gemeo", "--json")
		if v := decode(t, got); got.code != 2 || v.Error.Code != "different_tables" {
			t.Fatalf("exit %d %+v, want 2 different_tables", got.code, v.Error)
		}
		if cs, _ := w.svc.Store().Of(w.ctx, ch(b)); len(cs) != 1 {
			t.Errorf("connections of the character of table B: %+v, want only its table master", cs)
		}
	})
}

// Part of CA-CONX-07 (the real `ask` closes it in internal/app, over the CLI): Dev, not connected to QA, asks QA and gets exit 3, not_connected
// and the message of the document; the command never reaches the handler.
func TestAskToACharacterNotConnectedIsRefusedWithNotConnected(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	gm := w.gm()
	tm := w.master("3-login", gm)
	dev := w.dev("3-login", "Dev", by(tm))
	w.dev("3-login", "QA", by(tm))
	tmB := w.master("4-api", gm)
	w.dev("4-api", "Gemeo", by(tmB))
	cliBubble(t, w, func(t *testing.T, cw *cliWorld) {
		got := cw.run(t, dev, "ask", "QA", "oi", "--json")
		v := decode(t, got)
		if got.code != 3 || v.Error.Code != "not_connected" || v.Error.Message != "QA não está conectado a você" || v.Error.Hint == "" {
			t.Fatalf("exit %d %+v", got.code, v.Error)
		}
		if got := cw.run(t, dev, "ask", "QA", "oi"); got.code != 3 || !strings.Contains(got.stderr, "QA não está conectado a você") {
			t.Errorf("text mode: exit %d, stderr %q", got.code, got.stderr)
		}
		// Another table, or no such name: not visible (exit 4).
		for _, name := range []string{"Gemeo", "Fantasma"} {
			got := cw.run(t, dev, "ask", name, "oi", "--json")
			if v := decode(t, got); got.code != 4 || v.Error.Code != "not_visible" {
				t.Errorf("ask %s: exit %d %+v, want 4 not_visible", name, got.code, v.Error)
			}
		}
		if cw.asked.Load() != 0 {
			t.Fatal("a refused ask reached the handler")
		}
		// Connected, the same command goes through.
		if _, err := w.svc.Connect(w.ctx, connectionsRequest(tm, "Dev", "QA")); err != nil {
			t.Fatal(err)
		}
		if got := cw.run(t, dev, "ask", "QA", "oi"); got.code != 0 || cw.asked.Load() != 1 {
			t.Errorf("connected: exit %d, asked %d", got.code, cw.asked.Load())
		}
	})
}

func connectionsRequest(caller characters.Character, a, b string) connections.ConnectRequest {
	return connections.ConnectRequest{Caller: caller, A: a, B: b}
}
