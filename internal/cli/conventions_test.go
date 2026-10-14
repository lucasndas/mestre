package cli_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/cli"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/testkit/localrig"
	"github.com/lucasndas/mestre/pkg/clock"
)

// futureCommands are commands of later milestones, with the level their
// documents give them, so that the conventions that depend on the level are
// tested on the real shapes before the commands exist
// (docs/cli/grao-mestre.md, coordenacao.md, coordenacao-github.md,
// comunicacao.md).
func futureCommands() []cli.Spec {
	mk := func(name, group string, who cli.Who) cli.Spec {
		return cli.Spec{Name: name, Group: group, Summary: "test " + name, Usage: "mestre " + name, Who: who, MaxArgs: -1}
	}
	reply := mk("reply", cli.GroupCommunication, cli.WhoGrandMaster|cli.WhoTableMaster)
	reply.Secret = true
	notify := mk("notify", cli.GroupGrandMaster, cli.WhoGrandMaster)
	notify.Secret = true
	notify.Flags = []cli.Flag{{Name: "table", Kind: cli.FlagString, Desc: "the table of the notice"}}
	done := mk("done", cli.GroupCommunication, cli.WhoAnyone)
	done.Queueable = true
	issue := mk("issue comment", cli.GroupGitHub, cli.WhoTableMaster|cli.WhoGrandMaster)
	issue.ClassCanGrant = true
	return []cli.Spec{
		mk("table", cli.GroupGrandMaster, cli.WhoGrandMaster),
		mk("gh", cli.GroupGrandMaster, cli.WhoGrandMaster),
		mk("app", cli.GroupGrandMaster, cli.WhoGrandMaster),
		notify,
		mk("recruit", cli.GroupCoordination, cli.WhoTableMaster|cli.WhoGrandMaster),
		mk("pr open", cli.GroupGitHub, cli.WhoAnyone),
		reply, done, issue,
	}
}

func extendedRegistry() *cli.Registry {
	return cli.MustRegistry(append(cli.Default().Specs(), futureCommands()...)...)
}

// allowing is the profile of a character of the given role whose class lists
// the commands in cli.allow.
func allowing(role cli.Role, commands ...string) cli.Profile {
	return cli.Profile{Role: role, AllowsCLI: func(c string) bool { return slices.Contains(commands, c) }}
}

// gatedBubble runs f in a synctest bubble with an engine whose calls go
// through the Gate of the conventions, over a forwarder the test fills.
func gatedBubble(t *testing.T, profiles map[string]cli.Profile, f func(t *testing.T, r *localrig.Rig, fwd *localchan.LocalForwarder)) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		fwd := localchan.NewLocalForwarder()
		gate := cli.NewGate(cli.GateConfig{
			Commands: extendedRegistry(),
			Next:     fwd,
			Profiles: cli.ProfilesFunc(func(_ context.Context, id string) cli.Profile { return profiles[id] }),
		})
		r := localrig.New(clock.Real{}, localrig.Options{Config: localchan.Config{
			Forwarder: gate, Local: map[string]localchan.Handler{localchan.CmdHelp: gate.Help()},
		}})
		r.Start(t)
		f(t, r, fwd)
	})
}

// runWith runs the CLI of p with the given definition of the commands.
func (p *proc) runWith(ctx context.Context, reg *cli.Registry, args ...string) result {
	p.commands = reg
	defer func() { p.commands = nil }()
	return p.run(ctx, args...)
}

type jsonResult struct {
	OK    bool `json:"ok"`
	Data  map[string]any
	Error struct {
		Code, Message, Hint string
		Details             map[string]any
	}
}

// decode reads the one object a --json run must leave on stdout.
func decode(t *testing.T, got result) jsonResult {
	t.Helper()
	var v jsonResult
	dec := json.NewDecoder(strings.NewReader(got.stdout))
	if err := dec.Decode(&v); err != nil || dec.More() {
		t.Fatalf("stdout is not one JSON object: %q (%v)", got.stdout, err)
	}
	return v
}

