package cli_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/cli"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/testkit/localrig"
)

// capture answers a command and keeps the request the engine got.
func capture(r *localrig.Rig, cmd string) func() localchan.Request {
	var mu sync.Mutex
	var got localchan.Request
	r.Forwarder.Handle(cmd, localchan.HandlerFunc(func(_ context.Context, c *localchan.Call) localchan.Response {
		mu.Lock()
		got = c.Request
		mu.Unlock()
		return localchan.OK(map[string]any{"text": "ok"})
	}))
	return func() localchan.Request {
		mu.Lock()
		defer mu.Unlock()
		return got
	}
}

// The engine never opens a path an agent names: the CLI reads the file and
// sends the text, in the place the command says, and the path itself never
// goes (docs/cli/comunicacao.md, docs/cli/coordenacao.md).
func TestTheCLIReadsTheFilesAndSendsTheirText(t *testing.T) {
	t.Parallel()
	bubble(t, func(t *testing.T, r *localrig.Rig) {
		r.Character("tA", "alice")
		p := newProc(r, "alice-proc", "tA", "alice")
		p.files = map[string][]byte{"/work/login.yaml": []byte("name: Login\nsteps: []\n"), "/work/in.md": []byte("faça rápido"), "/work/nota.md": []byte("# Nota\ncorpo"),
			"/work/resumo.md": []byte("resumo longo")}
		check := func(cmd string, want func(localchan.Request) bool, args ...string) {
			t.Helper()
			got := capture(r, cmd)
			if res := p.run(t.Context(), args...); res.code != 0 {
				t.Fatalf("%v = %+v", args, res)
			}
			req := got()
			if !want(req) {
				t.Errorf("%v sent %+v", args, req)
			}
			if _, has := req.Flags["file"]; has {
				t.Errorf("%v sent the path in --file: %+v", args, req.Flags)
			}
			if _, has := req.Flags["input-file"]; has {
				t.Errorf("%v sent the path in --input-file: %+v", args, req.Flags)
			}
		}
		check("quest create", func(q localchan.Request) bool {
			return q.Flags["definition"] == "name: Login\nsteps: []\n"
		}, "quest", "create", "Login", "--file", "login.yaml")
		check("quest run", func(q localchan.Request) bool { return q.Flags["input"] == "faça rápido" }, "quest", "run", "Login", "--input-file", "/work/in.md")
		check("note create", func(q localchan.Request) bool {
			args, _ := q.Strings()
			return len(args) == 1 && args[0] == "# Nota\ncorpo"
		}, "note", "create", "--file", "nota.md")
		check("note write", func(q localchan.Request) bool {
			args, _ := q.Strings()
			return len(args) == 2 && args[0] == "Nota" && args[1] == "# Nota\ncorpo"
		}, "note", "write", "Nota", "--file", "nota.md")
		check("done", func(q localchan.Request) bool {
			args, _ := q.Strings()
			return len(args) == 1 && args[0] == "pronto\n\nresumo longo" && q.Flags["from-file"] == true
		}, "done", "pronto", "--file", "resumo.md")
		check("done", func(q localchan.Request) bool {
			args, _ := q.Strings()
			return len(args) == 1 && args[0] == "resumo longo" && q.Flags["from-file"] == true
		}, "done", "--file", "resumo.md")
		p.stdin = "da entrada"
		check("note create", func(q localchan.Request) bool {
			args, _ := q.Strings()
			return len(args) == 1 && args[0] == "da entrada"
		}, "note", "create", "--file", "-")
		p.stdin = "name: X\n"
		check("quest create", func(q localchan.Request) bool { return q.Flags["definition"] == "name: X\n" }, "quest", "create", "X", "--file", "-")
	})
}

// A file that is not there is not_found (4), one over the limit too_large
// (8), a binary one a usage error; the engine is not even called.
func TestTheFilesOfTheCLIAreCheckedBeforeAnythingIsSent(t *testing.T) {
	t.Parallel()
	bubble(t, func(t *testing.T, r *localrig.Rig) {
		r.Character("tA", "alice")
		p := newProc(r, "alice-proc", "tA", "alice")
		p.files = map[string][]byte{"/work/grande.md": []byte(strings.Repeat("x", 65<<10)), "/work/bin": {1, 0, 2}, "/work/ok.md": []byte("x")}
		got := capture(r, "done")
		cases := []struct {
			args []string
			code int
		}{
			{[]string{"done", "x", "--file", "nada.md"}, 4},
			{[]string{"done", "x", "--file", "grande.md"}, 8},
			{[]string{"done", "x", "--file", "bin"}, 2},
			{[]string{"quest", "run", "Q", "--input", "a", "--input-file", "ok.md"}, 2},
		}
		for _, c := range cases {
			res := p.run(t.Context(), c.args...)
			if res.code != c.code {
				t.Errorf("%v = %+v, want %d", c.args, res, c.code)
			}
		}
		if got().Cmd != "" {
			t.Errorf("the engine was called: %+v", got())
		}
	})
}

// The path of --attach is checked and only the file name goes; --attach can
// be repeated.
func TestReplyAttachmentsAreCheckedAndOnlyTheirNamesGo(t *testing.T) {
	t.Parallel()
	bubble(t, func(t *testing.T, r *localrig.Rig) {
		r.Character("tA", "alice")
		p := newProc(r, "alice-proc", "tA", "alice")
		p.files = map[string][]byte{"/work/a.png": {1}, "/work/dir/b.pdf": {2}}
		got := capture(r, "reply")
		if res := p.run(t.Context(), "reply", "veja", "--attach", "a.png", "--attach", "dir/b.pdf"); res.code != 0 {
			t.Fatalf("reply = %+v", res)
		}
		names, _ := got().Flags["attach"].([]any)
		if len(names) != 2 || names[0] != "a.png" || names[1] != "b.pdf" {
			t.Errorf("attach sent %v", got().Flags["attach"])
		}
		if res := p.run(t.Context(), "reply", "veja", "--attach", "a.png", "--attach", "nada.png"); res.code != 4 {
			t.Errorf("an attachment that does not exist = %+v", res)
		}
	})
}

// Every command of the documents is defined, with the level the documents give.
func TestTheCommandsOfCommunicationAndCoordinationAreDefined(t *testing.T) {
	t.Parallel()
	reg := cli.Default()
	for _, name := range []string{"list", "ask", "check", "note create", "note read", "note write", "note edit", "note stack", "note delete", "reply", "done", "wait", "inbox", "waits",
		"recruit", "spawn", "dismiss", "connect", "sheet list", "sheet show", "base list", "base show",
		"quest create", "quest run", "quest status", "quest pause", "quest resume", "quest cancel"} {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("%q is not defined", name)
		}
	}
	for name, who := range map[string]cli.Who{"recruit": cli.WhoTableMaster | cli.WhoGrandMaster, "spawn": cli.WhoTableMaster, "dismiss": cli.WhoTableMaster | cli.WhoGrandMaster,
		"reply": cli.WhoGrandMaster, "quest run": cli.WhoTableMaster, "quest status": cli.WhoTableMaster | cli.WhoGrandMaster} {
		if s, _ := reg.Get(name); s.Who != who {
			t.Errorf("%s is for %v, want %v", name, s.Who, who)
		}
	}
}