// The exit code of each error code, from the table of docs/cli/convencoes.md.
var exitOf = map[string]int{
	"internal": 1, "usage": 2, "denied": 3, "not_identified": 3, "level_denied": 3, "class_denied": 3, "not_connected": 3,
	"secret_detected": 3, "permission_prompt": 3, "pinned": 3,
	"not_found": 4, "not_visible": 4, "no_capable_machine": 4,
	"unavailable": 5, "engine_unreachable": 5, "citadel_unavailable": 5, "citadel_in_failover": 5, "protocol_mismatch": 5,
	"machine_unreachable": 5, "timeout": 6, "conflict": 7, "busy": 7, "limit": 8, "pending": 9, "scheduled": 9,
}

// CA-CLI-CONV-01: any command with --json, when it fails, leaves one object
// with ok false and error.code on stdout, nothing on stderr, and the exit
// code of the table. Every command of the definition, in each way it can
// fail: a bad flag, a caller without an id, an engine that is out, and each
// error the engine can answer.
func TestEveryCommandThatFailsWithJSONLeavesOneObject(t *testing.T) {
	t.Parallel()
	var local, remote []cli.Spec
	for _, s := range cli.Default().Specs() {
		if s.Local {
			local = append(local, s)
		} else {
			remote = append(remote, s)
		}
	}
	if len(remote) == 0 || len(local) == 0 {
		t.Fatalf("the definition has %d remote and %d local commands", len(remote), len(local))
	}
	check := func(t *testing.T, got result, wantCode string) {
		t.Helper()
		v := decode(t, got)
		if v.OK || v.Error.Code != wantCode || v.Error.Message == "" || v.Error.Hint == "" || v.Error.Details == nil {
			t.Fatalf("object = %+v for %q, want ok false and code %s", v, got.stdout, wantCode)
		}
		if got.stderr != "" {
			t.Fatalf("stderr = %q, want empty with --json", got.stderr)
		}
		if got.code != exitOf[wantCode] || got.code == 0 {
			t.Fatalf("exit = %d, want %d (%s)", got.code, exitOf[wantCode], wantCode)
		}
	}
	for _, s := range cli.Default().Specs() {
		t.Run(s.Name+" with a flag it does not have", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				r := localrig.New(clock.Real{}, localrig.Options{})
				p := newProc(r, "alice-proc", "", "alice")
				check(t, p.run(t.Context(), s.Name, "--nope", "--json"), "usage")
			})
		})
	}
	for _, s := range remote {
		args := slices.Repeat([]string{"x"}, s.MinArgs)
		t.Run(s.Name+" without an id", func(t *testing.T) {
			t.Parallel()
			bubble(t, func(t *testing.T, r *localrig.Rig) {
				p := newProc(r, "a-person", "", "")
				check(t, p.run(t.Context(), append(append([]string{s.Name}, args...), "--json")...), "not_identified")
			})
		})
		t.Run(s.Name+" with the engine out", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				r := localrig.New(clock.Real{}, localrig.Options{}) // never started
				p := newProc(r, "alice-proc", "", "alice")
				check(t, p.run(t.Context(), append(append([]string{s.Name}, args...), "--json")...), "engine_unreachable")
			})
		})
		for code, exit := range exitOf {
			t.Run(fmt.Sprintf("%s answered %s", s.Name, code), func(t *testing.T) {
				t.Parallel()
				bubble(t, func(t *testing.T, r *localrig.Rig) {
					r.Character("tA", "alice")
					p := newProc(r, "alice-proc", "tA", "alice")
					respondWith(r, s.Name, localchan.Fail(code, "it failed", "do this"))
					got := p.run(t.Context(), append(append([]string{s.Name}, args...), "--json")...)
					check(t, got, code)
					if got.code != exit {
						t.Fatalf("exit = %d, want %d", got.code, exit)
					}
				})
			})
		}
		t.Run(s.Name+" in text: two lines on stderr", func(t *testing.T) {
			t.Parallel()
			bubble(t, func(t *testing.T, r *localrig.Rig) {
				r.Character("tA", "alice")
				p := newProc(r, "alice-proc", "tA", "alice")
				// an engine that gives no hint, and a message with a newline
				respondWith(r, s.Name, localchan.Fail("conflict", "it is\nbusy", ""))
				got := p.run(t.Context(), append([]string{s.Name}, args...)...)
				lines := strings.Split(strings.TrimSuffix(got.stderr, "\n"), "\n")
				if got.code != 7 || got.stdout != "" || len(lines) != 2 || lines[0] != "error: it is busy" || !strings.HasPrefix(lines[1], "hint: ") || len(lines[1]) < 10 {
					t.Fatalf("result = %+v", got)
				}
			})
		})
	}
	for _, s := range local {
		if s.Name != "debug" {
			continue // help works without an engine, which is the point of it
		}
		t.Run("debug with the engine out", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				r := localrig.New(clock.Real{}, localrig.Options{})
				p := newProc(r, "alice-proc", "", "alice")
				check(t, p.run(t.Context(), "debug", "--json"), "engine_unreachable")
			})
		})
	}
	t.Run("help of a command that does not exist", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			r := localrig.New(clock.Real{}, localrig.Options{})
			p := newProc(r, "a-person", "", "")
			check(t, p.run(t.Context(), "help", "nonsense", "--json"), "usage")
			check(t, p.run(t.Context(), "nonsense", "--json"), "usage")
			check(t, p.run(t.Context(), "--json"), "usage")
		})
	})
}

// A success with --json is also one object, with ok true and data.
func TestASuccessWithJSONIsOneObjectWithData(t *testing.T) {
	t.Parallel()
	bubble(t, func(t *testing.T, r *localrig.Rig) {
		r.Character("tA", "alice")
		p := newProc(r, "alice-proc", "tA", "alice")
		respondWith(r, "list", localchan.OK(nil))
		got := p.run(t.Context(), "list", "--json")
		if v := decode(t, got); !v.OK || v.Data == nil || got.code != 0 || got.stderr != "" {
			t.Fatalf("result = %+v, object %+v", got, v)
		}
	})
}

// CA-CLI-CONV-03, with the cgroup of the terminal (Linux): the id of a
// character used by a process outside its terminal, or inside the terminal of
// another character, is refused with 3 and not_identified, and nothing runs.
// The real processes are the full suite's (CA-LOCAL-11 and CA-LOCAL-13, and
// the outside process of the same criterion).
func TestTheIdOfACharacterUsedOutsideItsTerminalIsRefused(t *testing.T) {
	t.Parallel()
	bubble(t, func(t *testing.T, r *localrig.Rig) {
		r.Character("tA", "alice")
		r.Character("tB", "bob")
		var ran atomic.Int32
		r.Forwarder.Handle("list", localchan.HandlerFunc(func(context.Context, *localchan.Call) localchan.Response {
			ran.Add(1)
			return localchan.OK(map[string]any{"text": "the list"})
		}))
		inside := newProc(r, "alice-proc", "tA", "alice")
		outside := newProc(r, "outside", "", "alice")
		other := newProc(r, "bobs-terminal", "tB", "alice")
		for name, p := range map[string]*proc{"outside any terminal": outside, "in the terminal of another character": other} {
			got := p.run(t.Context(), "list", "--json")
			if v := decode(t, got); got.code != 3 || v.OK || v.Error.Code != "not_identified" || got.stderr != "" {
				t.Errorf("%s: %+v", name, got)
			}
			if text := p.run(t.Context(), "list"); text.code != 3 || text.stdout != "" || !strings.HasPrefix(text.stderr, "error: ") {
				t.Errorf("%s, in text: %+v", name, text)
			}
		}
		if ran.Load() != 0 {
			t.Fatal("the command ran for a process outside the terminal")
		}
		if got := inside.run(t.Context(), "list"); got.code != 0 || got.stdout != "the list\n" {
			t.Fatalf("inside the terminal: %+v", got)
		}
	})
}

// Without an id the CLI does not even call the engine, so a person in a
// shell does not wait for an engine that is restarting.
func TestWithoutAnIdTheCLIDoesNotWaitForTheEngine(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := localrig.New(clock.Real{}, localrig.Options{}) // never started
		p := newProc(r, "a-person", "", "")
		begin := clock.Real{}.Now()
		got := p.run(t.Context(), "ask", "Reviewer", "hello")
		if got.code != 3 || !strings.Contains(got.stderr, "not_identified") && !strings.Contains(got.stderr, "MESTRE_AGENT_ID") {
			t.Fatalf("result = %+v", got)
		}
		if p.dials.Load() != 0 || clock.Since(clock.Real{}, begin) > time.Second {
			t.Fatalf("%d connections in %v", p.dials.Load(), clock.Since(clock.Real{}, begin))
		}
	})
}

// Part of CA-CLI-CONV-08 (it closes in milestone 5, when table, gh, app and
// notify exist; recruit arrives in milestone 4): an ordinary character's
// `mestre help` lists none of the commands of a higher level, and the
// engine decides it by the level and by cli.allow of the class.
func TestHelpListsOnlyWhatTheCallerMayUse(t *testing.T) {
	t.Parallel()
	profiles := map[string]cli.Profile{
		"alice": allowing(cli.RoleCharacter),
		"carol": allowing(cli.RoleCharacter, "issue comment"),
		"tm":    allowing(cli.RoleTableMaster),
		"gm":    allowing(cli.RoleGrandMaster),
	}
	listed := func(t *testing.T, p *proc) []string {
		t.Helper()
		text := p.run(t.Context(), "help")
		if text.code != 0 || text.stderr != "" {
			t.Fatalf("help = %+v", text)
		}
		js := p.run(t.Context(), "help", "--json")
		var v struct{ Data struct{ Commands []string } }
		if err := json.Unmarshal([]byte(js.stdout), &v); err != nil || js.code != 0 {
			t.Fatalf("help --json = %+v (%v)", js, err)
		}
		for _, c := range v.Data.Commands {
			if !strings.Contains(text.stdout, "mestre "+c) {
				t.Errorf("%q is in the JSON list but not in the text", c)
			}
		}
		return v.Data.Commands
	}
	gatedBubble(t, profiles, func(t *testing.T, r *localrig.Rig, _ *localchan.LocalForwarder) {
		for _, a := range []string{"alice", "carol", "tm", "gm"} {
			r.Character("t-"+a, a)
		}
		proc := func(a string) *proc {
			p := newProc(r, a+"-proc", "t-"+a, a)
			p.commands = extendedRegistry()
			return p
		}
		hidden := []string{"table", "gh", "app", "notify", "recruit"}
		alice := listed(t, proc("alice"))
		for _, h := range hidden {
			if slices.Contains(alice, h) {
				t.Errorf("an ordinary character sees %q in the help: %v", h, alice)
			}
		}
		for _, want := range []string{"list", "ask", "help", "debug", "done", "pr open"} {
			if !slices.Contains(alice, want) {
				t.Errorf("an ordinary character does not see %q: %v", want, alice)
			}
		}
		if slices.Contains(alice, "issue comment") || slices.Contains(alice, "reply") {
			t.Errorf("a class that lists nothing sees %v", alice)
		}
		if carol := listed(t, proc("carol")); !slices.Contains(carol, "issue comment") || slices.Contains(carol, "table") {
			t.Errorf("the class that lists `issue comment` sees %v", carol)
		}
		if tm := listed(t, proc("tm")); !slices.Contains(tm, "recruit") || !slices.Contains(tm, "reply") || slices.Contains(tm, "table") || slices.Contains(tm, "notify") {
			t.Errorf("the Master of the table sees %v", tm)
		}
		if gm := listed(t, proc("gm")); !slices.Contains(gm, "table") || !slices.Contains(gm, "notify") || !slices.Contains(gm, "gh") {
			t.Errorf("the Grand Master sees %v", gm)
		}
		// a person in a shell has no character: the commands of every character
		person := newProc(r, "a-person", "", "")
		person.commands = extendedRegistry()
		if got := listed(t, person); slices.Contains(got, "table") || !slices.Contains(got, "list") {
			t.Errorf("a person sees %v", got)
		}
	})
}

// The engine checks the level and the class at every call, whatever the
// help showed (docs/cli/convencoes.md#quem-pode-usar-nível).
func TestTheEngineRefusesACommandAboveTheLevelOrTheClass(t *testing.T) {
	t.Parallel()
	profiles := map[string]cli.Profile{
		"alice": allowing(cli.RoleCharacter),
		"carol": allowing(cli.RoleCharacter, "issue comment"),
		"tm":    allowing(cli.RoleTableMaster),
	}
	gatedBubble(t, profiles, func(t *testing.T, r *localrig.Rig, fwd *localchan.LocalForwarder) {
		var ran atomic.Int32
		for _, c := range []string{"table", "recruit", "issue comment", "list"} {
			fwd.Handle(c, localchan.HandlerFunc(func(context.Context, *localchan.Call) localchan.Response {
				ran.Add(1)
				return localchan.OK(map[string]any{"text": "done"})
			}))
		}
		reg := extendedRegistry()
		for _, a := range []string{"alice", "carol", "tm"} {
			r.Character("t-"+a, a)
		}
		cases := []struct {
			who, cmd, code string
			exit           int
		}{
			{"alice", "table", "level_denied", 3},
			{"alice", "recruit", "level_denied", 3},
			{"tm", "table", "level_denied", 3},
			{"alice", "issue comment", "class_denied", 3},
			{"carol", "issue comment", "", 0},
			{"tm", "issue comment", "", 0},
			{"tm", "recruit", "", 0},
			{"alice", "list", "", 0},
		}
		for _, tt := range cases {
			p := newProc(r, tt.who+"-"+strings.ReplaceAll(tt.cmd, " ", "-"), "t-"+tt.who, tt.who)
			before := ran.Load()
			got := p.runWith(t.Context(), reg, append(strings.Fields(tt.cmd), "--json")...)
			v := decode(t, got)
			if got.code != tt.exit || v.Error.Code != tt.code || got.stderr != "" {
				t.Errorf("%s runs `%s`: %+v", tt.who, tt.cmd, got)
			}
			if wantRan := tt.code == ""; (ran.Load() > before) != wantRan {
				t.Errorf("%s runs `%s`: the command ran = %v", tt.who, tt.cmd, ran.Load() > before)
			}
		}
	})
}

// Part of CA-CLI-CONV-10 (it closes in milestone 5, with the `reply` of the
// Grand Master): the text with a token of the shape `ghp_…` is refused with
// 3 and secret_detected, by the detector of the log masking, and nothing is
// sent. The token is made up.
func TestATextThatLooksLikeASecretIsRefusedAndNothingIsSent(t *testing.T) {
	t.Parallel()
	fakeToken := "ghp_" + "0123456789abcdefghijklmnopqrstuvwxyz"
	profiles := map[string]cli.Profile{"gm": allowing(cli.RoleGrandMaster)}
	gatedBubble(t, profiles, func(t *testing.T, r *localrig.Rig, fwd *localchan.LocalForwarder) {
		var sent []string
		for _, c := range []string{"reply", "notify"} {
			fwd.Handle(c, localchan.HandlerFunc(func(_ context.Context, call *localchan.Call) localchan.Response {
				args, _ := call.Request.Strings()
				sent = append(sent, args...)
				return localchan.OK(map[string]any{"text": "sent"})
			}))
		}
		r.Character("t-gm", "gm")
		reg := extendedRegistry()
		p := newProc(r, "gm-proc", "t-gm", "gm")
		for _, args := range [][]string{
			{"reply", "use this token: " + fakeToken},
			{"notify", "the token is " + fakeToken, "--json"},
			{"notify", "ok", "--table", "key " + fakeToken},
		} {
			got := p.runWith(t.Context(), reg, append(args, "--json")...)
			v := decode(t, got)
			if got.code != 3 || v.OK || v.Error.Code != "secret_detected" || got.stderr != "" {
				t.Errorf("mestre %v = %+v", args, got)
			}
			if strings.Contains(got.stdout, fakeToken) {
				t.Errorf("the refusal repeats the token: %s", got.stdout)
			}
		}
		if len(sent) != 0 {
			t.Fatalf("sent %q after the refusals", sent)
		}
		// text with no secret goes through
		if got := p.runWith(t.Context(), reg, "reply", "all good, see PR 42"); got.code != 0 || len(sent) != 1 {
			t.Fatalf("ordinary text: %+v, sent %q", got, sent)
		}
	})
}

// countingForwarder counts what passed the Gate.
type countingForwarder struct{ n atomic.Int32 }

func (f *countingForwarder) Forward(context.Context, *localchan.Call) localchan.Response {
	f.n.Add(1)
	return localchan.OK(nil)
}

// Part of CA-CLI-CONV-10: a process of the character can write the request
// itself, past the CLI. The engine then refuses, as a usage error and
// sending nothing, what the CLI would never send: an argument of a Secret
// command that is not text (a number, a list holding the token), a flag the
// command does not have, a flag with a value of another kind; a token in a
// flag of the command is still a secret.
func TestARequestWrittenPastTheCLIDoesNotSlipASecretThrough(t *testing.T) {
	t.Parallel()
	fakeToken := "ghp_" + "0123456789abcdefghijklmnopqrstuvwxyz"
	tok, _ := json.Marshal(fakeToken)
	list, _ := json.Marshal([]string{fakeToken})
	obj, _ := json.Marshal(map[string]string{"t": fakeToken})
	next := &countingForwarder{}
	g := cli.NewGate(cli.GateConfig{Commands: extendedRegistry(), Next: next,
		Profiles: cli.ProfilesFunc(func(context.Context, string) cli.Profile { return allowing(cli.RoleGrandMaster) })})
	ok := json.RawMessage(`"ok"`)
	for name, c := range map[string]struct {
		req  localchan.Request
		code string
	}{
		"a number among the arguments": {localchan.Request{Cmd: "reply", Args: []json.RawMessage{tok, json.RawMessage(`1`)}}, localchan.CodeUsage},
		"a list holding the token":     {localchan.Request{Cmd: "reply", Args: []json.RawMessage{list}}, localchan.CodeUsage},
		"an object holding the token":  {localchan.Request{Cmd: "notify", Args: []json.RawMessage{obj}}, localchan.CodeUsage},
		"a flag of another command":    {localchan.Request{Cmd: "notify", Args: []json.RawMessage{ok}, Flags: map[string]any{"secret": fakeToken}}, localchan.CodeUsage},
		"a flag as a list":             {localchan.Request{Cmd: "notify", Args: []json.RawMessage{ok}, Flags: map[string]any{"table": []any{fakeToken}}}, localchan.CodeUsage},
		"a flag as an object":          {localchan.Request{Cmd: "notify", Args: []json.RawMessage{ok}, Flags: map[string]any{"table": map[string]any{"x": fakeToken}}}, localchan.CodeUsage},
		"the token in the flag":        {localchan.Request{Cmd: "notify", Args: []json.RawMessage{ok}, Flags: map[string]any{"table": fakeToken}}, "secret_detected"},
		"an unknown flag of ask":       {localchan.Request{Cmd: "ask", Args: []json.RawMessage{ok, ok}, Flags: map[string]any{"wait": "1m"}}, localchan.CodeUsage},
		"a timeout that is not text":   {localchan.Request{Cmd: "ask", Args: []json.RawMessage{ok, ok}, Flags: map[string]any{"timeout": 60}}, localchan.CodeUsage},
	} {
		resp := g.Forward(t.Context(), &localchan.Call{Request: c.req, Caller: localchan.Caller{Identified: true, AgentID: "gm"}})
		if resp.OK || resp.Error == nil || resp.Error.Code != c.code {
			t.Errorf("%s: %+v; want %s", name, resp, c.code)
		} else if strings.Contains(resp.Error.Message, fakeToken) {
			t.Errorf("%s: the refusal repeats the token", name)
		}
	}
	if n := next.n.Load(); n != 0 {
		t.Fatalf("%d requests passed the Gate", n)
	}
	// What the CLI sends still passes.
	if resp := g.Forward(t.Context(), &localchan.Call{Request: localchan.Request{Cmd: "notify", Args: []json.RawMessage{ok}, Flags: map[string]any{"table": "1-login"}},
		Caller: localchan.Caller{Identified: true, AgentID: "gm"}}); !resp.OK || next.n.Load() != 1 {
		t.Fatalf("an ordinary notify: %+v", resp)
	}
}

// Part of CA-CLI-CONV-07 (it closes in milestone 4, with `ask --raw` and the
// state of the terminals): the rule that decides it. A target waiting for a
// permission is `permission_prompt` with exit 3, a busy one `busy` with exit
// 7, and in neither case anything is typed; a free one, or one blocked on a
// screen nobody recognizes (input), takes the keys.
func TestRawIsNotTypedIntoATargetThatIsNotFree(t *testing.T) {
	t.Parallel()
	cases := []struct {
		state, reason string
		allowed       bool
		code          string
		exit          int
	}{
		{cli.StateIdle, "", true, "", 0},
		{cli.StateBlocked, cli.ReasonInput, true, "", 0},
		{cli.StateBlocked, cli.ReasonPermission, false, "permission_prompt", 3},
		{cli.StateBusy, "", false, "busy", 7},
		{cli.StateBlocked, "quota", false, "busy", 7},
		{"stuck", "", false, "busy", 7},
		{"stopped", "", false, "busy", 7},
	}
	for _, tt := range cases {
		resp, ok := cli.CheckRaw(tt.state, tt.reason)
		if ok != tt.allowed {
			t.Errorf("%s/%s: allowed = %v", tt.state, tt.reason, ok)
			continue
		}
		if !ok && (resp.Error == nil || resp.Error.Code != tt.code || resp.ExitCode() != tt.exit || resp.Error.Hint == "") {
			t.Errorf("%s/%s: %+v", tt.state, tt.reason, resp.Error)
		}
	}
}

// Part of CA-CLI-CONV-11 and CA-CLI-CONV-05 (they close with `pr open` in
// milestone 5 and `done` in milestone 4): a request that was accepted and
// waits is not a success. With --json it is ok with queued and op_id and
// exit 0; in text it starts with `PENDING:` and says nothing of success.
func TestAQueuedRequestIsPendingNotDone(t *testing.T) {
	t.Parallel()
	bubble(t, func(t *testing.T, r *localrig.Rig) {
		r.Character("tA", "alice")
		p := newProc(r, "alice-proc", "tA", "alice")
		reg := extendedRegistry()
		for _, cmd := range []string{"pr open", "done"} {
			respondWith(r, cmd, cli.Queued("01J0000000000000000000OP1"))
			js := p.runWith(t.Context(), reg, append(strings.Fields(cmd), "--json")...)
			v := decode(t, js)
			if js.code != 0 || !v.OK || v.Data["queued"] != true || v.Data["op_id"] != "01J0000000000000000000OP1" || js.stderr != "" {
				t.Fatalf("%s --json: %+v", cmd, js)
			}
			text := p.runWith(t.Context(), reg, strings.Fields(cmd)...)
			if text.code != 0 || !strings.HasPrefix(text.stdout, "PENDING: "+cmd+" queued (op 01J0000000000000000000OP1)") ||
				!strings.Contains(text.stdout, "inbox") || strings.Contains(text.stdout, "opened") || text.stderr != "" {
				t.Fatalf("%s in text: %+v", cmd, text)
			}
		}
	})
}

// `mestre help <command>` and `mestre <command> --help` show the usage, the
// arguments, the flags, the level, an example and the exit codes, and need no
// engine.
func TestCommandHelpShowsUsageLevelExampleAndExitCodes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := localrig.New(clock.Real{}, localrig.Options{}) // no engine
		p := newProc(r, "a-person", "", "")
		for _, args := range [][]string{{"help", "ask"}, {"ask", "--help"}, {"ask", "-h"}} {
			got := p.run(t.Context(), args...)
			for _, want := range []string{`usage: mestre ask "<name>" "<prompt>" [--timeout <dur>]`, "<name>", "--timeout", "--json", "who: any character", "example: mestre ask", "exit codes:", "  6  timeout"} {
				if !strings.Contains(got.stdout, want) {
					t.Errorf("mestre %v lacks %q:\n%s", args, want, got.stdout)
				}
			}
			if got.code != 0 || got.stderr != "" {
				t.Errorf("mestre %v = exit %d, stderr %q", args, got.code, got.stderr)
			}
		}
		js := p.run(t.Context(), "help", "ask", "--json")
		var v struct {
			Data struct {
				Name, Usage, Example, Level string
				Flags                       []struct{ Name, Kind string }
				ExitCodes                   []int `json:"exit_codes"`
			}
		}
		if err := json.Unmarshal([]byte(js.stdout), &v); err != nil || v.Data.Name != "ask" || len(v.Data.Flags) != 1 || v.Data.Flags[0].Kind != "duration" ||
			!slices.Contains(v.Data.ExitCodes, 6) || v.Data.Level != "any character" || v.Data.Example == "" {
			t.Fatalf("help ask --json = %+v (%v)", js, err)
		}
		if p.dials.Load() != 0 {
			t.Error("the help of a command called the engine")
		}
	})
}

// A lone `--` ends the flags, so a text that starts with `--` is an argument.
func TestTextAfterDoubleDashIsAnArgument(t *testing.T) {
	t.Parallel()
	bubble(t, func(t *testing.T, r *localrig.Rig) {
		r.Character("tA", "alice")
		p := newProc(r, "alice-proc", "tA", "alice")
		var args []string
		r.Forwarder.Handle("ask", localchan.HandlerFunc(func(_ context.Context, c *localchan.Call) localchan.Response {
			args, _ = c.Request.Strings()
			return localchan.OK(map[string]any{"response": "ok"})
		}))
		if got := p.run(t.Context(), "ask", "Reviewer", "--", "--timeout is a word here"); got.code != 0 || !slices.Equal(args, []string{"Reviewer", "--timeout is a word here"}) {
			t.Fatalf("result = %+v, args %q", got, args)
		}
		if got := p.run(t.Context(), "ask", "Reviewer", "--timeout=2m", "go"); got.code != 0 || !slices.Equal(args, []string{"Reviewer", "go"}) {
			t.Fatalf("--timeout=2m: %+v, args %q", got, args)
		}
	})
}

// Item 5 of `mestre debug` is the answer of the engine about the Citadel: ok
// when it reaches it, fail with the code and a hint (exit 5) when not.
func TestDebugItemFiveIsWhatTheEngineSaysOfTheCitadel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		citadel   map[string]any
		status    string
		exit      int
		code      string
		wantInDet string
	}{
		{"reachable", map[string]any{"reachable": true, "protocol": 1, "epoch": 4, "detail": "connected to the Citadel (protocol v1, epoch 4)"}, "ok", 0, "", "epoch 4"},
		{"away", map[string]any{"reachable": false, "code": "citadel_unavailable", "detail": "this Tower has no connection to the Citadel"}, "fail", 5, "citadel_unavailable", "no connection"},
		{"in failover", map[string]any{"reachable": false, "code": "citadel_in_failover", "detail": "another machine is taking the place of the Citadel"}, "fail", 5, "citadel_in_failover", "taking the place"},
		{"not said", nil, "skipped", 0, "", "did not say"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				cfg := localchan.Config{}
				if tt.citadel != nil {
					cfg.DebugExtra = func() map[string]any { return map[string]any{"citadel": tt.citadel} }
				}
				r := localrig.New(clock.Real{}, localrig.Options{Config: cfg})
				r.Start(t)
				r.Character("tA", "alice")
				p := newProc(r, "alice-proc", "tA", "alice")
				js := p.run(t.Context(), "debug", "--json")
				var v struct {
					OK    bool
					Error struct{ Code, Hint string }
					Data  struct {
						Checks []struct{ Name, Status, Detail, Hint string }
					}
				}
				if err := json.Unmarshal([]byte(js.stdout), &v); err != nil || len(v.Data.Checks) != 5 {
					t.Fatalf("%q: %v", js.stdout, err)
				}
				c5 := v.Data.Checks[4]
				if c5.Name != "citadel" || c5.Status != tt.status || !strings.Contains(c5.Detail, tt.wantInDet) || js.code != tt.exit || v.Error.Code != tt.code {
					t.Fatalf("item 5 = %+v, exit %d, error %+v", c5, js.code, v.Error)
				}
				if tt.status == "fail" && (c5.Hint == "" || v.Error.Hint == "") {
					t.Fatalf("a failure with no hint: %+v", v)
				}
				text := p.run(t.Context(), "debug")
				if lines := strings.Split(text.stdout, "\n"); text.code != tt.exit || !strings.HasPrefix(lines[4], "5. citadel") || !strings.Contains(lines[4], tt.status) {
					t.Fatalf("text:\n%s", text.stdout)
				}
			})
		})
	}
}
